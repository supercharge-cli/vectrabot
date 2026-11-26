package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClipboardReadsTheDesktopSelection(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n[ \"$DISPLAY\" = :7 ] || exit 3\n[ \"$*\" = \"-o -selection clipboard\" ] || exit 4\nprintf 'copied on desktop'\n"
	if err := os.WriteFile(filepath.Join(bin, "xclip"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("VECTRABOT_DISPLAY", ":7")
	rec := httptest.NewRecorder()
	serveClipboard(rec, httptest.NewRequest("GET", "/v1/computer/clipboard", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"text":"copied on desktop"`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	// Sending text: both selections are written from the request body.
	got := filepath.Join(bin, "got")
	_ = os.WriteFile(filepath.Join(bin, "xclip"), []byte("#!/bin/sh\necho \"$* $(cat)\" >> "+got+"\n"), 0o755)
	rec = httptest.NewRecorder()
	serveClipboard(rec, httptest.NewRequest("POST", "/v1/computer/clipboard", strings.NewReader(`{"text":"from the PC"}`)))
	written, _ := os.ReadFile(got)
	if rec.Code != 200 || string(written) != "-i -selection clipboard from the PC\n-i -selection primary from the PC\n" {
		t.Fatalf("got %d %s / %q", rec.Code, rec.Body.String(), written)
	}
	rec = httptest.NewRecorder()
	serveClipboard(rec, httptest.NewRequest("POST", "/v1/computer/clipboard", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Fatalf("missing text: %d", rec.Code)
	}
	// Nothing copied yet: xclip fails, the host reports an empty clipboard.
	_ = os.WriteFile(filepath.Join(bin, "xclip"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	rec = httptest.NewRecorder()
	serveClipboard(rec, httptest.NewRequest("GET", "/v1/computer/clipboard", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"text":""`) {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
