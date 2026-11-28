package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// The Supercharge CLI only takes provider credentials from its own config file
// (~/.supercharge/config.toml, the file its /setup writes). Vectra's instance
// settings write the customer's provider choice and key to the env overlays
// instead, so before a turn the host copies that choice into a managed block
// of the CLI config and selects it with -m.

const (
	providerBlockStart = "# >>> vectra provider (managed: change it in Vectra settings) >>>"
	providerBlockEnd   = "# <<< vectra provider <<<"
)

var (
	providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	managedModelTable = regexp.MustCompile(`^\[model\.(?:"vectra-[^"]*"|vectra-[A-Za-z0-9._-]+)\]\s*$`)
	tableHeader       = regexp.MustCompile(`^\s*\[`)
	modelsTable       = regexp.MustCompile(`^\s*\[models\]\s*(#.*)?$`)
	providerConfigMu  sync.Mutex
)

type providerSetup struct {
	ID, Model, BaseURL, Format, Key string
}

// modelKey is the CLI catalog key of the managed provider entry.
func (p providerSetup) modelKey() string { return "vectra-" + p.ID }

func envMap(lines []string) map[string]string {
	out := map[string]string{}
	for _, line := range lines {
		if key, value, ok := strings.Cut(line, "="); ok {
			out[key] = value
		}
	}
	return out
}

func validHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := parsed.Hostname()
	return parsed.Scheme == "http" && (host == "127.0.0.1" || host == "localhost" || host == "::1")
}

