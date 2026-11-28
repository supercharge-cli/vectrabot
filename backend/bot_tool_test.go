package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestChiefCanCreateABotWithTheToolToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VECTRABOT_TOOL_TOKEN_FILE", dir+"/token")
	h := newBotHub(dir, openBotStore(dir, nil), newEventHub())
	intro := botIntro(h.store.bots[0], dir)
	if !strings.Contains(intro, "You are the main bot") || !strings.Contains(intro, "vectra-bot create") {
		t.Fatalf("intro missing create instructions:\n%s", intro)
	}
	if strings.Contains(intro, dir+"/token") {
		t.Fatal("intro must not tell the bot to print the token path")
	}
	no := httptest.NewRecorder()
	h.serveToolBots(no, httptest.NewRequest(http.MethodPost, "/v1/bots/tool", strings.NewReader(`{"name":"Reviewer"}`)))
	if no.Code != 401 {
		t.Fatal(no.Code)
	}
	token, err := os.ReadFile(dir + "/token")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/bots/tool", strings.NewReader(`{"name":"Reviewer","instructions":"Review changes"}`))
	req.Header.Set("X-Vectrabot-Tool", strings.TrimSpace(string(token)))
	yes := httptest.NewRecorder()
	h.serveToolBots(yes, req)
	if yes.Code != 200 || !strings.Contains(yes.Body.String(), "Reviewer") {
		t.Fatalf("%d %s", yes.Code, yes.Body.String())
	}
	if sessionPermission("ask") != "always-approve" || sessionPermission("auto") != "auto" {
		t.Fatal(sessionPermission("ask"), sessionPermission("auto"))
	}
}
