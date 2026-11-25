package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func workspaceRoot() string {
	if root := strings.TrimSpace(os.Getenv("VECTRABOT_WORKSPACE")); root != "" {
		return root
	}
	return "/home/user/workspace"
}

func resolveWorkspacePath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, "\x00") || filepath.IsAbs(name) {
		return "", errors.New("path escapes workspace")
	}
	full := filepath.Join(root, name)
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes workspace")
	}
	return full, nil
}

func serveReadFile(root string, w http.ResponseWriter, r *http.Request) {
	full, err := resolveWorkspacePath(root, r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func serveWriteFile(root string, w http.ResponseWriter, r *http.Request) {
	full, err := resolveWorkspacePath(root, r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable body"})
		return
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "workspace unavailable"})
		return
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "bytes": len(data)})
}
