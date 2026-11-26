package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type botTestEnv struct {
	t      *testing.T
	srv    *httptest.Server
	root   string
	frames chan map[string]any
}

func newBotTestEnv(t *testing.T) *botTestEnv {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VECTRABOT_FAKE_ACP", "1")
	t.Setenv("VECTRABOT_SUPERCHARGE_BIN", os.Args[0])
	t.Setenv("VECTRABOT_SUPERCHARGE_CONFIG", filepath.Join(dir, "config.toml"))
	t.Setenv("VECTRABOT_PROVIDER_ENV", filepath.Join(dir, "none.env"))
	root := filepath.Join(dir, "ws")
	_ = os.MkdirAll(root, 0o755)
	srv := httptest.NewServer(newServer(testTokens(), root))
	t.Cleanup(srv.Close)
	env := &botTestEnv{t: t, srv: srv, root: root, frames: make(chan map[string]any, 1000)}
	res, err := http.Get(srv.URL + "/api/events?token=secret")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("events: %v %v", err, res)
	}
	t.Cleanup(func() { res.Body.Close() })
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
		for sc.Scan() {
			if line, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var f map[string]any
				if json.Unmarshal([]byte(line), &f) == nil {
					env.frames <- f
				}
			}
		}
	}()
	return env
}

func (e *botTestEnv) do(method, path, body string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// waitFor returns the first frame matching ok within 10s.
func (e *botTestEnv) waitFor(what string, ok func(map[string]any) bool) map[string]any {
	e.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-e.frames:
			if ok(f) {
				return f
			}
		case <-deadline:
			e.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func messageFrame(f map[string]any) map[string]any {
	if f["kind"] != "message" {
		return nil
	}
	m, _ := f["message"].(map[string]any)
	return m
}

func textContains(contains string) func(map[string]any) bool {
	return func(f map[string]any) bool {
		m := messageFrame(f)
		if m == nil || m["role"] != "bot" {
			return false
		}
		s, _ := m["text"].(string)
		return strings.Contains(s, contains)
	}
}

func turnDone(f map[string]any) bool { return f["kind"] == "turn" && f["busy"] == false }

func TestBotsCreateChatAndStream(t *testing.T) {
	e := newBotTestEnv(t)
	code, body := e.do("POST", "/api/bots", `{"name":"Scout","instructions":"Be brief.","title":"Researcher"}`)
	if code != 200 {
		t.Fatalf("create: %d %v", code, body)
	}
	bot := body["bot"].(map[string]any)
	id, tid := bot["id"].(string), bot["threadId"].(string)
	_, list := e.do("GET", "/api/bots", "")
	if n := len(list["bots"].([]any)); n != 2 { // seeded Chief + Scout
		t.Fatalf("want 2 bots, got %d", n)
	}
	code, body = e.do("POST", "/api/bots/"+id+"/messages", `{"text":"hello"}`)
	if code != 200 || body["threadId"] != tid {
		t.Fatalf("send: %d %v", code, body)
	}
	e.waitFor("streamed reply", textContains("[primed] Hello world"))
	e.waitFor("turn end", turnDone)
	_, page := e.do("GET", "/api/threads/"+tid+"/messages", "")
	msgs := page["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("messages: %v", msgs)
	}
	session := page["session"].(map[string]any)
	if session["commands"] == nil || session["mode"] != "default" {
		t.Fatalf("session state: %v", session)
	}
	// Second turn in the same session is not primed again.
	e.do("POST", "/api/bots/"+id+"/messages", `{"text":"again"}`)
	f := e.waitFor("second reply", textContains("Hello world"))
	if strings.Contains(messageFrame(f)["text"].(string), "[primed]") {
		t.Fatal("instructions sent twice")
	}
	e.waitFor("turn end", turnDone)
	_, list = e.do("GET", "/api/bots/"+id, "")
	if p := list["bot"].(map[string]any)["preview"]; p != "Hello world" {
		t.Fatalf("preview %v", p)
	}
}

func TestBotQuestionPlanPermissionAndCancel(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	id, tid := bot["id"].(string), bot["threadId"].(string)
	answer := func(prompt, reqType, answer, expect string) {
		e.do("POST", "/api/bots/"+id+"/messages", `{"text":"`+prompt+`"}`)
		f := e.waitFor(reqType+" card", func(f map[string]any) bool {
			m := messageFrame(f)
			return m != nil && m["kind"] == "request" && m["request"].(map[string]any)["type"] == reqType && m["request"].(map[string]any)["state"] == "pending"
		})
		rid := messageFrame(f)["request"].(map[string]any)["id"].(string)
		code, out := e.do("POST", "/api/threads/"+tid+"/respond", `{"requestId":"`+rid+`","answer":`+answer+`}`)
		if code != 200 {
			t.Fatalf("respond %s: %d %v", reqType, code, out)
		}
		e.waitFor(reqType+" result", textContains(expect))
		e.waitFor("turn end", turnDone)
	}
	answer("ask", "question", `{"answers":{"Color?":["Blue"]}}`, `"answers":{"Color?":["Blue"]}`)
	answer("ask", "question", `{"outcome":"skip_interview"}`, `"outcome":"skip_interview"`)
	answer("plan", "plan", `{"outcome":"approved"}`, `{"outcome":"approved"}`)
	answer("plan", "plan", `{"outcome":"rejected","feedback":"smaller"}`, `"feedback":"smaller"`)
	answer("perm", "permission", `{"optionId":"allow-once"}`, `"optionId":"allow-once"`)

	// Cancel a running turn.
	e.do("POST", "/api/bots/"+id+"/messages", `{"text":"slow"}`)
	e.waitFor("working", textContains("working"))
	if code, _ := e.do("POST", "/api/bots/"+id+"/messages", `{"text":"queued one"}`); code != 200 {
		t.Fatal("queue failed")
	}
	e.do("POST", "/api/bots/"+id+"/interrupt", `{}`)
	f := e.waitFor("cancelled", func(f map[string]any) bool { return turnDone(f) })
	if f["stopReason"] != "cancelled" {
		t.Fatalf("stop reason %v", f)
	}

	// Modes switch the live session.
	if code, _ := e.do("POST", "/api/threads/"+tid+"/mode", `{"modeId":"plan"}`); code != 200 {
		t.Fatal("mode")
	}
	_, st := e.do("GET", "/api/threads/"+tid+"/session", "")
	if st["mode"] != "plan" {
		t.Fatalf("mode not plan: %v", st)
	}
}

func TestBotToolCardsAndFileChanges(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	id := bot["id"].(string)
	e.do("POST", "/api/bots/"+id+"/messages", `{"text":"edit"}`)
	f := e.waitFor("tool done", func(f map[string]any) bool {
		m := messageFrame(f)
		return m != nil && m["kind"] == "tool" && m["tool"].(map[string]any)["status"] == "completed"
	})
	tool := messageFrame(f)["tool"].(map[string]any)
	if tool["name"] != "write" || len(tool["diffs"].([]any)) != 1 {
		t.Fatalf("tool card %v", tool)
	}
	f = e.waitFor("changes", func(f map[string]any) bool {
		m := messageFrame(f)
		return m != nil && m["kind"] == "changes"
	})
	ch := messageFrame(f)["changes"].([]any)[0].(map[string]any)
	if ch["path"] != "notes.md" || ch["kind"] != "added" {
		t.Fatalf("changes %v", ch)
	}
}

func TestBotResumeAfterRestart(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	id, tid := bot["id"].(string), bot["threadId"].(string)
	e.do("POST", "/api/bots/"+id+"/messages", `{"text":"hi"}`)
	e.waitFor("turn end", turnDone)
	raw, _ := os.ReadFile(filepath.Join(e.root, ".vectrabot", "bots", "index.json"))
	if !strings.Contains(string(raw), `"sessionId": "sess-`) {
		t.Fatal("session id not saved")
	}
	// A second host over the same workspace reloads the saved session; the
	// replayed history must not duplicate messages.
	srv2 := httptest.NewServer(newServer(testTokens(), e.root))
	defer srv2.Close()
	e.srv = srv2
	if code, st := e.do("POST", "/api/threads/"+tid+"/open", `{}`); code != 200 || st["open"] != true {
		t.Fatalf("open: %d %v", code, st)
	}
	_, page := e.do("GET", "/api/threads/"+tid+"/messages", "")
	if n := len(page["messages"].([]any)); n != 2 {
		t.Fatalf("want 2 messages after resume, got %d", n)
	}
}

func TestBotThreadsRoutinesAndProviders(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	id := bot["id"].(string)
	_, out := e.do("POST", "/api/bots/"+id+"/tasks", `{"title":"Second"}`)
	newTid := out["threadId"].(string)
	if out["bot"].(map[string]any)["threadId"] != newTid || len(out["bot"].(map[string]any)["tasks"].([]any)) != 2 {
		t.Fatalf("new thread %v", out)
	}
	if code, _ := e.do("PATCH", "/api/bots/"+id+"/tasks/"+newTid, `{"title":"Renamed","pinned":true}`); code != 200 {
		t.Fatal("rename")
	}
	if code, _ := e.do("DELETE", "/api/bots/"+id+"/tasks/"+newTid, ""); code != 200 {
		t.Fatal("delete thread")
	}
	if code, _ := e.do("DELETE", "/api/bots/"+id, ""); code != 409 {
		t.Fatal("last bot must stay")
	}
	code, out := e.do("POST", "/api/routines", `{"name":"Daily","prompt":"hi","botId":"`+id+`","schedule":{"type":"daily","time":"09:00"}}`)
	if code != 200 {
		t.Fatalf("routine %v", out)
	}
	rid := out["routine"].(map[string]any)["id"].(string)
	if out["routine"].(map[string]any)["nextRunAt"].(float64) <= float64(time.Now().UnixMilli()) {
		t.Fatal("nextRunAt in the past")
	}
	if code, out = e.do("POST", "/api/routines/"+rid+"/run", ""); code != 200 {
		t.Fatalf("run %v", out)
	}
	e.waitFor("routine reply", textContains("Hello world"))
	if code, _ = e.do("POST", "/api/routines", `{"name":"x","prompt":"y","botId":"`+id+`","schedule":{"type":"interval","everyMinutes":1}}`); code != 400 {
		t.Fatal("interval floor not enforced")
	}
	code, st := e.do("GET", "/api/providers", "")
	if code != 200 || st["hasApiKey"] != true {
		t.Fatalf("providers %d %v", code, st)
	}
	if k := st["apiKey"].(string); strings.Contains(k, "should-never-leak") || !strings.HasSuffix(k, "1234") {
		t.Fatalf("key not masked: %q", k)
	}
	if code, _ := e.do("POST", "/api/providers/nope", `{}`); code != 404 {
		t.Fatal("unknown action allowed")
	}
	code, models := e.do("GET", "/api/models", "")
	if code != 200 || models["modelState"] == nil {
		t.Fatalf("models %v", models)
	}
	// Files
	req, _ := http.NewRequest("PUT", e.srv.URL+"/api/bots/"+id+"/files/brief.txt", strings.NewReader("notes"))
	req.Header.Set("Authorization", "Bearer secret")
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	_, files := e.do("GET", "/api/bots/"+id+"/files", "")
	if len(files["files"].([]any)) != 1 {
		t.Fatalf("files %v", files)
	}
	if code, _ := e.do("PUT", "/api/bots/"+id+"/files/..", "x"); code == 200 {
		t.Fatal("bad name accepted")
	}
}

func TestEventsNeedToken(t *testing.T) {
	e := newBotTestEnv(t)
	res, _ := http.Get(e.srv.URL + "/api/events")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("events open without token: %d", res.StatusCode)
	}
	res, _ = http.Get(e.srv.URL + "/api/bots?token=secret")
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("query token accepted outside events")
	}
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("x", 0)
	now := time.Date(2026, 1, 5, 10, 0, 0, 0, loc) // Monday
	if got := time.UnixMilli(nextRun(routineSchedule{Type: "daily", Time: "09:00"}, now)).In(loc); got.Day() != 6 || got.Hour() != 9 {
		t.Fatalf("daily %v", got)
	}
	if got := time.UnixMilli(nextRun(routineSchedule{Type: "daily", Time: "09:00", Weekdays: []int{5}}, now)).In(loc); got.Weekday() != time.Friday {
		t.Fatalf("weekday %v", got)
	}
	anchor := now.Add(-25 * time.Minute).UnixMilli()
	if got := nextRun(routineSchedule{Type: "interval", EveryMinutes: 10, AnchorAt: anchor}, now); got != now.Add(5*time.Minute).UnixMilli() {
		t.Fatalf("interval %v", time.UnixMilli(got))
	}
	if nextRun(routineSchedule{Type: "once", At: now.Add(-time.Minute).UnixMilli()}, now) != 0 {
		t.Fatal("past once")
	}
}

