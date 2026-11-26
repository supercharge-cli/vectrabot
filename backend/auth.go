package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// tokenStore holds every credential the host accepts:
//   - VECTRABOT_SESSION, a fixed session (tests, manual runs)
//   - VECTRABOT_CLOUD_TOKEN, the control plane's own credential for server-side proxying
//   - each line of VECTRABOT_SESSION_FILE, the client sessions the cloud minted on attach
//
// The session file is re-read when it changes, so attaching a second device adds a
// session without restarting the host or signing the first device out.
type tokenStore struct {
	mu       sync.Mutex
	fixed    []string
	file     string
	modTime  time.Time
	size     int64
	fromFile []string
}

func newTokenStore(fixed []string, file string) *tokenStore {
	store := &tokenStore{file: file}
	for _, token := range fixed {
		if token = strings.TrimSpace(token); token != "" {
			store.fixed = append(store.fixed, token)
		}
	}
	return store
}

func loadTokenStore() (*tokenStore, error) {
	file := strings.TrimSpace(os.Getenv("VECTRABOT_SESSION_FILE"))
	if file == "" {
		file = "/home/user/.vectrabot/sessions"
	}
	store := newTokenStore([]string{os.Getenv("VECTRABOT_SESSION"), os.Getenv("VECTRABOT_CLOUD_TOKEN")}, file)
	if len(store.fixed) == 0 && len(store.current()) == 0 {
		return nil, errors.New("VECTRABOT_SESSION or a session file is required")
	}
	return store, nil
}

func (s *tokenStore) current() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file != "" {
		if info, err := os.Stat(s.file); err == nil {
			if !info.ModTime().Equal(s.modTime) || info.Size() != s.size {
				if raw, err := os.ReadFile(s.file); err == nil {
					s.fromFile = s.fromFile[:0]
					for _, line := range strings.Split(string(raw), "\n") {
						// Each line is "<token>" or "<token> <label>"; only the token counts.
						if fields := strings.Fields(line); len(fields) > 0 && len(fields[0]) >= 32 {
							s.fromFile = append(s.fromFile, fields[0])
						}
					}
					s.modTime, s.size = info.ModTime(), info.Size()
				}
			}
		} else {
			s.fromFile, s.modTime, s.size = nil, time.Time{}, 0
		}
	}
	out := make([]string, 0, len(s.fixed)+len(s.fromFile))
	out = append(out, s.fixed...)
	return append(out, s.fromFile...)
}

func (s *tokenStore) valid(got string) bool {
	got = strings.TrimSpace(got)
	if got == "" {
		return false
	}
	ok := false
	for _, token := range s.current() {
		if tokenMatches(got, token) {
			ok = true
		}
	}
	return ok
}

func tokenMatches(got, token string) bool {
	got = strings.TrimSpace(got)
	if got == "" || len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func authorized(r *http.Request, tokens *tokenStore) bool {
	header := r.Header.Get("Authorization")
	got, ok := strings.CutPrefix(header, "Bearer ")
	if ok && tokens.valid(got) {
		return true
	}
	// Browsers cannot set an Authorization header on a WebSocket.
	if r.URL.Path == "/v1/terminal" {
		return tokens.valid(r.URL.Query().Get("token"))
	}
	return false
}

func rejectHTML(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.Header.Get("Accept"), "text/html") && r.URL.Path == "/" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no web frontend"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
