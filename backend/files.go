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
	name := r.URL.Query().Get("path")
	if name == "" || name == "." {
		serveFileList(root, root, w)
		return
	}
	full, err := resolveWorkspacePath(root, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	info, err := os.Stat(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if info.IsDir() {
		serveFileList(root, full, w)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	name = filepath.Base(full)
	w.Header().Set("Content-Type", mimeForName(name))
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func mimeForName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".txt", ".md", ".json", ".log":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
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

func serveFileList(root, full string, w http.ResponseWriter) {
	entries, err := os.ReadDir(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
		return
	}
	rel, _ := filepath.Rel(root, full)
	if rel == "." {
		rel = ""
	}
	type row struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Dir  bool   `json:"dir"`
		Size int64  `json:"size"`
	}
	out := make([]row, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := entry.Name()
		if rel != "" {
			path = rel + "/" + entry.Name()
		}
		out = append(out, row{Name: entry.Name(), Path: path, Dir: entry.IsDir(), Size: info.Size()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "entries": out})
}

func serveDeleteFile(root string, w http.ResponseWriter, r *http.Request) {
	full, err := resolveWorkspacePath(root, r.URL.Query().Get("path"))
	if err != nil || full == root {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes workspace"})
		return
	}
	if err := os.RemoveAll(full); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func serveMakeDir(root string, w http.ResponseWriter, r *http.Request) {
	full, err := resolveWorkspacePath(root, r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(full, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "folder was not created"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
