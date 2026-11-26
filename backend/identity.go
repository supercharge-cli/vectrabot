package main

import (
	"bytes"
	"cmp"
	"errors"
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A bot's identity: which bot is the workspace's main one, its colour and
// the face it shows in every client (generated pattern, initial, emoji or an
// uploaded image, cropped to a shape).

// botPalette is the colour set every client offers. New bots get one no other
// bot wears yet, while any is left.
var botPalette = []string{"#22a06b", "#3b82f6", "#ef6461", "#f08c3a", "#8b5cf6", "#06b6d4", "#ec4899", "#d4a017", "#14b8a6", "#f97362"}

var avatarKinds = map[string]bool{"generated": true, "initial": true, "emoji": true, "image": true}
var avatarShapes = map[string]bool{"circle": true, "rounded": true, "square": true}
var glyphSeedRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// maxAvatarBytes caps an uploaded image; clients downscale before sending.
const maxAvatarBytes = 2 << 20

// maxBots keeps one workspace from growing an unbounded sidebar.
const maxBots = 60

func (s *botStore) freshColorLocked() string {
	worn := map[string]bool{}
	for _, b := range s.bots {
		worn[strings.ToLower(b.Color)] = true
	}
	var free []string
	for _, c := range botPalette {
		if !worn[c] {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		free = botPalette
	}
	return free[rand.IntN(len(free))]
}

// normalizeLocked repairs a loaded index: one main bot (the oldest when none
// is marked), valid colours, kinds and shapes. Reports whether it changed.
func (s *botStore) normalizeLocked() bool {
	changed := false
	mainIdx := -1
	for i, b := range s.bots {
		if b.Main {
			if mainIdx >= 0 {
				b.Main = false
				changed = true
				continue
			}
			mainIdx = i
		}
	}
	if mainIdx < 0 && len(s.bots) > 0 {
		oldest := 0
		for i, b := range s.bots {
			if b.CreatedAt < s.bots[oldest].CreatedAt {
				oldest = i
			}
		}
		s.bots[oldest].Main = true
		changed = true
	}
	for i, b := range s.bots {
		if !hexColor.MatchString(b.Color) {
			b.Color = botPalette[i%len(botPalette)]
			changed = true
		}
		if !avatarKinds[b.AvatarKind] || (b.AvatarKind == "image" && b.AvatarExt == "") || (b.AvatarKind == "emoji" && b.Emoji == "") {
			b.AvatarKind = "generated"
			changed = true
		}
		if !avatarShapes[b.AvatarShape] {
			b.AvatarShape = "circle"
			changed = true
		}
	}
	return changed
}

// sortedBotsLocked is the order every client lists bots in: the main bot,
// then pinned ones, then the rest by latest activity.
func (h *botHub) sortedViewsLocked() []botView {
	views := make([]botView, 0, len(h.store.bots))
	for _, b := range h.store.bots {
		views = append(views, h.viewLocked(b))
	}
	sort.SliceStable(views, func(i, j int) bool {
		a, b := views[i], views[j]
		if a.Main != b.Main {
			return a.Main
		}
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		ta, tb := max(a.LastAt, a.CreatedAt), max(b.LastAt, b.CreatedAt)
		if ta != tb {
			return ta > tb
		}
		return a.CreatedAt < b.CreatedAt
	})
	return views
}

// glyphOf is the bot's generated face: 15 bits of a 5x5 grid whose right two
// columns mirror the left two. Clients draw a set bit (row*3+col) as a cell.
func glyphOf(id string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	g := int(h.Sum32() & 0x7fff)
	for bitsOn(g) < 4 || bitsOn(g) > 11 {
		g = (g*31 + 0x2b5d) & 0x7fff
	}
	return g
}

func bitsOn(v int) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

func avatarURL(b *botRecord) *string {
	if b.AvatarExt == "" {
		return nil
	}
	u := "/api/bots/" + b.ID + "/avatar?v=" + strconv.FormatInt(b.AvatarRev, 10)
	return &u
}

func (s *botStore) avatarPath(b *botRecord) string {
	return filepath.Join(s.dir, "avatars", filepath.Base(b.ID)+"."+b.AvatarExt)
}

// sniffAvatar accepts only PNG, JPEG, GIF and WebP by their bytes, never by a
// name or a client's content type, so an upload can't become script or SVG.
func sniffAvatar(raw []byte) (ext, mime string, ok bool) {
	switch {
	case bytes.HasPrefix(raw, []byte("\x89PNG\r\n\x1a\n")):
		return "png", "image/png", true
	case bytes.HasPrefix(raw, []byte{0xff, 0xd8, 0xff}):
		return "jpg", "image/jpeg", true
	case bytes.HasPrefix(raw, []byte("GIF87a")), bytes.HasPrefix(raw, []byte("GIF89a")):
		return "gif", "image/gif", true
	case len(raw) > 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP":
		return "webp", "image/webp", true
	}
	return "", "", false
}

var avatarMimes = map[string]string{"png": "image/png", "jpg": "image/jpeg", "gif": "image/gif", "webp": "image/webp"}

func validEmoji(v string) bool {
	return v != "" && utf8.ValidString(v) && len(v) <= 32 && utf8.RuneCountInString(v) <= 8 && !strings.ContainsAny(v, "<>\"'&\n\r\t")
}

var errMainBot = errors.New("this is your main bot; it can be renamed and restyled but not deleted")

func registerAvatarRoutes(guard func(string, http.HandlerFunc), h *botHub) {
	guard("PUT /api/bots/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		raw, err := readLimited(w, r, maxAvatarBytes)
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "image must be at most 2 MB"})
			return
		}
		ext, _, ok := sniffAvatar(raw)
		if !ok {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "image must be PNG, JPEG, GIF or WebP"})
			return
		}
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		if b.AvatarExt != "" {
			_ = os.Remove(h.store.avatarPath(b))
		}
		b.AvatarExt, b.AvatarRev, b.AvatarKind = ext, nowMillis(), "image"
		if err := writeFileAtomic(h.store.avatarPath(b), raw); err != nil {
			b.AvatarExt, b.AvatarKind = "", "generated"
			h.store.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store the image"})
			return
		}
		b.UpdatedAt = nowMillis()
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	})
	guard("GET /api/bots/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		var path, mime string
		if b != nil && b.AvatarExt != "" {
			path, mime = h.store.avatarPath(b), avatarMimes[b.AvatarExt]
		}
		h.store.mu.Unlock()
		if path == "" {
			notFound(w, "avatar")
			return
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			notFound(w, "avatar")
			return
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_, _ = w.Write(raw)
	})
	guard("DELETE /api/bots/{id}/avatar", func(w http.ResponseWriter, r *http.Request) {
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		if b.AvatarExt != "" {
			_ = os.Remove(h.store.avatarPath(b))
		}
		b.AvatarExt, b.AvatarRev = "", 0
		if b.AvatarKind == "image" {
			b.AvatarKind = "generated"
		}
		b.UpdatedAt = nowMillis()
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	})
	// Duplicate copies a bot's profile, look and settings (not its threads).
	guard("POST /api/bots/{id}/duplicate", func(w http.ResponseWriter, r *http.Request) {
		h.store.mu.Lock()
		src := h.store.botLocked(r.PathValue("id"))
		if src == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		if len(h.store.bots) >= maxBots {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "a workspace holds at most " + strconv.Itoa(maxBots) + " bots"})
			return
		}
		b := h.store.createLocked(copyName(src.Name), src.Instructions, "")
		b.Title, b.Description, b.Settings = src.Title, src.Description, src.Settings
		b.AvatarKind, b.Emoji, b.AvatarShape, b.GlyphSeed = src.AvatarKind, src.Emoji, src.AvatarShape, cmp.Or(src.GlyphSeed, src.ID)
		if src.AvatarExt != "" {
			if raw, err := os.ReadFile(h.store.avatarPath(src)); err == nil {
				b.AvatarExt, b.AvatarRev = src.AvatarExt, nowMillis()
				if writeFileAtomic(h.store.avatarPath(b), raw) != nil {
					b.AvatarExt = ""
				}
			}
		}
		h.store.normalizeLocked()
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	})
}

func copyName(name string) string {
	n := name + " copy"
	if len(n) > 80 {
		n = n[:80]
	}
	return n
}

func readLimited(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(http.MaxBytesReader(w, r.Body, limit))
	return buf.Bytes(), err
}
