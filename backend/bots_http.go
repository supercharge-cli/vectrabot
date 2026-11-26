package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The bot API. Shapes follow the companion app's client: GET /api/bots
// returns {bots}, each bot carries its threads as tasks, and live changes
// arrive on GET /api/events.

type botView struct {
	botRecord
	// AvatarURL is the stored image on this host, null when there is none.
	AvatarURL *string `json:"avatarUrl"`
	// Glyph is the 15-bit pattern of the generated face (see glyphOf).
	Glyph   int          `json:"glyph"`
	Busy    bool         `json:"busy"`
	Preview string       `json:"preview"`
	LastAt  int64        `json:"lastAt"`
	Tasks   []threadView `json:"tasks"`
}

type threadView struct {
	threadRecord
	Busy    bool   `json:"busy"`
	Preview string `json:"preview"`
}

func previewOf(list []*message) (string, int64) {
	for i := len(list) - 1; i >= 0; i-- {
		m := list[i]
		if (m.Kind == "text" || m.Kind == "notice") && strings.TrimSpace(m.Text) != "" {
			prefix := ""
			if m.Role == "user" {
				prefix = "You: "
			}
			return prefix + shortTitle(m.Text), m.At
		}
	}
	return "", 0
}

func (h *botHub) viewLocked(b *botRecord) botView {
	v := botView{botRecord: *b, Tasks: []threadView{}, AvatarURL: avatarURL(b), Glyph: glyphOf(cmp.Or(b.GlyphSeed, b.ID))}
	for _, t := range h.store.threadsOfLocked(b.ID) {
		tv := threadView{threadRecord: *t, Busy: h.turning[t.ID]}
		tv.Preview, _ = previewOf(h.store.messagesLocked(t.ID))
		if tv.Busy {
			v.Busy = true
		}
		v.Tasks = append(v.Tasks, tv)
	}
	if v.ThreadID != "" {
		v.Preview, v.LastAt = previewOf(h.store.messagesLocked(v.ThreadID))
	}
	for _, t := range v.Tasks {
		if t.UpdatedAt > v.LastAt {
			v.LastAt = t.UpdatedAt
			if t.Preview != "" {
				v.Preview = t.Preview
			}
		}
	}
	return v
}

func (h *botHub) publishBot(botID string) {
	h.store.mu.Lock()
	b := h.store.botLocked(botID)
	if b == nil {
		h.store.mu.Unlock()
		return
	}
	v := h.viewLocked(b)
	h.store.mu.Unlock()
	h.events.publish("bot", map[string]any{"bot": v})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return false
	}
	return true
}

func notFound(w http.ResponseWriter, what string) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": what + " not found"})
}

type botInput struct {
	Name           *string `json:"name"`
	Title          *string `json:"title"`
	Description    *string `json:"description"`
	Instructions   *string `json:"instructions"`
	Color          *string `json:"color"`
	Model          *string `json:"model"`
	PermissionMode *string `json:"permissionMode"`
	Mode           *string `json:"mode"`
	AvatarKind     *string `json:"avatarKind"`
	Emoji          *string `json:"emoji"`
	AvatarShape    *string `json:"avatarShape"`
	Pinned         *bool   `json:"pinned"`
	GlyphSeed      *string `json:"glyphSeed"`
}

var permissionModes = map[string]bool{"ask": true, "auto": true, "always-approve": true}
var sessionModes = map[string]bool{"default": true, "plan": true, "ask": true}

