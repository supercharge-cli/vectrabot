package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
)

func loadSessionToken() (string, error) {
	if token := strings.TrimSpace(os.Getenv("VECTRABOT_SESSION")); token != "" {
		return token, nil
	}
	raw, err := os.ReadFile("/home/user/.vectrabot-session")
	if err != nil {
		return "", errors.New("VECTRABOT_SESSION is required")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("VECTRABOT_SESSION is required")
	}
	return token, nil
}

func tokenMatches(got, token string) bool {
	got = strings.TrimSpace(got)
	if got == "" || len(got) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func authorized(r *http.Request, token string) bool {
	header := r.Header.Get("Authorization")
	got, ok := strings.CutPrefix(header, "Bearer ")
	if ok && tokenMatches(got, token) {
		return true
	}
	// Browsers cannot set an Authorization header on a WebSocket.
	if r.URL.Path == "/v1/terminal" {
		return tokenMatches(r.URL.Query().Get("token"), token)
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
