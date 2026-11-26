package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// desktopBridge drives the template's desktop runtime (handoff state, on
// 127.0.0.1:9223) and relays the VNC server (127.0.0.1:5900) to a viewer that
// holds a ticket for the current takeover.
type desktopBridge struct {
	runtime string
	vncAddr string
	client  *http.Client

	mu      sync.Mutex
	tickets map[string]vncTicket
	// viewing marks a connected viewer as activity; it returns the release.
	viewing func() func()
}

type vncTicket struct {
	handoffID string
	botID     string
	expiresAt time.Time
}

var handoffIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)
var botIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func newDesktopBridge() *desktopBridge {
	runtime := strings.TrimRight(strings.TrimSpace(os.Getenv("VECTRABOT_DESKTOP_RUNTIME")), "/")
	if runtime == "" {
		runtime = "http://127.0.0.1:9223"
	}
	vnc := strings.TrimSpace(os.Getenv("VECTRABOT_VNC_ADDR"))
	if vnc == "" {
		vnc = "127.0.0.1:5900"
	}
	return &desktopBridge{
		runtime: runtime,
		vncAddr: vnc,
		client:  &http.Client{Timeout: 15 * time.Second},
		tickets: map[string]vncTicket{},
	}
}

type runtimeStatus struct {
	OK              bool   `json:"ok"`
	Handoff         bool   `json:"handoff"`
	HandoffID       string `json:"handoffId"`
	HandoffPhase    string `json:"handoffPhase"`
	ExpiresAt       int64  `json:"expiresAt"`
	BotID           string `json:"botId"`
	HandoffProtocol int    `json:"handoffProtocol"`
}

func (d *desktopBridge) call(ctx context.Context, method, path string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.runtime+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("accept", "application/json")
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	// The host is the authenticated viewer side: the user, not an agent.
	req.Header.Set("x-vectra-browser-observer", "1")
	res, err := d.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, err
}

func (d *desktopBridge) status(ctx context.Context) (runtimeStatus, map[string]any, error) {
	code, raw, err := d.call(ctx, http.MethodGet, "/status", nil)
	if err != nil {
		return runtimeStatus{}, nil, err
	}
	var typed runtimeStatus
	var loose map[string]any
	if code != http.StatusOK || json.Unmarshal(raw, &typed) != nil || json.Unmarshal(raw, &loose) != nil {
		return runtimeStatus{}, nil, errDesktopUnavailable
	}
	return typed, loose, nil
}

type desktopError string

func (e desktopError) Error() string { return string(e) }

const errDesktopUnavailable = desktopError("desktop runtime unavailable")

func requestBotID(raw string) (string, bool) {
	if raw == "" {
		return "main", true
	}
	return raw, botIDPattern.MatchString(raw)
}

func (d *desktopBridge) serveStatus(w http.ResponseWriter, r *http.Request) {
	botID, ok := requestBotID(r.URL.Query().Get("botId"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid botId"})
		return
	}
	_, loose, err := d.status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "connected": false, "desktop": false, "handoff": false, "botId": botID, "error": errDesktopUnavailable.Error()})
		return
	}
	loose["enabled"] = true
	loose["connected"] = true
	writeJSON(w, http.StatusOK, loose)
}

type handoffRequest struct {
	Action    string `json:"action"`
	Minutes   any    `json:"minutes"`
	HandoffID string `json:"handoffId"`
	BotID     string `json:"botId"`
	SessionID string `json:"sessionId,omitempty"`
}

func handoffMinutes(value any) int {
	minutes := 10
	if n, ok := value.(float64); ok {
		minutes = int(n)
	}
	if minutes < 1 {
		return 1
	}
	if minutes > 30 {
		return 30
	}
	return minutes
}

func (d *desktopBridge) serveHandoff(w http.ResponseWriter, r *http.Request) {
	var body handoffRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "invalid handoff"})
		return
	}
	if body.Action != "start" && body.Action != "stop" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "action must be start or stop"})
		return
	}
	if !handoffIDPattern.MatchString(body.HandoffID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "A unique handoffId is required"})
		return
	}
	botID, ok := requestBotID(body.BotID)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid botId"})
		return
	}
	payload := map[string]any{"handoffId": body.HandoffID, "botId": botID}
	if body.Action == "start" {
		payload["minutes"] = handoffMinutes(body.Minutes)
		if body.SessionID != "" {
			payload["sessionId"] = body.SessionID
		}
	}
	code, raw, err := d.call(r.Context(), http.MethodPost, "/handoff/"+body.Action, payload)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": errDesktopUnavailable.Error()})
		return
	}
	if body.Action == "stop" && code == http.StatusOK {
		d.dropTickets(body.HandoffID)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(raw)
}

func (d *desktopBridge) serveTicket(w http.ResponseWriter, r *http.Request) {
	handoffID := r.URL.Query().Get("handoffId")
	if !handoffIDPattern.MatchString(handoffID) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "A current handoffId is required"})
		return
	}
	botID, ok := requestBotID(r.URL.Query().Get("botId"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "Invalid botId"})
		return
	}
	state, _, err := d.status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": errDesktopUnavailable.Error()})
		return
	}
	if !state.Handoff || state.HandoffID != handoffID || state.ExpiresAt <= time.Now().UnixMilli() {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": "Take control before opening the desktop"})
		return
	}
	ticket, err := d.issue(handoffID, botID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ticket": ticket, "handoffId": handoffID, "botId": botID, "expiresInSeconds": 60})
}

func (d *desktopBridge) issue(handoffID, botID string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for key, item := range d.tickets {
		if item.expiresAt.Before(now) {
			delete(d.tickets, key)
		}
	}
	if len(d.tickets) >= 256 {
		return "", desktopError("too many outstanding desktop tickets")
	}
	d.tickets[ticket] = vncTicket{handoffID: handoffID, botID: botID, expiresAt: now.Add(60 * time.Second)}
	return ticket, nil
}

// redeem consumes a ticket: each one opens a single viewer connection.
func (d *desktopBridge) redeem(ticket string) (vncTicket, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	item, ok := d.tickets[ticket]
	delete(d.tickets, ticket)
	if !ok || item.expiresAt.Before(time.Now()) {
		return vncTicket{}, false
	}
	return item, true
}

func (d *desktopBridge) dropTickets(handoffID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, item := range d.tickets {
		if item.handoffID == handoffID {
			delete(d.tickets, key)
		}
	}
}

func (d *desktopBridge) serveVNC(w http.ResponseWriter, r *http.Request) {
	item, ok := d.redeem(r.URL.Query().Get("ticket"))
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "desktop ticket required"})
		return
	}
	state, _, err := d.status(r.Context())
	if err != nil || !state.Handoff || state.HandoffID != item.handoffID {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Take control before opening the desktop"})
		return
	}
	upstream, err := net.DialTimeout("tcp", d.vncAddr, 5*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "desktop VNC unavailable"})
		return
	}
	defer upstream.Close()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	if d.viewing != nil {
		defer d.viewing()()
	}
	conn.SetReadLimit(4 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		buf := make([]byte, 64<<10)
		for {
			n, err := upstream.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// Returning control ends the viewer: watch the handoff and close when it moves on.
	go func() {
		defer cancel()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now, _, err := d.status(ctx)
				if err == nil && (!now.Handoff || now.HandoffID != item.handoffID) {
					return
				}
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if _, err := upstream.Write(data); err != nil {
			return
		}
	}
}
