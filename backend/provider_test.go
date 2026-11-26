package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeKey = "mk_test_0123456789abcdef"

func savedMistral() map[string]string {
	return map[string]string{
		"SUPERAGENT_PROVIDER":             "mistral",
		"SUPERAGENT_MODEL":                "mistral-medium-latest",
		"SUPERAGENT_PROVIDER_BASE_URL":    "https://api.mistral.ai/v1/",
		"SUPERAGENT_PROVIDER_FORMAT":      "chat_completions",
		"SUPERAGENT_PROVIDER_API_KEY_ENV": "MISTRAL_API_KEY",
		"MISTRAL_API_KEY":                 fakeKey,
	}
}

func useConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("VECTRABOT_SUPERCHARGE_CONFIG", path)
	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyProviderWritesOwnerOnlyBlockAndKeepsUserConfig(t *testing.T) {
	original := "[marketplace]\ndefault_skills_installs_purged = true\n"
	path := useConfig(t, original)

	key, err := applyProvider(savedMistral())
	if err != nil || key != "vectra-mistral" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	text := readText(t, path)
	for _, want := range []string{
		"[marketplace]", `default = "vectra-mistral"`, `[model."vectra-mistral"]`,
		`model = "mistral-medium-latest"`, `base_url = "https://api.mistral.ai/v1"`,
		`api_key = "` + fakeKey + `"`, `api_backend = "chat_completions"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}

	if _, err := applyProvider(savedMistral()); err != nil || readText(t, path) != text {
		t.Fatal("second apply changed the file")
	}

	if key, err := applyProvider(map[string]string{}); err != nil || key != "" {
		t.Fatalf("clear key=%q err=%v", key, err)
	}
	if got := readText(t, path); got != original {
		t.Fatalf("clearing did not restore the user config:\n%s", got)
	}
}

func TestApplyProviderLeavesUserDefaultAndDropsStrayManagedTable(t *testing.T) {
	path := useConfig(t, "[models]\ndefault = \"mine\"\n\n[model.\"vectra-old\"]\napi_key = \"stale\"\n\n[ui]\ntheme = \"dark\"\n")
	if _, err := applyProvider(savedMistral()); err != nil {
		t.Fatal(err)
	}
	text := readText(t, path)
	if strings.Count(text, "[models]") != 1 || !strings.Contains(text, `default = "mine"`) {
		t.Fatalf("user [models] not kept as is:\n%s", text)
	}
	if strings.Contains(text, "stale") || strings.Contains(text, "vectra-old") {
		t.Fatalf("stray managed table kept:\n%s", text)
	}
	if !strings.Contains(text, "[ui]\ntheme = \"dark\"") {
		t.Fatalf("user table lost:\n%s", text)
	}
}

func TestProviderFromEnvRejectsUnsafeValues(t *testing.T) {
	cases := map[string]func(map[string]string){
		"plain http":  func(v map[string]string) { v["SUPERAGENT_PROVIDER_BASE_URL"] = "http://evil.example/v1" },
		"key newline": func(v map[string]string) { v["MISTRAL_API_KEY"] = "a\n[x]" },
		"no key":      func(v map[string]string) { delete(v, "MISTRAL_API_KEY") },
		"bad id":      func(v map[string]string) { v["SUPERAGENT_PROVIDER"] = "mis\"tral" },
		"no model":    func(v map[string]string) { v["SUPERAGENT_MODEL"] = "" },
	}
	for name, mutate := range cases {
		vars := savedMistral()
		mutate(vars)
		if _, ok := providerFromEnv(vars); ok {
			t.Fatalf("%s accepted", name)
		}
	}
	vars := savedMistral()
	vars["SUPERAGENT_PROVIDER_BASE_URL"] = "http://127.0.0.1:11434/v1"
	vars["SUPERAGENT_MODEL"] = `say "hi"`
	setup, ok := providerFromEnv(vars)
	if !ok || !strings.Contains(managedBlock(setup, false), `model = "say \"hi\""`) {
		t.Fatal("loopback provider or quoting failed")
	}
}

func TestTurnUsesSavedProviderWithoutPassingSecrets(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "daemon.env")
	var lines []string
	for k, v := range savedMistral() {
		lines = append(lines, k+"="+v)
	}
	lines = append(lines, "SUPERAGENT_API_TOKEN=inbox-secret", "TELEGRAM_BOT_TOKEN=tg-secret")
	if err := os.WriteFile(envFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_PROVIDER_ENV", envFile)
	t.Setenv("VECTRABOT_SESSION", "host-session-secret")
	t.Setenv("VECTRABOT_CLOUD_TOKEN", "host-cloud-secret")
	config := useConfig(t, "")
	record := filepath.Join(dir, "record")
	bin := filepath.Join(dir, "supercharge")
	script := "#!/bin/sh\n{ echo \"ARGS $*\"; env; } > " + record + "\nprintf '{\"text\":\"PONG\",\"stopReason\":\"end_turn\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VECTRABOT_SUPERCHARGE_BIN", bin)
	root := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServer(testTokens(), root))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/turns", strings.NewReader(`{"prompt":"ping"}`))
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 200 || body["output"] != "PONG" {
		t.Fatalf("turn %d %v", res.StatusCode, body)
	}
	seen := readText(t, record)
	if !strings.Contains(seen, "-m vectra-mistral") || !strings.Contains(seen, "SUPERAGENT_PROVIDER=mistral") {
		t.Fatalf("turn did not select the saved provider:\n%s", seen)
	}
	for _, secret := range []string{fakeKey, "inbox-secret", "tg-secret", "host-session-secret", "host-cloud-secret"} {
		if strings.Contains(seen, secret) {
			t.Fatalf("turn environment leaked %q", secret)
		}
	}
	if !strings.Contains(readText(t, config), fakeKey) {
		t.Fatal("provider key not written to the CLI config")
	}
}

func TestParseTurnOutputReadsPrettyJSON(t *testing.T) {
	out := []byte("{\n  \"text\": \"PONG\",\n  \"stopReason\": \"end_turn\",\n  \"modelUsage\": {\"mistral-medium-latest\": {\"modelCalls\": 1}}\n}\n")
	reply, typed, ok := parseTurnOutput(out)
	if !ok || typed.Text != "PONG" || reply["modelUsage"] == nil {
		t.Fatalf("ok=%v typed=%+v", ok, typed)
	}
	_, typed, ok = parseTurnOutput([]byte("noise\n{\"type\":\"error\",\"message\":\"No provider\"}\nError: No provider\n"))
	if !ok || typed.Type != "error" || typed.Message != "No provider" {
		t.Fatalf("error line ok=%v typed=%+v", ok, typed)
	}
}