func TestBotTurnErrorsMapToActions(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	e.do("POST", "/api/bots/"+bot["id"].(string)+"/messages", `{"text":"fail401"}`)
	f := e.waitFor("error notice", func(f map[string]any) bool {
		m := messageFrame(f)
		return m != nil && m["kind"] == "notice" && m["error"] == true
	})
	m := messageFrame(f)
	if m["errorCode"].(float64) != -32000 || m["action"] != "providers" {
		t.Fatalf("notice %v", m)
	}
	// The released CLI wraps some 401s in -32603; a 403 must not ask for a new key.
	for prompt, want := range map[string]string{"fail401internal": "providers", "fail403": "retry"} {
		e.waitFor("turn end", turnDone)
		e.do("POST", "/api/bots/"+bot["id"].(string)+"/messages", `{"text":"`+prompt+`"}`)
		f := e.waitFor(prompt+" notice", func(f map[string]any) bool {
			m := messageFrame(f)
			return m != nil && m["kind"] == "notice" && m["error"] == true && m["errorCode"] == float64(-32603)
		})
		if got := messageFrame(f)["action"]; got != want {
			t.Fatalf("%s action %v want %s: %v", prompt, got, want, messageFrame(f))
		}
	}
}

// A provider change during a turn replaces the bot's process once the turn ends.
func TestProviderSaveReplacesBusyProcessAfterTurn(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("GET", "/api/bots", "")
	bot := body["bots"].([]any)[0].(map[string]any)
	id, tid := bot["id"].(string), bot["threadId"].(string)
	pid := func() string {
		e.do("POST", "/api/bots/"+id+"/messages", `{"text":"pid"}`)
		f := e.waitFor("pid", textContains("pid="))
		e.waitFor("turn end", turnDone)
		m := messageFrame(f)
		return m["text"].(string)
	}
	before := pid()
	e.do("POST", "/api/bots/"+id+"/messages", `{"text":"ask"}`)
	f := e.waitFor("question card", func(f map[string]any) bool {
		m := messageFrame(f)
		return m != nil && m["kind"] == "request" && m["request"].(map[string]any)["state"] == "pending"
	})
	if code, out := e.do("POST", "/api/providers/save", `{}`); code != 200 {
		t.Fatalf("save %d %v", code, out)
	}
	rid := messageFrame(f)["request"].(map[string]any)["id"].(string)
	if code, out := e.do("POST", "/api/threads/"+tid+"/respond", `{"requestId":"`+rid+`","answer":{"answers":{"Color?":["Red"]}}}`); code != 200 {
		t.Fatalf("respond %d %v", code, out)
	}
	e.waitFor("answer", textContains("Red"))
	e.waitFor("turn end", turnDone)
	if after := pid(); after == before {
		t.Fatalf("process kept after a provider change: %s", after)
	}
}
