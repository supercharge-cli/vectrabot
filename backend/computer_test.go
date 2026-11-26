package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func testTokens() *tokenStore { return newTokenStore([]string{"secret"}, "") }

// fakeDesktop mimics the template's desktop runtime handoff contract.
type fakeDesktop struct {
	mu        sync.Mutex
	handoffID string
	active    bool
	observer  []string
}

func (f *fakeDesktop) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.observer = append(f.observer, r.Header.Get("x-vectra-browser-observer"))
		state := func() map[string]any {
			return map[string]any{"ok": true, "desktop": true, "handoff": f.active, "handoffId": f.handoffID, "expiresAt": time.Now().Add(time.Minute).UnixMilli(), "handoffProtocol": 2, "botId": "main"}
		}
		switch r.URL.Path {
		case "/status":
			writeJSON(w, 200, state())
		case "/handoff/start", "/handoff/stop":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			id, _ := body["handoffId"].(string)
			if r.URL.Path == "/handoff/start" {
				if _, ok := body["minutes"].(float64); !ok {
					writeJSON(w, 409, map[string]any{"ok": false, "error": "minutes must be an integer"})
					return
				}
				f.active, f.handoffID = true, id
			} else {
				if id != f.handoffID {
					writeJSON(w, 409, map[string]any{"ok": false, "error": "Stale handoff"})
					return
				}
				f.active = false
			}
			writeJSON(w, 200, state())
		default:
			http.NotFound(w, r)
		}
	})
}

