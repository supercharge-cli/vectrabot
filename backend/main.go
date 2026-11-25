// Sandbox host is the process inside an E2B workspace.
// It has no web page. The PC and Android apps call it directly
// with the session our cloud minted.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("VECTRABOT_HOST_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8788"
	}
	token, err := loadSessionToken()
	if err != nil {
		log.Fatal(err)
	}
	root := workspaceRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	log.Printf("sandbox-host listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, newServer(token, root)))
}

func newServer(token, root string) http.Handler {
	state := openState(root)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "sandbox-host"})
	})
	mux.HandleFunc("GET /v1/files", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveReadFile(root, w, r)
	})
	mux.HandleFunc("PUT /v1/files", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveWriteFile(root, w, r)
	})
	mux.HandleFunc("GET /v1/terminal", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveTerminal(root, w, r)
	})
	mux.HandleFunc("POST /v1/turns", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveTurn(root, w, r)
	})
	mux.HandleFunc("GET /v1/preview", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		servePreview(w, r)
	})
	mux.HandleFunc("/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveAgents(state, w, r)
	})
	mux.HandleFunc("/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveSettings(state, w, r)
	})
	mux.HandleFunc("GET /v1/screen", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
			return
		}
		serveScreen(w, r)
	})
	return rejectHTML(mux)
}
