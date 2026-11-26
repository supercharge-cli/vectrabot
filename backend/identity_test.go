package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

func (e *botTestEnv) raw(method, path string, body []byte) (int, http.Header, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, out
}

func botsOf(t *testing.T, e *botTestEnv) []map[string]any {
	t.Helper()
	_, list := e.do("GET", "/api/bots", "")
	var out []map[string]any
	for _, b := range list["bots"].([]any) {
		out = append(out, b.(map[string]any))
	}
	return out
}

func TestMainBotIsFirstAndCannotBeDeleted(t *testing.T) {
	e := newBotTestEnv(t)
	bots := botsOf(t, e)
	if len(bots) != 1 || bots[0]["main"] != true || bots[0]["avatarKind"] != "generated" || bots[0]["avatarUrl"] != nil {
		t.Fatalf("seed: %v", bots)
	}
	mainID := bots[0]["id"].(string)
	if code, body := e.do("DELETE", "/api/bots/"+mainID, ""); code != 409 || !strings.Contains(body["error"].(string), "main bot") {
		t.Fatalf("delete lone main: %d %v", code, body)
	}
	colors := map[string]bool{bots[0]["color"].(string): true}
	var scout string
	for i, name := range []string{"Scout", "Writer", "Ops"} {
		code, body := e.do("POST", "/api/bots", `{"name":"`+name+`"}`)
		if code != 200 {
			t.Fatalf("create %s: %d %v", name, code, body)
		}
		b := body["bot"].(map[string]any)
		if b["main"] != false {
			t.Fatalf("created bot marked main: %v", b)
		}
		if colors[b["color"].(string)] {
			t.Fatalf("bot %d reused a colour while others were free: %v", i, b["color"])
		}
		colors[b["color"].(string)] = true
		if i == 0 {
			scout = b["id"].(string)
		}
	}
	// Activity moves a created bot up, but the main bot stays first.
	if code, _ := e.do("POST", "/api/bots/"+scout+"/messages", `{"text":"hello"}`); code != 200 {
		t.Fatal("send")
	}
	e.waitFor("turn end", turnDone)
	bots = botsOf(t, e)
	if bots[0]["id"] != mainID || bots[1]["id"] != scout {
		t.Fatalf("order: %v %v", bots[0]["name"], bots[1]["name"])
	}
	code, body := e.do("DELETE", "/api/bots/"+mainID, "")
	if code != 409 || !strings.Contains(body["error"].(string), "main bot") {
		t.Fatalf("delete main: %d %v", code, body)
	}
	if code, body := e.do("PATCH", "/api/bots/"+mainID+"/profile", `{"name":"Captain","color":"#123abc","description":"Runs the show"}`); code != 200 || body["bot"].(map[string]any)["name"] != "Captain" {
		t.Fatalf("rename main: %d %v", code, body)
	}
	if code, _ := e.do("DELETE", "/api/bots/"+scout, ""); code != 200 {
		t.Fatal("delete created bot")
	}
	if n := len(botsOf(t, e)); n != 3 {
		t.Fatalf("want 3 bots, got %d", n)
	}
}

func TestBotIdentityValidationAndPin(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("POST", "/api/bots", `{"name":"Scout","emoji":"🚀","avatarKind":"emoji","avatarShape":"rounded","color":"#AABBCC"}`)
	b := body["bot"].(map[string]any)
	if b["avatarKind"] != "emoji" || b["emoji"] != "🚀" || b["avatarShape"] != "rounded" || b["color"] != "#aabbcc" {
		t.Fatalf("create identity: %v", b)
	}
	id := b["id"].(string)
	for _, bad := range []string{`{"color":"red"}`, `{"avatarShape":"blob"}`, `{"avatarKind":"mascot"}`, `{"avatarKind":"image"}`, `{"emoji":"<script>"}`} {
		if code, _ := e.do("PATCH", "/api/bots/"+id, bad); code != 400 {
			t.Fatalf("%s accepted: %d", bad, code)
		}
	}
	if code, _ := e.do("PATCH", "/api/bots/"+id, `{"pinned":true}`); code != 200 {
		t.Fatal("pin")
	}
	_, _ = e.do("POST", "/api/bots", `{"name":"Later"}`)
	bots := botsOf(t, e)
	if bots[0]["main"] != true || bots[1]["id"] != id || bots[1]["pinned"] != true {
		t.Fatalf("pinned order: %v / %v", bots[1]["name"], bots[1]["pinned"])
	}
	if g, ok := bots[1]["glyph"].(float64); !ok || int(g) != glyphOf(id) || bitsOn(int(g)) < 4 {
		t.Fatalf("glyph: %v", bots[1]["glyph"])
	}
}