func applyBotInput(b *botRecord, in botInput) error {
	set := func(dst *string, src *string, max int) {
		if src != nil {
			v := strings.TrimSpace(*src)
			if len(v) > max {
				v = v[:max]
			}
			*dst = v
		}
	}
	set(&b.Name, in.Name, 80)
	set(&b.Title, in.Title, 120)
	set(&b.Description, in.Description, 500)
	set(&b.Instructions, in.Instructions, 20000)
	if in.Color != nil && strings.TrimSpace(*in.Color) != "" {
		c := strings.ToLower(strings.TrimSpace(*in.Color))
		if !hexColor.MatchString(c) {
			return errors.New("color must be a #rrggbb hex colour")
		}
		b.Color = c
	}
	if in.Emoji != nil {
		e := strings.TrimSpace(*in.Emoji)
		if e != "" && !validEmoji(e) {
			return errors.New("emoji must be a short emoji or symbol")
		}
		b.Emoji = e
	}
	if in.AvatarShape != nil {
		if !avatarShapes[*in.AvatarShape] {
			return errors.New("avatarShape must be circle, rounded or square")
		}
		b.AvatarShape = *in.AvatarShape
	}
	if in.AvatarKind != nil {
		k := *in.AvatarKind
		switch {
		case !avatarKinds[k]:
			return errors.New("avatarKind must be generated, initial, emoji or image")
		case k == "image" && b.AvatarExt == "":
			return errors.New("upload an image first")
		case k == "emoji" && b.Emoji == "":
			return errors.New("pick an emoji first")
		}
		b.AvatarKind = k
	}
	if b.AvatarKind == "emoji" && b.Emoji == "" {
		b.AvatarKind = "generated"
	}
	if in.Pinned != nil {
		b.Pinned = *in.Pinned
	}
	if in.GlyphSeed != nil {
		if *in.GlyphSeed != "" && !glyphSeedRe.MatchString(*in.GlyphSeed) {
			return errors.New("glyphSeed must be 1-32 letters, digits, _ or -")
		}
		b.GlyphSeed = *in.GlyphSeed
	}
	set(&b.Settings.Model, in.Model, 200)
	if in.PermissionMode != nil {
		if !permissionModes[*in.PermissionMode] {
			return errors.New("permissionMode must be ask, auto or always-approve")
		}
		b.Settings.PermissionMode = *in.PermissionMode
	}
	if in.Mode != nil {
		if !sessionModes[*in.Mode] {
			return errors.New("mode must be default, plan or ask")
		}
		b.Settings.Mode = *in.Mode
	}
	if b.Name == "" {
		return errors.New("name required")
	}
	return nil
}

