package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// toolTokenPath is a file only processes inside the sandbox can read. Chief
// sends it when creating another bot. It is not the workspace session token.
func toolTokenPath() string {
	if p := strings.TrimSpace(os.Getenv("VECTRABOT_TOOL_TOKEN_FILE")); p != "" {
		return p
	}
	dir := "/home/user/.vectrabot"
	if s := strings.TrimSpace(os.Getenv("VECTRABOT_SESSION_FILE")); s != "" {
		dir = filepath.Dir(s)
	}
	return filepath.Join(dir, "bot-tool-token")
}

func ensureToolToken() (string, error) {
	path := toolTokenPath()
	if raw, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(raw))
		if token != "" {
			return token, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

func (h *botHub) serveToolBots(w http.ResponseWriter, r *http.Request) {
	token, err := ensureToolToken()
	if err != nil || !tokenMatches(r.Header.Get("X-Vectrabot-Tool"), token) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method == http.MethodGet {
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		var bots []map[string]string
		for _, b := range h.store.bots {
			bots = append(bots, map[string]string{"id": b.ID, "name": b.Name, "instructions": b.Instructions})
		}
		writeJSON(w, http.StatusOK, map[string]any{"bots": bots})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	var body struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > 80 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1–80 characters"})
		return
	}
	h.store.mu.Lock()
	bot := h.store.createLocked(name, body.Instructions, "")
	bot.Settings.PermissionMode = "always-approve"
	err = h.store.saveLocked()
	id, botName := bot.ID, bot.Name
	h.store.mu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save the bot"})
		return
	}
	h.events.publish("bots", map[string]any{"botId": id})
	writeJSON(w, http.StatusOK, map[string]any{"bot": map[string]string{"id": id, "name": botName}})
}

// sessionPermission is the mode a thread starts in. Ask was the old default;
// the workspace now runs with every action allowed unless a bot chose auto.
func sessionPermission(mode string) string {
	if mode == "" || mode == "ask" {
		return "always-approve"
	}
	return mode
}