func plainValue(value string, max int) bool {
	if value == "" || len(value) > max {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// providerFromEnv reads the provider the cloud settings saved. ok is false when
// none is saved or it is incomplete, in which case the CLI's own config is used.
func providerFromEnv(vars map[string]string) (providerSetup, bool) {
	id := strings.ToLower(strings.TrimSpace(vars["SUPERAGENT_PROVIDER"]))
	keyEnv := strings.TrimSpace(vars["SUPERAGENT_PROVIDER_API_KEY_ENV"])
	setup := providerSetup{
		ID:      id,
		Model:   strings.TrimSpace(vars["SUPERAGENT_MODEL"]),
		BaseURL: strings.TrimRight(strings.TrimSpace(vars["SUPERAGENT_PROVIDER_BASE_URL"]), "/"),
		Format:  strings.TrimSpace(vars["SUPERAGENT_PROVIDER_FORMAT"]),
	}
	if keyEnv != "" {
		setup.Key = strings.TrimSpace(vars[keyEnv])
	}
	switch setup.Format {
	case "chat_completions", "messages", "responses":
	default:
		setup.Format = "chat_completions"
	}
	if !providerIDPattern.MatchString(setup.ID) || !plainValue(setup.Model, 200) ||
		!validHTTPURL(setup.BaseURL) || !plainValue(setup.Key, 4096) {
		return providerSetup{}, false
	}
	return setup, true
}

func superchargeConfigPath() string {
	if path := strings.TrimSpace(os.Getenv("VECTRABOT_SUPERCHARGE_CONFIG")); path != "" {
		return path
	}
	if home := strings.TrimSpace(os.Getenv("SUPERCHARGE_HOME")); home != "" {
		return filepath.Join(home, "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "/home/user"
	}
	return filepath.Join(home, ".supercharge", "config.toml")
}

func tomlString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// stripManaged removes the managed block, and any managed model table left
// behind if another tool rewrote the file without the markers. The CLI's own
// /setup rewrites the file with toml_edit, which can move its own tables
// inside the markers; those are kept and only the managed tables are dropped.
func stripManaged(text string) string {
	var kept []string
	inBlock, inTable := false, false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == providerBlockStart:
			inBlock, inTable = true, true
			continue
		case trimmed == providerBlockEnd:
			inBlock, inTable = false, false
			continue
		case managedModelTable.MatchString(trimmed) || (inBlock && modelsTable.MatchString(line)):
			inTable = true
			continue
		case inTable && tableHeader.MatchString(line):
			inTable = false
		case inTable:
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

func hasModelsTable(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if modelsTable.MatchString(line) {
			return true
		}
	}
	return false
}

func managedBlock(p providerSetup, withDefault bool) string {
	lines := []string{providerBlockStart}
	if withDefault {
		lines = append(lines, "[models]", "default = "+tomlString(p.modelKey()), "")
	}
	lines = append(lines,
		fmt.Sprintf("[model.%s]", tomlString(p.modelKey())),
		"model = "+tomlString(p.Model),
		"name = "+tomlString(p.ID+" · "+p.Model),
		"base_url = "+tomlString(p.BaseURL),
		"api_key = "+tomlString(p.Key),
		"api_backend = "+tomlString(p.Format),
		providerBlockEnd,
	)
	return strings.Join(lines, "\n")
}

// cliOwnerPath marks that the provider was last chosen through the CLI's own
// setup (the apps' Providers screen) rather than the cloud settings.
func cliOwnerPath() string { return superchargeConfigPath() + ".vectra-cli-owned" }

// providerFingerprint identifies the cloud's saved provider without storing it.
func providerFingerprint(vars map[string]string) string {
	setup, ok := providerFromEnv(vars)
	if !ok {
		return "none"
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{setup.ID, setup.Model, setup.BaseURL, setup.Format, setup.Key}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// markCLIProvider records that the CLI's setup now owns the provider choice.
// It holds until the cloud settings change, which hands the choice back.
func markCLIProvider(vars map[string]string) error {
	providerConfigMu.Lock()
	defer providerConfigMu.Unlock()
	path := cliOwnerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(providerFingerprint(vars)), 0o600)
}

// applyProvider writes the saved provider into the CLI config (owner-only) and
// returns the catalog key to select, or "" to leave the CLI's own choice.
// With no saved provider it removes an earlier managed block. After a save
// through the CLI's setup it leaves the file alone until the cloud settings
// change.
func applyProvider(vars map[string]string) (string, error) {
	providerConfigMu.Lock()
	defer providerConfigMu.Unlock()
	if owned, err := os.ReadFile(cliOwnerPath()); err == nil {
		if string(owned) == providerFingerprint(vars) {
			return "", nil
		}
		_ = os.Remove(cliOwnerPath())
	}
	path := superchargeConfigPath()
	current, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	base := stripManaged(string(current))
	setup, ok := providerFromEnv(vars)
	next := base
	if ok {
		if next != "" {
			next += "\n\n"
		}
		next += managedBlock(setup, !hasModelsTable(base))
	}
	if next != "" {
		next += "\n"
	}
	if next != string(current) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", err
		}
		tmp := path + ".vectra.tmp"
		if err := os.WriteFile(tmp, []byte(next), 0o600); err != nil {
			return "", err
		}
		if err := os.Chmod(tmp, 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
	}
	if !ok {
		return "", nil
	}
	return setup.modelKey(), nil
}

var secretEnvName = regexp.MustCompile(`(?i)(KEY|TOKEN|SECRET|PASSWORD|PASSPHRASE|CREDENTIALS?)$`)

// hostOnlyEnv are the host's own credentials; agent turns and terminals never get them.
var hostOnlyEnv = map[string]bool{"VECTRABOT_SESSION": true, "VECTRABOT_CLOUD_TOKEN": true}

// searchKeyEnv reaches the CLI as a search provider credential rather than a
// model credential, so the CLI reads it from the environment instead.
var searchKeyEnv = map[string]bool{"EXA_API_KEY": true}

// childEnv is the environment for processes the host starts on the user's
// behalf: the host's environment without its own credentials, plus the
// non-secret settings from the overlays. Provider keys reach the CLI through
// its config file only, so tools the agent runs cannot read them from env.
func childEnv(overlay []string) []string {
	var env []string
	for _, line := range os.Environ() {
		key, _, _ := strings.Cut(line, "=")
		if hostOnlyEnv[key] {
			continue
		}
		env = append(env, line)
	}
	for _, line := range overlay {
		key, _, _ := strings.Cut(line, "=")
		if hostOnlyEnv[key] || (secretEnvName.MatchString(key) && !searchKeyEnv[key]) {
			continue
		}
		env = append(env, line)
	}
	return env
}
