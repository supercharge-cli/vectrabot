package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type turnRequest struct {
	Engine  string `json:"engine"`
	Prompt  string `json:"prompt"`
	AgentID string `json:"agentId"`
}

// turnTimeout bounds one Supercharge turn so a stuck CLI cannot hold the
// request (and the sandbox) open forever.
func turnTimeout() time.Duration {
	if raw := os.Getenv("VECTRABOT_TURN_TIMEOUT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

// providerEnvFiles are the overlays the cloud's provider setup writes. They hold
// the customer's own provider choice and key, so a turn must see them.
func providerEnvFiles() []string {
	if raw := strings.TrimSpace(os.Getenv("VECTRABOT_PROVIDER_ENV")); raw != "" {
		return strings.Split(raw, ":")
	}
	return []string{"/home/user/.superagent/daemon.env", "/home/user/.superagent/gateway.env"}
}

// readEnvFile parses simple KEY=VALUE lines (optionally quoted, optionally
// prefixed with export). It never evaluates shell syntax.
func readEnvFile(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var out []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimPrefix(line, "export ")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			continue
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		out = append(out, key+"="+value)
	}
	return out
}

// turnArgs is the Supercharge CLI's single-turn, non-interactive form.
func turnArgs(root, prompt, rules string) []string {
	args := []string{"-p", prompt, "--output-format", "json", "--always-approve", "--cwd", root}
	if rules != "" {
		args = append(args, "--rules", rules)
	}
	return args
}

// cliReply is the JSON the CLI prints in json output mode. Errors come back as
// {"type":"error","message":...} with exit status 0, so the type decides.
type cliReply struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Result  string `json:"result"`
	Text    string `json:"text"`
}

func parseTurnOutput(out []byte) (reply map[string]any, typed cliReply, ok bool) {
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var loose map[string]any
		var next cliReply
		if json.Unmarshal([]byte(line), &loose) == nil && json.Unmarshal([]byte(line), &next) == nil {
			reply, typed, ok = loose, next, true
			if next.Type == "error" {
				return
			}
		}
	}
	return
}

func serveTurn(root string, state *diskState, w http.ResponseWriter, r *http.Request) {
	var body turnRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid turn"})
		return
	}
	if body.Engine != "" && body.Engine != "supercharge" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only the supercharge engine is allowed"})
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt required"})
		return
	}
	rules := ""
	if body.AgentID != "" {
		agent, ok := state.agent(body.AgentID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
			return
		}
		rules = strings.TrimSpace(agent.Instructions)
	}
	ctx, cancel := context.WithTimeout(r.Context(), turnTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, superchargeBinary(), turnArgs(root, body.Prompt, rules)...)
	cmd.Dir = root
	env := os.Environ()
	for _, file := range providerEnvFiles() {
		env = append(env, readEnvFile(file)...)
	}
	cmd.Env = append(env, "VECTRABOT_ENGINE=supercharge")
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": "supercharge turn timed out", "detail": trimOutput(out)})
		return
	}
	reply, typed, parsed := parseTurnOutput(out)
	if parsed && typed.Type == "error" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": typed.Message, "engine": "supercharge"})
		return
	}
	if err != nil {
		detail := trimOutput(out)
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			detail = trimOutput(exit.Stderr)
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "supercharge did not run", "detail": detail})
		return
	}
	text := typed.Result
	if text == "" {
		text = typed.Text
	}
	if text == "" && !parsed {
		text = strings.TrimSpace(string(out))
	}
	payload := map[string]any{"engine": "supercharge", "output": trimOutput([]byte(text))}
	if parsed {
		payload["reply"] = reply
	}
	writeJSON(w, http.StatusOK, payload)
}

func superchargeBinary() string {
	if bin := strings.TrimSpace(os.Getenv("VECTRABOT_SUPERCHARGE_BIN")); bin != "" {
		return bin
	}
	return "supercharge"
}

func trimOutput(out []byte) string {
	const max = 16000
	if len(out) > max {
		out = out[:max]
	}
	return string(out)
}
