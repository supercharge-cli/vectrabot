package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// turnTimeout bounds one Supercharge turn so a stuck CLI cannot hold the
// request (and the sandbox) open forever.
func turnTimeout() time.Duration {
	if raw := os.Getenv("VECTRABOT_TURN_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

type turnRequest struct {
	Engine string `json:"engine"`
	Prompt string `json:"prompt"`
}

func serveTurn(root string, w http.ResponseWriter, r *http.Request) {
	var body turnRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid turn"})
		return
	}
	if body.Engine != "supercharge" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only the supercharge engine is allowed"})
		return
	}
	if stringsTrim(body.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), turnTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, "supercharge", "agent", "--prompt", body.Prompt)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VECTRABOT_ENGINE=supercharge")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"error":  "supercharge turn timed out",
			"detail": trimOutput(out),
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error":  "supercharge did not run",
			"detail": trimOutput(out),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"engine": "supercharge", "output": trimOutput(out)})
}

func stringsTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\n' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func trimOutput(out []byte) string {
	const max = 4000
	if len(out) > max {
		out = out[:max]
	}
	return string(out)
}
