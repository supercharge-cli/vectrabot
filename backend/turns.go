package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
)

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
	cmd := exec.Command("supercharge", "agent", "--prompt", body.Prompt)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VECTRABOT_ENGINE=supercharge")
	out, err := cmd.CombinedOutput()
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
