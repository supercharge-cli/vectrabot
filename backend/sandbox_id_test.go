package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxBriefUsesTheRealId(t *testing.T) {
	t.Setenv("E2B_SANDBOX_ID", "sbx-test")
	t.Setenv("E2B_TEMPLATE_ID", "vectrabot-workspace")
	t.Setenv("E2B_DOMAIN", "e2b.app")
	text := sandboxBrief("bot_test")
	for _, want := range []string{"sbx-test", "vectrabot-workspace", "https://3000-sbx-test.e2b.app", "desktop_observe"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestSandboxBriefTypesOnTheBotDisplay(t *testing.T) {
	t.Setenv("E2B_SANDBOX_ID", "sbx-test")
	t.Setenv("VECTRABOT_DESKTOP_START", "")
	t.Setenv("VECTRABOT_DISPLAY", ":21")
	text := sandboxBrief("bot_test")
	if !strings.Contains(text, "DISPLAY=:") || strings.Contains(text, "DISPLAY=:99") {
		t.Fatalf("brief does not use a bot display: %s", text)
	}
	if !strings.Contains(text, "You have your own desktop") {
		t.Fatalf("brief does not tell the bot it has its own desktop: %s", text)
	}
}

func TestSandboxBriefLetsTheBotSolveAReadableCaptcha(t *testing.T) {
	t.Setenv("E2B_SANDBOX_ID", "sbx-test")
	text := sandboxBrief("bot_test")
	for _, want := range []string{
		"ordinary work",
		"A banned address",
		"A behaviour flag",
		"A region wall",
		"Never loop",
		"browser_handoff",
		"reCAPTCHA",
		"Never use a CAPTCHA-solving service",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	// The refusal rules must land after the desktop rules so they win over them.
	if strings.Index(text, "four different reasons") < strings.Index(text, "desktop_observe") {
		t.Fatal("refusal rules must follow the desktop rules")
	}
}

func TestPublicizeLinks(t *testing.T) {
	t.Setenv("E2B_SANDBOX_ID", "id77m6epjllieap5elze5")
	t.Setenv("E2B_DOMAIN", "e2b.app")
	got := publicizeLinks("open http://localhost:8080/app and 127.0.0.1:3000")
	if !strings.Contains(got, "https://8080-id77m6epjllieap5elze5.e2b.app/app") || !strings.Contains(got, "https://3000-id77m6epjllieap5elze5.e2b.app") {
		t.Fatal(got)
	}
}

func TestWorkspaceFolderList(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/files?path=src", nil)
	serveReadFile(root, rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "main.txt") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	serveMakeDir(root, rec, httptest.NewRequest(http.MethodPost, "/v1/folders?path=notes", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "notes")); err != nil {
		t.Fatal(err)
	}
}