func TestAvatarUploadServeDeleteAndPersist(t *testing.T) {
	e := newBotTestEnv(t)
	_, body := e.do("POST", "/api/bots", `{"name":"Scout"}`)
	id := body["bot"].(map[string]any)["id"].(string)
	if code, _, _ := e.raw("PUT", "/api/bots/"+id+"/avatar", []byte("<svg onload=alert(1)>")); code != 415 {
		t.Fatalf("svg accepted: %d", code)
	}
	if code, _, _ := e.raw("PUT", "/api/bots/"+id+"/avatar", bytes.Repeat([]byte{0x89}, maxAvatarBytes+10)); code != 413 {
		t.Fatalf("oversize accepted: %d", code)
	}
	code, _, out := e.raw("PUT", "/api/bots/"+id+"/avatar", tinyPNG)
	if code != 200 || !strings.Contains(string(out), `"avatarKind":"image"`) {
		t.Fatalf("upload: %d %s", code, out)
	}
	e.waitFor("bot frame with avatar", func(f map[string]any) bool {
		b, _ := f["bot"].(map[string]any)
		return f["kind"] == "bot" && b != nil && b["id"] == id && b["avatarUrl"] != nil
	})
	bots := botsOf(t, e)
	var url string
	for _, b := range bots {
		if b["id"] == id {
			url, _ = b["avatarUrl"].(string)
		}
	}
	if !strings.HasPrefix(url, "/api/bots/"+id+"/avatar?v=") {
		t.Fatalf("avatarUrl: %q", url)
	}
	code, hdr, img := e.raw("GET", url, nil)
	if code != 200 || hdr.Get("Content-Type") != "image/png" || hdr.Get("X-Content-Type-Options") != "nosniff" || !bytes.Equal(img, tinyPNG) {
		t.Fatalf("serve: %d %v", code, hdr)
	}
	if code, _, _ := e.raw("GET", "/api/bots/"+id+"/avatar", nil); code != 200 {
		t.Fatal("serve without version")
	}
	_, _ = e.do("PATCH", "/api/bots/"+id, `{"avatarShape":"square","description":"Finds things"}`)

	// A new host on the same workspace (restart, or a resumed sandbox) keeps it all.
	srv2 := httptest.NewServer(newServer(testTokens(), e.root))
	defer srv2.Close()
	e2 := &botTestEnv{t: t, srv: srv2, root: e.root}
	var got map[string]any
	for _, b := range botsOf(t, e2) {
		if b["id"] == id {
			got = b
		}
	}
	if got == nil || got["avatarKind"] != "image" || got["avatarShape"] != "square" || got["description"] != "Finds things" || got["avatarUrl"] != url {
		t.Fatalf("after restart: %v", got)
	}
	if code, _, img := e2.raw("GET", url, nil); code != 200 || !bytes.Equal(img, tinyPNG) {
		t.Fatal("image after restart")
	}
	// Duplicate carries the look but not the threads.
	code, dup := e2.do("POST", "/api/bots/"+id+"/duplicate", "")
	d := dup["bot"].(map[string]any)
	if code != 200 || d["name"] != "Scout copy" || d["avatarKind"] != "image" || d["avatarUrl"] == nil || d["main"] != false {
		t.Fatalf("duplicate: %d %v", code, d)
	}
	code, _, out = e2.raw("DELETE", "/api/bots/"+id+"/avatar", nil)
	if code != 200 || !strings.Contains(string(out), `"avatarUrl":null`) || !strings.Contains(string(out), `"avatarKind":"generated"`) {
		t.Fatalf("delete avatar: %d %s", code, out)
	}
	if code, _, _ := e2.raw("GET", "/api/bots/"+id+"/avatar", nil); code != 404 {
		t.Fatal("avatar still served")
	}
	// Deleting the duplicate removes its stored image.
	dupID := d["id"].(string)
	_, _ = e2.do("DELETE", "/api/bots/"+dupID, "")
	if _, err := os.Stat(filepath.Join(e.root, ".vectrabot", "bots", "avatars", dupID+".png")); !os.IsNotExist(err) {
		t.Fatalf("duplicate's image left behind: %v", err)
	}
}

func TestGlyphSeedIsSharedWithClients(t *testing.T) {
	// The PC and Android clients compute the same value for a draft's preview.
	for seed, want := range map[string]int{"bot_0123456789abcdef": 2479, "a": 10540, "Zz-9": 18499} {
		if glyphOf(seed) != want || bitsOn(want) < 4 || bitsOn(want) > 11 {
			t.Fatalf("%s: %d", seed, want)
		}
	}
	e := newBotTestEnv(t)
	_, body := e.do("POST", "/api/bots", `{"name":"Scout","glyphSeed":"abc123"}`)
	if g := int(body["bot"].(map[string]any)["glyph"].(float64)); g != glyphOf("abc123") {
		t.Fatalf("seeded glyph %d", g)
	}
	if code, _ := e.do("POST", "/api/bots", `{"name":"x","glyphSeed":"no spaces"}`); code != 400 {
		t.Fatal("bad seed accepted")
	}
}

func TestOlderIndexGetsAMainBot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".vectrabot", "bots")
	_ = os.MkdirAll(dir, 0o700)
	idx := `{"bots":[{"id":"b2","name":"Late","color":"red","createdAt":20},{"id":"b1","name":"Chief","color":"#4c8dff","createdAt":10}],"threads":[]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(idx), 0o600); err != nil {
		t.Fatal(err)
	}
	s := openBotStore(root, nil)
	if !s.botLocked("b1").Main || s.botLocked("b2").Main {
		t.Fatal("oldest bot should become main")
	}
	if b := s.botLocked("b2"); !hexColor.MatchString(b.Color) || b.AvatarKind != "generated" || b.AvatarShape != "circle" {
		t.Fatalf("not repaired: %+v", b)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if !strings.Contains(string(raw), `"main": true`) {
		t.Fatal("repair not saved")
	}
}