func authed(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func TestComputerHandoffTicketAndVNCRelay(t *testing.T) {
	desktop := &fakeDesktop{}
	runtime := httptest.NewServer(desktop.handler())
	defer runtime.Close()
	vnc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer vnc.Close()
	go func() {
		for {
			conn, err := vnc.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = conn.Write([]byte("RFB 003.008\n"))
				buf := make([]byte, 64)
				n, _ := conn.Read(buf)
				_, _ = conn.Write(append([]byte("echo:"), buf[:n]...))
				time.Sleep(time.Second)
			}()
		}
	}()
	t.Setenv("VECTRABOT_DESKTOP_RUNTIME", runtime.URL)
	t.Setenv("VECTRABOT_VNC_ADDR", vnc.Addr().String())
	root := t.TempDir()
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()

	code, status := authed(t, "GET", srv.URL+"/v1/computer/status", "")
	if code != 200 || status["enabled"] != true || status["handoff"] != false {
		t.Fatalf("status %d %v", code, status)
	}
	code, _ = authed(t, "GET", srv.URL+"/v1/computer/vnc-ticket?handoffId=aaaaaaaaaaaaaaaa", "")
	if code != http.StatusConflict {
		t.Fatalf("ticket before takeover %d", code)
	}
	id := "abcdefabcdefabcdef12"
	code, started := authed(t, "POST", srv.URL+"/v1/computer/handoff", `{"action":"start","minutes":10,"handoffId":"`+id+`","botId":"main"}`)
	if code != 200 || started["ok"] != true || started["handoff"] != true {
		t.Fatalf("start %d %v", code, started)
	}
	code, ticket := authed(t, "GET", srv.URL+"/v1/computer/vnc-ticket?handoffId="+id+"&botId=main", "")
	if code != 200 || ticket["handoffId"] != id || ticket["ticket"] == "" {
		t.Fatalf("ticket %d %v", code, ticket)
	}
	ws := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/computer/browser/vnc?ticket="
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, res, err := websocket.Dial(ctx, ws+"forged", nil); err == nil || res.StatusCode != 401 {
		t.Fatalf("forged ticket accepted")
	}
	conn, _, err := websocket.Dial(ctx, ws+ticket["ticket"].(string), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, hello, err := conn.Read(ctx)
	if err != nil || !strings.HasPrefix(string(hello), "RFB 003.008") {
		t.Fatalf("rfb greeting %q %v", hello, err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("client-init")); err != nil {
		t.Fatal(err)
	}
	_, echoed, err := conn.Read(ctx)
	if err != nil || string(echoed) != "echo:client-init" {
		t.Fatalf("relay %q %v", echoed, err)
	}
	conn.CloseNow()
	if _, res, err := websocket.Dial(ctx, ws+ticket["ticket"].(string), nil); err == nil || res.StatusCode != 401 {
		t.Fatalf("ticket reused")
	}
	code, stopped := authed(t, "POST", srv.URL+"/v1/computer/handoff", `{"action":"stop","handoffId":"`+id+`","botId":"main"}`)
	if code != 200 || stopped["handoff"] != false {
		t.Fatalf("stop %d %v", code, stopped)
	}
	for _, seen := range desktop.observer {
		if seen != "1" {
			t.Fatalf("runtime call without observer header")
		}
	}
	res, _ := http.Get(srv.URL + "/v1/computer/status")
	if res.StatusCode != 401 {
		t.Fatalf("status without session %d", res.StatusCode)
	}
}

func TestSessionFileAddsDevicesWithoutRestart(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sessions")
	tokens := newTokenStore(nil, file)
	srv := httptest.NewServer(newServer(tokens, filepath.Join(dir, "ws")))
	defer srv.Close()
	phone := strings.Repeat("p", 43)
	pc := strings.Repeat("c", 43)
	check := func(token string, want int) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/v1/settings", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("token %c: %d want %d", token[0], res.StatusCode, want)
		}
	}
	check(phone, 401)
	if err := os.WriteFile(file, []byte(phone+" phone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(phone, 200)
	if err := os.WriteFile(file, []byte(phone+" phone\n"+pc+" pc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check(phone, 200)
	check(pc, 200)
	check("short", 401)
}

func TestTurnUsesSingleShotCLIAndSurfacesErrors(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "supercharge")
	argsFile := filepath.Join(dir, "args")
	envFile := filepath.Join(dir, "provider.env")
	if err := os.WriteFile(envFile, []byte("export SUPERAGENT_PROVIDER=\"openai\"\n# comment\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\nif [ \"$SUPERAGENT_PROVIDER\" = openai ]; then printf '{\"type\":\"result\",\"result\":\"pong\"}\\n'; else printf '{\"type\":\"error\",\"message\":\"No provider credentials are configured.\"}\\n'; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_SUPERCHARGE_BIN", bin)
	t.Setenv("VECTRABOT_PROVIDER_ENV", filepath.Join(dir, "missing.env"))
	root := filepath.Join(dir, "ws")
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()
	code, out := authed(t, "POST", srv.URL+"/v1/turns", `{"prompt":"ping"}`)
	if code != http.StatusBadGateway || !strings.Contains(out["error"].(string), "No provider credentials") {
		t.Fatalf("no provider %d %v", code, out)
	}
	t.Setenv("VECTRABOT_PROVIDER_ENV", envFile)
	authed(t, "PUT", srv.URL+"/v1/agents", `{"id":"reviewer","name":"Reviewer","instructions":"Be brief."}`)
	code, out = authed(t, "POST", srv.URL+"/v1/turns", `{"engine":"supercharge","prompt":"ping","agentId":"reviewer"}`)
	if code != 200 || out["output"] != "pong" {
		t.Fatalf("turn %d %v", code, out)
	}
	raw, _ := os.ReadFile(argsFile)
	want := "-p\nping\n--output-format\njson\n--always-approve\n--cwd\n" + root + "\n--rules\nBe brief.\n"
	if string(raw) != want {
		t.Fatalf("args %q", raw)
	}
	code, _ = authed(t, "POST", srv.URL+"/v1/turns", `{"prompt":"ping","agentId":"nobody"}`)
	if code != 404 {
		t.Fatalf("unknown agent %d", code)
	}
}

func TestStatusReportsOpenTerminalsAsWork(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "sh")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec cat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_SHELL", shell)
	srv := httptest.NewServer(newServer(testTokens(), filepath.Join(dir, "ws")))
	defer srv.Close()
	busy := func() (bool, int) {
		_, out := authed(t, "GET", srv.URL+"/v1/status", "")
		tasks, _ := out["backgroundTasks"].([]any)
		return out["turn"].(map[string]any)["busy"].(bool), len(tasks)
	}
	if turn, tasks := busy(); turn || tasks != 0 {
		t.Fatalf("idle host reports work: %v %d", turn, tasks)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, strings.Replace(srv.URL, "http", "ws", 1)+"/v1/terminal?token=secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, tasks := busy(); tasks == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("open terminal not reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn.Close(websocket.StatusNormalClosure, "")
	deadline = time.Now().Add(2 * time.Second)
	for {
		if _, tasks := busy(); tasks == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed terminal still reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBrowsersMayCallWithASessionOnly(t *testing.T) {
	srv := httptest.NewServer(newServer(testTokens(), t.TempDir()))
	defer srv.Close()
	for _, path := range []string{"/v1/turns", "/api/bots"} {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+path, nil)
		req.Header.Set("Origin", "http://127.0.0.1:4173")
		req.Header.Set("Access-Control-Request-Method", "POST")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != "*" || res.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("preflight %s %d %v", path, res.StatusCode, res.Header)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/settings", nil)
	req.Header.Set("Origin", "http://127.0.0.1:4173")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("cross-origin call without a session %d", res.StatusCode)
	}
}