func registerBotRoutes(guard func(string, http.HandlerFunc), h *botHub, routines *routineStore, providers *providerControl) {
	guard("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "sandbox-host", "api": 1})
	})
	guard("GET /api/events", h.events.serve)
	registerAvatarRoutes(guard, h)
	guard("GET /api/bots", func(w http.ResponseWriter, _ *http.Request) {
		h.store.mu.Lock()
		views := h.sortedViewsLocked()
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"bots": views, "groups": []any{}, "cursor": h.events.cursor()})
	})
	guard("POST /api/bots", func(w http.ResponseWriter, r *http.Request) {
		var in botInput
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		if len(h.store.bots) >= maxBots {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "a workspace holds at most " + strconv.Itoa(maxBots) + " bots"})
			return
		}
		name := ""
		if in.Name != nil {
			name = *in.Name
		}
		b := h.store.createLocked(name, "", "")
		if err := applyBotInput(b, in); err != nil {
			h.store.bots = h.store.bots[:len(h.store.bots)-1]
			delete(h.store.threads, b.ThreadID)
			delete(h.store.messages, b.ThreadID)
			h.store.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	})
	guard("GET /api/bots/{id}", func(w http.ResponseWriter, r *http.Request) {
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	})
	updateBot := func(w http.ResponseWriter, r *http.Request) {
		var in botInput
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		next := *b
		if err := applyBotInput(&next, in); err != nil {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		profileChanged := next.Instructions != b.Instructions || next.Name != b.Name || next.Title != b.Title || next.Description != b.Description
		next.UpdatedAt = nowMillis()
		*b = next
		if profileChanged {
			// New instructions reach every thread on its next turn.
			for _, t := range h.store.threadsOfLocked(b.ID) {
				t.Primed = false
			}
		}
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v})
	}
	guard("PATCH /api/bots/{id}", updateBot)
	guard("PATCH /api/bots/{id}/profile", updateBot)
	guard("PATCH /api/bots/{id}/settings", updateBot)
	guard("DELETE /api/bots/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		h.store.mu.Lock()
		if b := h.store.botLocked(id); b != nil && b.Main {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": b.Name + ": " + errMainBot.Error()})
			return
		}
		if len(h.store.bots) <= 1 {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "keep at least one bot"})
			return
		}
		idx := -1
		for i, b := range h.store.bots {
			if b.ID == id {
				idx = i
			}
		}
		if idx < 0 {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		if gone := h.store.bots[idx]; gone.AvatarExt != "" {
			_ = os.Remove(h.store.avatarPath(gone))
		}
		h.store.bots = append(h.store.bots[:idx], h.store.bots[idx+1:]...)
		for tid, t := range h.store.threads {
			if t.BotID == id {
				delete(h.store.threads, tid)
				delete(h.store.messages, tid)
				_ = os.Remove(h.store.threadFile(tid))
			}
		}
		_ = os.RemoveAll(h.store.botFilesDir(id))
		_ = h.store.saveLocked()
		h.store.mu.Unlock()
		h.stopBot(id)
		routines.removeBot(id)
		h.events.publish("bot.deleted", map[string]any{"botId": id})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Threads ("tasks" in the companion client's words).
	guard("POST /api/bots/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title    string `json:"title"`
			ThreadID string `json:"threadId"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		var t *threadRecord
		if in.ThreadID != "" {
			t = h.store.threads[in.ThreadID]
			if t == nil || t.BotID != b.ID {
				h.store.mu.Unlock()
				notFound(w, "thread")
				return
			}
		} else {
			t = h.store.newThreadLocked(b, in.Title)
			if in.Title != "" {
				t.Renamed = true
			}
		}
		b.ThreadID = t.ID
		_ = h.store.saveLocked()
		v := h.viewLocked(b)
		h.store.mu.Unlock()
		h.events.publish("bot", map[string]any{"bot": v})
		writeJSON(w, http.StatusOK, map[string]any{"bot": v, "threadId": t.ID})
	})
	guard("PATCH /api/bots/{id}/tasks/{tid}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title    *string `json:"title"`
			Pinned   *bool   `json:"pinned"`
			Archived *bool   `json:"archived"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		t := h.store.threads[r.PathValue("tid")]
		if t == nil || t.BotID != r.PathValue("id") {
			h.store.mu.Unlock()
			notFound(w, "thread")
			return
		}
		if in.Title != nil {
			t.Title = shortTitle(*in.Title)
			t.Renamed = true
		}
		if in.Pinned != nil {
			t.Pinned = *in.Pinned
		}
		if in.Archived != nil {
			t.ArchivedAt = 0
			if *in.Archived {
				t.ArchivedAt = nowMillis()
			}
		}
		_ = h.store.saveLocked()
		h.store.mu.Unlock()
		h.publishBot(r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("DELETE /api/bots/{id}/tasks/{tid}", func(w http.ResponseWriter, r *http.Request) {
		botID, tid := r.PathValue("id"), r.PathValue("tid")
		h.store.mu.Lock()
		t := h.store.threads[tid]
		b := h.store.botLocked(botID)
		if t == nil || b == nil || t.BotID != botID {
			h.store.mu.Unlock()
			notFound(w, "thread")
			return
		}
		busy := h.turning[tid]
		if busy {
			h.store.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]string{"error": "stop the running turn first"})
			return
		}
		delete(h.store.threads, tid)
		delete(h.store.messages, tid)
		delete(h.live, tid)
		_ = os.Remove(h.store.threadFile(tid))
		if b.ThreadID == tid {
			b.ThreadID = ""
			if rest := h.store.threadsOfLocked(botID); len(rest) > 0 {
				b.ThreadID = rest[0].ID
			} else {
				b.ThreadID = h.store.newThreadLocked(b, "").ID
			}
		}
		_ = h.store.saveLocked()
		h.store.mu.Unlock()
		h.publishBot(botID)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("GET /api/threads/{tid}/messages", func(w http.ResponseWriter, r *http.Request) {
		tid := r.PathValue("tid")
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 500 {
			limit = 200
		}
		before := r.URL.Query().Get("before")
		h.store.mu.Lock()
		if h.store.threads[tid] == nil {
			h.store.mu.Unlock()
			notFound(w, "thread")
			return
		}
		list := h.store.messagesLocked(tid)
		end := len(list)
		if before != "" {
			for i, m := range list {
				if m.ID == before {
					end = i
				}
			}
		}
		start := end - limit
		if start < 0 {
			start = 0
		}
		out := make([]*message, 0, end-start)
		for _, m := range list[start:end] {
			out = append(out, cloneMessage(m))
		}
		state := h.sessionStateLocked(tid)
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"messages": out, "hasMore": start > 0, "session": state})
	})
	guard("GET /api/threads/{tid}/session", func(w http.ResponseWriter, r *http.Request) {
		h.store.mu.Lock()
		state := h.sessionStateLocked(r.PathValue("tid"))
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
	})
	// open starts (or resumes) the session ahead of the first message so the
	// composer can show modes, models and slash commands.
	guard("POST /api/threads/{tid}/open", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if _, _, err := h.openSession(ctx, r.PathValue("tid")); err != nil {
			writeTurnError(w, err)
			return
		}
		h.store.mu.Lock()
		state := h.sessionStateLocked(r.PathValue("tid"))
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
	})
	guard("POST /api/bots/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text     string `json:"text"`
			ThreadID string `json:"threadId"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		if b == nil {
			h.store.mu.Unlock()
			notFound(w, "bot")
			return
		}
		tid := in.ThreadID
		if tid == "" {
			tid = b.ThreadID
		}
		if t := h.store.threads[tid]; t == nil || t.BotID != b.ID {
			h.store.mu.Unlock()
			notFound(w, "thread")
			return
		}
		h.store.mu.Unlock()
		queued, err := h.send(r.Context(), tid, in.Text)
		if err != nil {
			writeTurnError(w, err)
			return
		}
		h.publishBot(r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]any{"threadId": tid, "queued": queued})
	})
	guard("POST /api/bots/{id}/interrupt", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ThreadID string `json:"threadId"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		h.store.mu.Lock()
		b := h.store.botLocked(r.PathValue("id"))
		tid := in.ThreadID
		if b != nil && tid == "" {
			tid = b.ThreadID
		}
		h.store.mu.Unlock()
		if b == nil {
			notFound(w, "bot")
			return
		}
		if err := h.interrupt(tid); err != nil {
			writeTurnError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("POST /api/threads/{tid}/respond", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RequestID string          `json:"requestId"`
			Answer    json.RawMessage `json:"answer"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if len(in.Answer) == 0 || string(in.Answer) == "null" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "answer required"})
			return
		}
		if err := h.respond(r.PathValue("tid"), in.RequestID, in.Answer); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "that request is no longer waiting"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("POST /api/threads/{tid}/mode", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ModeID string `json:"modeId"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if !sessionModes[in.ModeID] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode must be default, plan or ask"})
			return
		}
		if err := h.setMode(r.Context(), r.PathValue("tid"), in.ModeID); err != nil {
			writeTurnError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("POST /api/threads/{tid}/config", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ConfigID string `json:"configId"`
			Value    string `json:"value"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if in.ConfigID == "" || in.Value == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "configId and value required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if err := h.setOption(ctx, r.PathValue("tid"), in.ConfigID, in.Value); err != nil {
			writeTurnError(w, err)
			return
		}
		h.store.mu.Lock()
		state := h.sessionStateLocked(r.PathValue("tid"))
		h.store.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
	})

	// Per-bot reference files.
	guard("GET /api/bots/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := h.botDir(w, r)
		if !ok {
			return
		}
		entries, _ := os.ReadDir(dir)
		files := []map[string]any{}
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !e.IsDir() {
				files = append(files, map[string]any{"name": e.Name(), "size": info.Size(), "modifiedAt": info.ModTime().UnixMilli()})
			}
		}
		sort.Slice(files, func(i, j int) bool { return files[i]["name"].(string) < files[j]["name"].(string) })
		writeJSON(w, http.StatusOK, map[string]any{"files": files, "path": dir})
	})
	guard("PUT /api/bots/{id}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := h.botDir(w, r)
		name, okName := safeFileName(r.PathValue("name"))
		if !ok || !okName {
			if ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file name"})
			}
			return
		}
		_ = os.MkdirAll(dir, 0o700)
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 25<<20))
		if err != nil {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "files are limited to 25 MB"})
			return
		}
		if err := writeFileAtomic(filepath.Join(dir, name), raw); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the file"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "size": len(raw)})
	})
	guard("GET /api/bots/{id}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := h.botDir(w, r)
		name, okName := safeFileName(r.PathValue("name"))
		if !ok || !okName {
			if ok {
				notFound(w, "file")
			}
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, filepath.Join(dir, name))
	})
	guard("DELETE /api/bots/{id}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		dir, ok := h.botDir(w, r)
		name, okName := safeFileName(r.PathValue("name"))
		if !ok || !okName {
			if ok {
				notFound(w, "file")
			}
			return
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			notFound(w, "file")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Routines.
	guard("GET /api/routines", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, routines.list()) })
	guard("POST /api/routines", func(w http.ResponseWriter, r *http.Request) {
		var in routineInput
		if !decodeBody(w, r, &in) {
			return
		}
		rt, err := routines.upsert("", in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routine": rt})
	})
	guard("PATCH /api/routines/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in routineInput
		if !decodeBody(w, r, &in) {
			return
		}
		rt, err := routines.upsert(r.PathValue("id"), in)
		if errors.Is(err, errNotFound) {
			notFound(w, "routine")
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routine": rt})
	})
	guard("DELETE /api/routines/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := routines.remove(r.PathValue("id")); err != nil {
			notFound(w, "routine")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	guard("POST /api/routines/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		run, err := routines.fire(r.PathValue("id"), true)
		if errors.Is(err, errNotFound) {
			notFound(w, "routine")
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"run": run})
	})

	// Providers, through the CLI's own setup methods.
	guard("GET /api/providers", providers.serveStatus)
	guard("GET /api/models", providers.serveModels)
	guard("POST /api/providers/{action}", providers.serveAction)
	guard("POST /api/providers/{action}/{sub}", providers.serveAction)
}

func (h *botHub) botDir(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	h.store.mu.Lock()
	ok := h.store.botLocked(id) != nil
	h.store.mu.Unlock()
	if !ok {
		notFound(w, "bot")
		return "", false
	}
	return h.store.botFilesDir(id), true
}

func safeFileName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || len(name) > 200 || strings.HasPrefix(name, ".") {
		return "", false
	}
	return name, true
}

func writeTurnError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNotFound):
		notFound(w, "thread")
	case err.Error() == "message required":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}
