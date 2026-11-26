// Sandbox host is the process inside an E2B workspace.
// It has no web page. The PC and Android apps call it directly
// with the session our cloud minted.
package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// defaultAddr is the app port the cloud proxies to. Port 8788 stays free for the
// template's wake and Telegram doorbell.
const defaultAddr = "0.0.0.0:8790"

func main() {
	addr := os.Getenv("VECTRABOT_HOST_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	tokens, err := loadTokenStore()
	if err != nil {
		log.Fatal(err)
	}
	root := workspaceRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		log.Fatal(err)
	}
	// The template's wake service sends SIGUSR1 to whatever owns the app port after
	// a resume. Go would exit on it by default.
	signal.Ignore(syscall.SIGUSR1)
	if pidfile := strings.TrimSpace(os.Getenv("VECTRABOT_PIDFILE")); pidfile != "" {
		// Recording our pid where the template expects its app daemon keeps the wake
		// service from starting a second process on this port.
		if err := os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
			log.Printf("pidfile: %v", err)
		}
	}
	log.Printf("sandbox-host listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, newServer(tokens, root)))
}

func newServer(tokens *tokenStore, root string) http.Handler {
	state := openState(root)
	desktop := newDesktopBridge()
	mux := http.NewServeMux()
	guard := func(pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if !authorized(r, tokens) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})
				return
			}
			handler(w, r)
		})
	}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "ok", "service": "sandbox-host"})
	})
	guard("GET /v1/files", func(w http.ResponseWriter, r *http.Request) { serveReadFile(root, w, r) })
	guard("PUT /v1/files", func(w http.ResponseWriter, r *http.Request) { serveWriteFile(root, w, r) })
	guard("GET /v1/terminal", func(w http.ResponseWriter, r *http.Request) { serveTerminal(root, w, r) })
	guard("POST /v1/turns", func(w http.ResponseWriter, r *http.Request) { serveTurn(root, state, w, r) })
	guard("GET /v1/preview", servePreview)
	guard("/v1/agents", func(w http.ResponseWriter, r *http.Request) { serveAgents(state, w, r) })
	guard("/v1/settings", func(w http.ResponseWriter, r *http.Request) { serveSettings(state, w, r) })
	guard("GET /v1/screen", serveScreen)
	guard("GET /v1/computer/status", desktop.serveStatus)
	guard("POST /v1/computer/handoff", desktop.serveHandoff)
	guard("GET /v1/computer/vnc-ticket", desktop.serveTicket)
	// The viewer is a browser WebSocket: it proves itself with a one-use ticket
	// minted by an authorized vnc-ticket call instead of a header.
	mux.HandleFunc("GET /v1/computer/browser/vnc", desktop.serveVNC)
	return rejectHTML(mux)
}
