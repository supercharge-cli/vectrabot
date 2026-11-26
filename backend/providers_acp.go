package main

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// providerControl is a session-less ACP process for provider setup: the same
// `_supercharge/setup/*` methods the CLI's own /setup uses.
type providerControl struct {
	hub  *botHub
	mu   sync.Mutex
	conn *acpConn
	init json.RawMessage
	used time.Time
}

type quietHandler struct{}

func (quietHandler) notify(string, json.RawMessage) {}
func (quietHandler) request(context.Context, string, json.RawMessage) (any, error) {
	return nil, &rpcError{Code: -32601, Message: "Method not found"}
}

func newProviderControl(hub *botHub) *providerControl {
	p := &providerControl{hub: hub}
	go func() {
		for range time.Tick(time.Minute) {
			p.mu.Lock()
			if p.conn != nil && time.Since(p.used) > 5*time.Minute {
				p.conn.close()
				p.conn = nil
			}
			p.mu.Unlock()
		}
	}()
	return p
}

func (p *providerControl) connLocked(ctx context.Context) (*acpConn, error) {
	p.used = time.Now()
	if p.conn != nil && p.conn.alive() {
		return p.conn, nil
	}
	cmd, err := p.hub.agentCommand()
	if err != nil {
		return nil, err
	}
	conn, err := startACP(cmd, quietHandler{})
	if err != nil {
		return nil, err
	}
	init, err := initializeACP(ctx, conn)
	if err != nil {
		conn.close()
		return nil, err
	}
	p.conn, p.init = conn, init
	return conn, nil
}

func (p *providerControl) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p.mu.Lock()
	conn, err := p.connLocked(ctx)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = conn.call(ctx, method, params, &out)
	return out, err
}

// restart drops the control process so the next call reads the new config.
func (p *providerControl) restart() {
	p.mu.Lock()
	if p.conn != nil {
		p.conn.close()
		p.conn = nil
	}
	p.mu.Unlock()
}

var providerActions = map[string]string{
	"models":        "_supercharge/setup/models",
	"save":          "_supercharge/setup/save",
	"auth/start":    "_supercharge/setup/auth_start",
	"auth/poll":     "_supercharge/setup/auth_poll",
	"auth/cancel":   "_supercharge/setup/auth_cancel",
	"device/start":  "_supercharge/setup/device_start",
	"device/poll":   "_supercharge/setup/device_poll",
	"device/cancel": "_supercharge/setup/device_cancel",
	"image/status":  "_supercharge/setup/image_status",
	"image/save":    "_supercharge/setup/image_save",
}

var secretField = regexp.MustCompile(`(?i)^(api_?key|apikey|key|token|access_?token|refresh_?token|secret|password|client_?secret)$`)

// scrubSecrets removes credential values from anything we pass back to the
// apps. The CLI reports hasApiKey instead of keys, but a reply never carries
// a full key even if a future version adds one.
func scrubSecrets(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && secretField.MatchString(k) {
				t[k] = maskSecret(s)
				continue
			}
			t[k] = scrubSecrets(val)
		}
	case []any:
		for i := range t {
			t[i] = scrubSecrets(t[i])
		}
	}
	return v
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}

func writeScrubbed(w http.ResponseWriter, raw json.RawMessage) {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		v = map[string]any{}
	}
	writeJSON(w, http.StatusOK, scrubSecrets(v))
}

func writeRPCError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if e, ok := err.(*rpcError); ok && e.Code == -32602 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func (p *providerControl) serveStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	raw, err := p.call(ctx, "_supercharge/setup/status", map[string]any{})
	if err != nil {
		writeRPCError(w, err)
		return
	}
	writeScrubbed(w, raw)
}

func (p *providerControl) serveAction(w http.ResponseWriter, r *http.Request) {
	action := strings.Trim(r.PathValue("action"), "/")
	if sub := r.PathValue("sub"); sub != "" {
		action += "/" + sub
	}
	method, ok := providerActions[action]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown provider action"})
		return
	}
	var params map[string]any
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&params); err != nil {
		params = map[string]any{}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := p.call(ctx, method, params)
	if err != nil {
		writeRPCError(w, err)
		return
	}
	if action == "save" {
		// The CLI's setup now owns the provider choice; the cloud settings no
		// longer override it on the next process start.
		if err := markCLIProvider(envMap(savedOverlay())); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not record the provider choice"})
			return
		}
	}
	if action == "save" || action == "image/save" {
		// A new key or login changes the config: new turns start fresh processes.
		p.hub.restartIdle()
		// The control process also answers /api/models from its startup state.
		p.restart()
	}
	writeScrubbed(w, raw)
}

// serveModels lists the models the CLI can switch between, for bot settings.
func (p *providerControl) serveModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	p.mu.Lock()
	_, err := p.connLocked(ctx)
	init := p.init
	p.mu.Unlock()
	if err != nil {
		writeRPCError(w, err)
		return
	}
	var body struct {
		ModelState json.RawMessage `json:"modelState"`
		Meta       struct {
			ModelState json.RawMessage `json:"modelState"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(init, &body)
	state := body.ModelState
	if len(state) == 0 {
		state = body.Meta.ModelState
	}
	var v any
	_ = json.Unmarshal(state, &v)
	writeJSON(w, http.StatusOK, map[string]any{"modelState": scrubSecrets(v)})
}
