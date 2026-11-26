package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// The desktop's VNC server does not forward copies made on the desktop, so the
// host reads the X clipboard itself for the person viewing the workspace.
const clipboardLimit = 64 << 10

func desktopXEnv() []string {
	display := strings.TrimSpace(os.Getenv("VECTRABOT_DISPLAY"))
	if display == "" {
		display = ":99"
	}
	xauth := strings.TrimSpace(os.Getenv("VECTRABOT_XAUTHORITY"))
	if xauth == "" {
		xauth = "/tmp/supercharge-browser/Xauthority"
	}
	return append(childEnv(nil), "DISPLAY="+display, "XAUTHORITY="+xauth)
}

func readDesktopClipboard(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "xclip", "-o", "-selection", "clipboard")
	cmd.Env = desktopXEnv()
	out, err := cmd.Output()
	if err != nil {
		// xclip exits non-zero when nothing has been copied yet.
		if _, ok := err.(*exec.ExitError); ok {
			return "", nil
		}
		return "", err
	}
	if len(out) > clipboardLimit {
		out = out[:clipboardLimit]
	}
	text := strings.ToValidUTF8(string(out), "")
	if !utf8.ValidString(text) {
		return "", nil
	}
	return text, nil
}

// writeDesktopClipboard puts text on the desktop's clipboard and primary
// selection; xclip keeps serving it in the background until something else
// is copied.
func writeDesktopClipboard(ctx context.Context, text string) error {
	for _, selection := range []string{"clipboard", "primary"} {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		cmd := exec.CommandContext(ctx, "xclip", "-i", "-selection", selection)
		cmd.Env = desktopXEnv()
		cmd.Stdin = strings.NewReader(text)
		err := cmd.Run()
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func serveClipboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodPost {
		var body struct {
			Text *string `json:"text"`
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, clipboardLimit*2+1024))
		if json.Unmarshal(raw, &body) != nil || body.Text == nil || len(*body.Text) > clipboardLimit || !utf8.ValidString(*body.Text) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "send {\"text\"} up to 64 KB"})
			return
		}
		if err := writeDesktopClipboard(r.Context(), *body.Text); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "desktop clipboard unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		return
	}
	text, err := readDesktopClipboard(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "desktop clipboard unavailable"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
}
