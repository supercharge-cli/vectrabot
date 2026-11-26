package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestAuthPathAndEngine(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "supercharge")
	script := "#!/bin/sh\nprintf 'ran:%s' \"$3\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health %d", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/files?path=note.txt", strings.NewReader("hi"))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("missing auth %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v1/files?path=../note.txt", strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("escape %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/turns", strings.NewReader(`{"engine":"claude","prompt":"hi"}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("engine %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/v1/turns", strings.NewReader(`{"engine":"supercharge","prompt":"ping"}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("turn %d", res.StatusCode)
	}
}

func TestAgentsAndSettings(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/agents", strings.NewReader(`{"id":"github","name":"GitHub","engine":"claude"}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("foreign engine %d", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v1/agents", strings.NewReader(`{"id":"github","name":"GitHub","instructions":"Repos only."}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("agent %d", res.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/v1/settings", strings.NewReader(`{"previewPort":5173}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("settings %d", res.StatusCode)
	}
}

func TestTerminalEcho(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell")
	script := "#!/bin/sh\nwhile IFS= read -r line; do printf '%s\\n' \"$line\"; done\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_SHELL", shell)
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http", "ws", 1)+"/v1/terminal", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if out := readTerminal(t, ctx, conn, "hello"); !strings.Contains(out, "hello") {
		t.Fatalf("echo %q", out)
	}
}

// readTerminal collects PTY output (binary frames) until want appears.
func readTerminal(t *testing.T, ctx context.Context, conn *websocket.Conn, want string) string {
	t.Helper()
	var out strings.Builder
	for !strings.Contains(out.String(), want) {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (have %q)", err, out.String())
		}
		if kind == websocket.MessageBinary {
			out.Write(data)
		}
	}
	return out.String()
}

func TestTerminalResumesAfterDisconnect(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "fake-shell")
	script := "#!/bin/sh\nwhile IFS= read -r line; do printf 'got:%s\\n' \"$line\"; done\n"
	if err := os.WriteFile(shell, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_SHELL", shell)
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/terminal?token=secret&session=resume-test-0123456789"
	dial := func() (*websocket.Conn, map[string]any) {
		conn, _, err := websocket.Dial(ctx, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		kind, data, err := conn.Read(ctx)
		var hello map[string]any
		if err != nil || kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil {
			t.Fatalf("no attach message: %v %q", err, data)
		}
		return conn, hello
	}
	first, hello := dial()
	if hello["resumed"] != false {
		t.Fatalf("first attach %v", hello)
	}
	_ = first.Write(ctx, websocket.MessageText, []byte("one\n"))
	readTerminal(t, ctx, first, "got:one")
	first.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)

	second, hello := dial()
	defer second.Close(websocket.StatusNormalClosure, "")
	if hello["resumed"] != true {
		t.Fatalf("second attach %v", hello)
	}
	readTerminal(t, ctx, second, "got:one") // replayed scrollback
	_ = second.Write(ctx, websocket.MessageText, []byte("two\n"))
	readTerminal(t, ctx, second, "got:two")

	if _, _, err := websocket.Dial(ctx, strings.Replace(url, "resume-test-0123456789", "bad", 1), nil); err == nil {
		t.Fatal("short session id accepted")
	}
}

func TestTurnTimeout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "supercharge")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("VECTRABOT_TURN_TIMEOUT", "200ms")
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/turns", strings.NewReader(`{"engine":"supercharge","prompt":"hang"}`))
	req.Header.Set("Authorization", "Bearer secret")
	started := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("timeout status %d", res.StatusCode)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("turn was not cut off: %s", time.Since(started))
	}
}
