package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// botHub runs bots: one persistent `supercharge agent stdio` process per bot,
// one ACP session per thread. Everything the agent streams becomes thread
// messages and SSE frames; everything it asks waits for an app to respond.
type botHub struct {
	store  *botStore
	events *eventHub
	root   string

	// guarded by store.mu
	runtimes map[string]*botRuntime // by bot id
	live     map[string]*threadLive // by thread id
	turning  map[string]bool        // threads with a turn in flight
	queues   map[string][]string    // texts sent while a turn ran

	startMu sync.Mutex // serializes process starts and session opens
}

type botRuntime struct {
	botID    string
	conn     *acpConn
	init     json.RawMessage
	sessions map[string]string // session id -> thread id
	lastUsed time.Time
	// stale is set when the provider config changed during a turn; the process
	// is replaced once it is idle so new turns read the new config.
	stale bool
}

type pendingRequest struct {
	threadID  string
	messageID string
	answer    chan json.RawMessage
}

// threadLive is the in-memory state of an open session.
type threadLive struct {
	sessionID     string
	modes         json.RawMessage
	modeID        string
	models        json.RawMessage
	configOptions json.RawMessage
	commands      json.RawMessage
	usage         json.RawMessage
	textMsg       string // message id the agent's text streams into
	thoughtMsg    string
	todoMsg       string
	tools         map[string]string // tool call id -> message id
	pending       map[string]*pendingRequest
	dirty         map[string]bool
	flushArmed    bool
	cancel        bool
	before        map[string]fileStamp
}

func newBotHub(root string, store *botStore, events *eventHub) *botHub {
	h := &botHub{store: store, events: events, root: root, runtimes: map[string]*botRuntime{}, live: map[string]*threadLive{}, turning: map[string]bool{}, queues: map[string][]string{}}
	go h.reapIdle()
	return h
}

func idleTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("VECTRABOT_BOT_IDLE")); err == nil && d > 0 {
		return d
	}
	return 15 * time.Minute
}

func (h *botHub) reapIdle() {
	for range time.Tick(time.Minute) {
		h.closeIdle(idleTimeout())
	}
}

// closeIdle stops bot processes with no running turn that sat unused for
// longer than idle. Threads keep their session id and reload later.
func (h *botHub) closeIdle(idle time.Duration) {
	h.store.mu.Lock()
	var stop []*acpConn
	for id, rt := range h.runtimes {
		if time.Since(rt.lastUsed) < idle {
			continue
		}
		if h.runtimeBusyLocked(rt) {
			if idle == 0 {
				rt.stale = true
			}
			continue
		}
		stop = append(stop, rt.conn)
		for _, tid := range rt.sessions {
			delete(h.live, tid)
		}
		delete(h.runtimes, id)
	}
	h.store.mu.Unlock()
	for _, c := range stop {
		c.close()
	}
}

// replaceableLocked reports a stale process that has no turn in flight.
func (h *botHub) replaceableLocked(rt *botRuntime) bool {
	return rt.stale && !h.runtimeBusyLocked(rt)
}

func (h *botHub) runtimeBusyLocked(rt *botRuntime) bool {
	for _, tid := range rt.sessions {
		if l := h.live[tid]; h.turning[tid] || (l != nil && len(l.pending) > 0) {
			return true
		}
	}
	return false
}

// agentCommand starts the CLI in ACP mode with the saved provider applied.
func (h *botHub) agentCommand(botID string) (*exec.Cmd, error) {
	var overlay []string
	for _, file := range providerEnvFiles() {
		overlay = append(overlay, readEnvFile(file)...)
	}
	if _, err := applyProvider(envMap(overlay)); err != nil {
		return nil, errors.New("could not apply the saved provider settings")
	}
	cmd := exec.Command(superchargeBinary(), "agent", "stdio")
	cmd.Dir = h.root
	cmd.Env = append(append(childEnv(overlay), "VECTRABOT_ENGINE=supercharge"), e2bAgentEnv(botID)...)
	cmd.Stderr = nil
	return cmd, nil
}

// e2bAgentEnv selects the E2B desktop agent and points it at the desktop
// controller for this bot. The controller is the browser sidecar started by
// the VM image; desktop observe and action tools talk to it, not to the
// person's own computer.
func e2bAgentEnv(botID string) []string {
	desk := ensureBotDesktop(botID)
	return []string{
		"SUPERCHARGE_RUNTIME_MODE=e2b",
		"SUPERCHARGE_AGENT=supercharge-e2b",
		"SUPERCHARGE_DESKTOP_PROTOCOL=1",
		"SUPERCHARGE_DISABLE_AUTOUPDATER=1",
		"DISPLAY=" + desk.display,
		"XAUTHORITY=" + desk.runtime + "/Xauthority",
		"DBUS_SESSION_BUS_ADDRESS=unix:path=" + desk.runtime + "/bus",
		"SUPERAGENT_BROWSER_SIDECAR_PORT=" + desk.port,
		"VECTRA_BROWSER_OWNER=" + desk.owner,
	}
}

type botDesktop struct {
	display string
	runtime string
	port    string
	owner   string
}

func desktopOwner(botID string) string {
	var cleaned strings.Builder
	for _, r := range botID {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	owner := cleaned.String()
	if owner == "" {
		owner = "main"
	}
	if len(owner) > 80 {
		owner = owner[:80]
	}
	return owner
}

// desktopForSlot matches the VM desktop starter: main is display :99 and
// sidecar 9224; every other bot gets the slot written under its runtime dir.
func desktopForSlot(botID string, slot int) botDesktop {
	if slot < 0 || slot > 300 {
		slot = 0
	}
	runtime := "/tmp/supercharge-browser"
	port := 9224
	if raw := strings.TrimSpace(os.Getenv("SUPERAGENT_BROWSER_SIDECAR_PORT")); slot == 0 && raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			port = n
		}
	}
	displayN := 99 + slot
	if slot > 0 {
		runtime = "/tmp/supercharge-browser/bots/" + botID
		port = 13000 + slot
	}
	if slot == 0 {
		if display := strings.TrimSpace(os.Getenv("VECTRABOT_DISPLAY")); display != "" {
			return botDesktop{display: display, runtime: runtime, port: strconv.Itoa(port), owner: botID}
		}
		if dir := strings.TrimSpace(os.Getenv("VECTRABOT_DESKTOP_RUNTIME")); dir != "" {
			runtime = dir
		}
	}
	return botDesktop{display: ":" + strconv.Itoa(displayN), runtime: runtime, port: strconv.Itoa(port), owner: botID}
}

// ensureBotDesktop starts the VM desktop controller for this bot when the
// image script is present, then reads the slot it assigned.
func ensureBotDesktop(botID string) botDesktop {
	owner := desktopOwner(botID)
	script := strings.TrimSpace(os.Getenv("VECTRABOT_DESKTOP_START"))
	if script == "" {
		script = "/opt/vectra/browser-runtime/start.sh"
	}
	if info, err := os.Stat(script); err == nil && !info.IsDir() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		cmd := exec.CommandContext(ctx, "bash", script)
		cmd.Env = append(os.Environ(), "SUPERAGENT_DESKTOP_BOT_ID="+owner, "SUPERCHARGE_RUNTIME_MODE=e2b")
		_ = cmd.Run()
		cancel()
	}
	slot := 0
	if owner != "main" {
		raw, err := os.ReadFile("/tmp/supercharge-browser/bots/" + owner + "/slot")
		if err == nil {
			if n, conv := strconv.Atoi(strings.TrimSpace(string(raw))); conv == nil && n >= 1 && n <= 300 {
				slot = n
			}
		}
	}
	desk := desktopForSlot(owner, slot)
	desk.owner = owner
	return desk
}

// runtimeFor returns the bot's running process, starting it when needed.
func (h *botHub) runtimeFor(ctx context.Context, botID string) (*botRuntime, error) {
	h.store.mu.Lock()
	rt := h.runtimes[botID]
	if rt != nil && rt.conn.alive() && !h.replaceableLocked(rt) {
		rt.lastUsed = time.Now()
		h.store.mu.Unlock()
		return rt, nil
	}
	var old *acpConn
	if rt != nil {
		old = rt.conn
		for _, tid := range rt.sessions {
			delete(h.live, tid)
		}
		delete(h.runtimes, botID)
	}
	h.store.mu.Unlock()
	if old != nil {
		old.close()
	}
	cmd, err := h.agentCommand(botID)
	if err != nil {
		return nil, err
	}
	rt = &botRuntime{botID: botID, sessions: map[string]string{}, lastUsed: time.Now()}
	conn, err := startACP(cmd, &botEvents{hub: h, rt: rt})
	if err != nil {
		return nil, fmt.Errorf("could not start supercharge: %w", err)
	}
	rt.conn = conn
	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	init, err := initializeACP(initCtx, conn)
	if err != nil {
		conn.close()
		return nil, fmt.Errorf("supercharge did not start: %w", err)
	}
	rt.init = init
	h.store.mu.Lock()
	h.runtimes[botID] = rt
	h.store.mu.Unlock()
	return rt, nil
}

type sessionReply struct {
	SessionID     string          `json:"sessionId"`
	Modes         json.RawMessage `json:"modes"`
	Models        json.RawMessage `json:"models"`
	ConfigOptions json.RawMessage `json:"configOptions"`
}

// openSession makes sure the thread has a live session: it reloads the saved
// one (resume) or creates a new one and applies the bot's settings.
func (h *botHub) openSession(ctx context.Context, threadID string) (*botRuntime, *threadLive, error) {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	h.store.mu.Lock()
	thread := h.store.threads[threadID]
	if thread == nil {
		h.store.mu.Unlock()
		return nil, nil, errNotFound
	}
	bot := h.store.botLocked(thread.BotID)
	if bot == nil {
		h.store.mu.Unlock()
		return nil, nil, errNotFound
	}
	botID, saved, settings, wantMode := bot.ID, thread.SessionID, bot.Settings, thread.Mode
	if wantMode == "plan" || wantMode == "ask" {
		wantMode = ""
	}
	settings.PermissionMode = sessionPermission(settings.PermissionMode)
	if rt := h.runtimes[botID]; rt != nil && rt.conn.alive() && !h.replaceableLocked(rt) {
		if l := h.live[threadID]; l != nil {
			rt.lastUsed = time.Now()
			h.store.mu.Unlock()
			return rt, l, nil
		}
	}
	h.store.mu.Unlock()

	rt, err := h.runtimeFor(ctx, botID)
	if err != nil {
		return nil, nil, err
	}
	live := &threadLive{tools: map[string]string{}, pending: map[string]*pendingRequest{}, dirty: map[string]bool{}}
	var reply sessionReply
	loaded := false
	if saved != "" {
		// Register first: load replays history as updates for this session.
		h.store.mu.Lock()
		rt.sessions[saved] = threadID
		h.live[threadID] = live
		h.store.mu.Unlock()
		err := rt.conn.call(ctx, "session/load", map[string]any{"sessionId": saved, "cwd": h.root, "mcpServers": []any{}}, &reply)
		if err == nil {
			reply.SessionID = saved
			loaded = true
		} else {
			log.Printf("session/load %s: %v", saved, err)
			h.store.mu.Lock()
			delete(rt.sessions, saved)
			delete(h.live, threadID)
			h.store.mu.Unlock()
		}
	}
	if !loaded {
		if err := rt.conn.call(ctx, "session/new", map[string]any{"cwd": h.root, "mcpServers": []any{}}, &reply); err != nil {
			return nil, nil, fmt.Errorf("could not open a session: %w", err)
		}
	}
	live.sessionID = reply.SessionID
	live.modes, live.models, live.configOptions = reply.Modes, reply.Models, reply.ConfigOptions
	var modes struct {
		CurrentModeID string `json:"currentModeId"`
	}
	_ = json.Unmarshal(reply.Modes, &modes)
	live.modeID = modes.CurrentModeID
	h.store.mu.Lock()
	rt.sessions[reply.SessionID] = threadID
	h.live[threadID] = live
	if t := h.store.threads[threadID]; t != nil && t.SessionID != reply.SessionID {
		t.SessionID = reply.SessionID
		t.Primed = false
		_ = h.store.saveLocked()
	}
	h.store.mu.Unlock()
	if !loaded {
		if settings.Model != "" {
			if err := h.setConfigOn(ctx, rt, live, "model", settings.Model); err != nil {
				// Keep going on the workspace default, and say so.
				h.store.mu.Lock()
				h.appendLocked(threadID, &message{Role: "bot", Kind: "notice", Error: true, Action: "model",
					Text: fmt.Sprintf("The bot's model %q is not available, so this thread uses the workspace default. Pick another model in the bot's settings.", settings.Model)})
				h.store.mu.Unlock()
			}
		}
		if settings.PermissionMode != "" {
			h.setConfigOn(ctx, rt, live, "permission_mode", settings.PermissionMode)
		}
	}
	if wantMode != "" && wantMode != live.modeID {
		if err := rt.conn.call(ctx, "session/set_mode", map[string]any{"sessionId": live.sessionID, "modeId": wantMode}, nil); err == nil {
			h.store.mu.Lock()
			live.modeID = wantMode
			h.store.mu.Unlock()
		}
	}
	h.publishSession(threadID)
	return rt, live, nil
}

func (h *botHub) setConfigOn(ctx context.Context, rt *botRuntime, live *threadLive, id, value string) error {
	var out struct {
		ConfigOptions json.RawMessage `json:"configOptions"`
	}
	err := rt.conn.call(ctx, "session/set_config_option", map[string]any{"sessionId": live.sessionID, "configId": id, "value": value}, &out)
	if err != nil {
		log.Printf("set_config_option %s: %v", id, err)
		return err
	}
	h.store.mu.Lock()
	if len(out.ConfigOptions) > 0 {
		live.configOptions = out.ConfigOptions
	}
	h.store.mu.Unlock()
	return nil
}

// sessionState is what the apps show around the composer.
func (h *botHub) sessionStateLocked(threadID string) map[string]any {
	state := map[string]any{"threadId": threadID, "open": false, "busy": h.turning[threadID], "queued": len(h.queues[threadID])}
	t := h.store.threads[threadID]
	if t != nil {
		state["mode"] = t.Mode
	}
	l := h.live[threadID]
	if l == nil {
		return state
	}
	state["open"] = true
	if l.modeID != "" {
		state["mode"] = l.modeID
	}
	for key, raw := range map[string]json.RawMessage{"modes": l.modes, "models": l.models, "configOptions": l.configOptions, "commands": l.commands, "usage": l.usage} {
		if len(raw) > 0 {
			state[key] = raw
		}
	}
	var pending []string
	for id := range l.pending {
		pending = append(pending, id)
	}
	state["pending"] = pending
	return state
}

func (h *botHub) publishSession(threadID string) {
	h.store.mu.Lock()
	state := h.sessionStateLocked(threadID)
	botID := ""
	if t := h.store.threads[threadID]; t != nil {
		botID = t.BotID
	}
	h.store.mu.Unlock()
	h.events.publish("session", map[string]any{"threadId": threadID, "botId": botID, "session": state})
}

// appendLocked adds a message to a thread and announces it.
func (h *botHub) appendLocked(threadID string, m *message) {
	m.ThreadID = threadID
	if m.ID == "" {
		m.ID = newID("msg_")
	}
	if m.At == 0 {
		m.At = nowMillis()
	}
	h.store.messages[threadID] = append(h.store.messagesLocked(threadID), m)
	if t := h.store.threads[threadID]; t != nil {
		t.UpdatedAt = m.At
	}
	h.emitLocked(threadID, m)
}

func (h *botHub) findLocked(threadID, id string) *message {
	list := h.store.messagesLocked(threadID)
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].ID == id {
			return list[i]
		}
	}
	return nil
}

func (h *botHub) emitLocked(threadID string, m *message) {
	botID := ""
	if t := h.store.threads[threadID]; t != nil {
		botID = t.BotID
	}
	h.events.publish("message", map[string]any{"threadId": threadID, "botId": botID, "message": cloneMessage(m)})
}

// touchLocked marks a streaming message changed; frames go out at most every
// 100ms per thread so long replies do not flood the apps.
func (h *botHub) touchLocked(threadID string, l *threadLive, id string) {
	l.dirty[id] = true
	if l.flushArmed {
		return
	}
	l.flushArmed = true
	time.AfterFunc(100*time.Millisecond, func() {
		h.store.mu.Lock()
		defer h.store.mu.Unlock()
		h.flushLocked(threadID, l)
	})
}

func (h *botHub) flushLocked(threadID string, l *threadLive) {
	l.flushArmed = false
	for id := range l.dirty {
		if m := h.findLocked(threadID, id); m != nil {
			h.emitLocked(threadID, m)
		}
	}
	l.dirty = map[string]bool{}
}

// send starts a turn, or queues the text when the thread is already busy.
func (h *botHub) send(ctx context.Context, threadID, text string) (queued bool, err error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return false, errors.New("message required")
	}
	h.store.mu.Lock()
	thread := h.store.threads[threadID]
	if thread == nil {
		h.store.mu.Unlock()
		return false, errNotFound
	}
	if h.turning[threadID] {
		h.queues[threadID] = append(h.queues[threadID], text)
		h.store.mu.Unlock()
		h.publishSession(threadID)
		return true, nil
	}
	h.appendLocked(threadID, &message{Role: "user", Kind: "text", Text: text})
	if thread.Title == "" {
		thread.Title = shortTitle(text)
	}
	_ = h.store.saveThreadLocked(threadID)
	_ = h.store.saveLocked()
	h.turning[threadID] = true
	h.store.mu.Unlock()
	go h.runTurn(threadID, text)
	return false, nil
}

func shortTitle(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) > 60 {
		text = string([]rune(text)[:60]) + "…"
	}
	return text
}

func (h *botHub) runTurn(threadID, text string) {
	ctx, cancelTurn := context.WithTimeout(context.Background(), botTurnTimeout())
	defer cancelTurn()
	h.publishTurn(threadID, true, "", "")
	rt, live, err := h.openSession(ctx, threadID)
	if err != nil {
		h.finishTurn(threadID, nil, "", err)
		return
	}
	h.store.mu.Lock()
	live.cancel = false
	live.textMsg, live.thoughtMsg, live.todoMsg = "", "", ""
	live.before = snapshotTree(h.root)
	thread := h.store.threads[threadID]
	bot := h.store.botLocked(thread.BotID)
	blocks := []map[string]any{}
	if !thread.Primed {
		if intro := botIntro(bot, h.store.botFilesDir(bot.ID)); intro != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": intro})
		}
		thread.Primed = true
		_ = h.store.saveLocked()
	}
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	sessionID := live.sessionID
	h.store.mu.Unlock()
	h.publishSession(threadID)

	var result struct {
		StopReason string `json:"stopReason"`
	}
	err = rt.conn.call(ctx, "session/prompt", map[string]any{"sessionId": sessionID, "prompt": blocks}, &result)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		_ = rt.conn.notifyAgent("session/cancel", map[string]string{"sessionId": sessionID})
		err = errors.New("the turn timed out")
	}
	h.finishTurn(threadID, live, result.StopReason, err)
}

// botIntro is the first block of a new session: who the bot is.
func botIntro(bot *botRecord, filesDir string) string {
	if bot == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("<bot_profile>\n")
	fmt.Fprintf(&b, "Your name is %s.", bot.Name)
	if bot.Title != "" {
		fmt.Fprintf(&b, " Role: %s.", bot.Title)
	}
	b.WriteString("\n")
	if bot.Description != "" {
		fmt.Fprintf(&b, "About you: %s\n", bot.Description)
	}
	if bot.Instructions != "" {
		b.WriteString("Follow these standing instructions from the owner for this whole conversation:\n")
		b.WriteString(bot.Instructions)
		b.WriteString("\n")
	}
	if bot.Main {
		b.WriteString("You are the main bot, Chief. You can create other bots that show up in this workspace.\n")
		b.WriteString("Create one by reading the tool token and calling the local bot service. Do not print the token.\n")
		fmt.Fprintf(&b, "TOKEN=$(cat %s)\n", toolTokenPath())
		b.WriteString("curl -sS -H \"Content-Type: application/json\" -H \"X-Vectrabot-Tool: $TOKEN\" ")
		b.WriteString("-d '{\"name\":\"Name\",\"instructions\":\"What this bot does\"}' http://127.0.0.1:8790/v1/bots/tool\n")
		b.WriteString("List bots with the same header and a GET to that URL. Only you create bots.\n")
	}
	if entries, err := os.ReadDir(filesDir); err == nil && len(entries) > 0 {
		fmt.Fprintf(&b, "Reference files the owner gave you are in %s.\n", filesDir)
	}
	b.WriteString("Do not mention this profile block unless asked.\n</bot_profile>")
	return b.String()
}

func (h *botHub) finishTurn(threadID string, live *threadLive, stopReason string, err error) {
	h.store.mu.Lock()
	var next string
	if live == nil {
		live = h.live[threadID]
	}
	if live != nil {
		h.flushLocked(threadID, live)
		if live.before != nil {
			if changes := diffTree(live.before, snapshotTree(h.root)); len(changes) > 0 {
				h.appendLocked(threadID, &message{Role: "bot", Kind: "changes", Changes: changes})
			}
			live.before = nil
		}
		for _, p := range live.pending {
			close(p.answer)
		}
		live.pending = map[string]*pendingRequest{}
		live.textMsg, live.thoughtMsg = "", ""
		if live.cancel {
			delete(h.queues, threadID)
		}
	}
	delete(h.turning, threadID)
	// A provider change arrived mid-turn: replace the process now it is idle.
	if t := h.store.threads[threadID]; t != nil {
		if rt := h.runtimes[t.BotID]; rt != nil && h.replaceableLocked(rt) {
			for _, tid := range rt.sessions {
				delete(h.live, tid)
			}
			delete(h.runtimes, t.BotID)
			go rt.conn.close()
		}
	}
	if q := h.queues[threadID]; err == nil && len(q) > 0 {
		next = q[0]
		h.queues[threadID] = q[1:]
	}
	var rpc *rpcError
	if errors.As(err, &rpc) && rpc.Code == -32602 && strings.Contains(rpc.Error(), "unknown session") {
		if t := h.store.threads[threadID]; t != nil {
			t.SessionID, t.Primed = "", false
		}
		delete(h.live, threadID)
	}
	if err != nil {
		h.appendLocked(threadID, turnErrorMessage(err))
	} else if stopReason == "cancelled" {
		h.appendLocked(threadID, &message{Role: "bot", Kind: "notice", Text: "Stopped."})
	} else if stopReason == "max_tokens" || stopReason == "max_turn_requests" || stopReason == "refusal" {
		h.appendLocked(threadID, &message{Role: "bot", Kind: "notice", Text: "Turn ended: " + strings.ReplaceAll(stopReason, "_", " ")})
	}
	_ = h.store.saveThreadLocked(threadID)
	_ = h.store.saveLocked()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	h.store.mu.Unlock()
	h.publishTurn(threadID, false, stopReason, errText)
	h.publishSession(threadID)
	if next != "" {
		_, _ = h.send(context.Background(), threadID, next)
	}
}

func (h *botHub) publishTurn(threadID string, busy bool, stopReason, errText string) {
	h.store.mu.Lock()
	botID := ""
	if t := h.store.threads[threadID]; t != nil {
		botID = t.BotID
	}
	h.store.mu.Unlock()
	payload := map[string]any{"threadId": threadID, "botId": botID, "busy": busy}
	if stopReason != "" {
		payload["stopReason"] = stopReason
	}
	if errText != "" {
		payload["error"] = errText
	}
	h.events.publish("turn", payload)
}

// interrupt cancels the running turn and anything the agent was waiting on.
func (h *botHub) interrupt(threadID string) error {
	h.store.mu.Lock()
	l := h.live[threadID]
	t := h.store.threads[threadID]
	if l == nil || t == nil {
		h.store.mu.Unlock()
		return nil
	}
	rt := h.runtimes[t.BotID]
	l.cancel = true
	delete(h.queues, threadID)
	for id, p := range l.pending {
		close(p.answer)
		delete(l.pending, id)
	}
	sessionID := l.sessionID
	h.store.mu.Unlock()
	if rt == nil {
		return nil
	}
	return rt.conn.notifyAgent("session/cancel", map[string]string{"sessionId": sessionID})
}

// respond delivers a person's answer to a pending agent request.
func (h *botHub) respond(threadID, requestID string, answer json.RawMessage) error {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	l := h.live[threadID]
	if l == nil {
		return errNotFound
	}
	p := l.pending[requestID]
	if p == nil {
		return errNotFound
	}
	delete(l.pending, requestID)
	p.answer <- answer
	close(p.answer)
	return nil
}

// setMode switches the thread's session mode (default, plan, ask).
func (h *botHub) setMode(ctx context.Context, threadID, modeID string) error {
	h.store.mu.Lock()
	t := h.store.threads[threadID]
	if t == nil {
		h.store.mu.Unlock()
		return errNotFound
	}
	t.Mode = modeID
	_ = h.store.saveLocked()
	l := h.live[threadID]
	rt := h.runtimes[t.BotID]
	h.store.mu.Unlock()
	if l != nil && rt != nil {
		if err := rt.conn.call(ctx, "session/set_mode", map[string]any{"sessionId": l.sessionID, "modeId": modeID}, nil); err != nil {
			return err
		}
		h.store.mu.Lock()
		l.modeID = modeID
		h.store.mu.Unlock()
	}
	h.publishSession(threadID)
	return nil
}

// setOption changes a session config option (model, permission_mode, ...),
// opening the session if needed.
func (h *botHub) setOption(ctx context.Context, threadID, id, value string) error {
	rt, live, err := h.openSession(ctx, threadID)
	if err != nil {
		return err
	}
	if err := h.setConfigOn(ctx, rt, live, id, value); err != nil {
		return err
	}
	h.publishSession(threadID)
	return nil
}

// stopBot ends a bot's process (after delete or a settings change).
func (h *botHub) stopBot(botID string) {
	h.store.mu.Lock()
	rt := h.runtimes[botID]
	if rt != nil {
		for _, tid := range rt.sessions {
			delete(h.live, tid)
		}
		delete(h.runtimes, botID)
	}
	h.store.mu.Unlock()
	if rt != nil {
		rt.conn.close()
	}
}

// restartIdle closes every bot process with no running turn, so the next
// turn starts fresh with a changed provider config.
func (h *botHub) restartIdle() { h.closeIdle(0) }

// botTurnTimeout is longer than a one-shot turn: a turn may wait on a person
// answering a question or approving a plan.
func botTurnTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("VECTRABOT_BOT_TURN_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return time.Hour
}

// turnErrorMessage turns an ACP error into a notice the apps can act on.
// Codes follow the CLI: -32000 is a 401 (sign in again), -32003 a 429,
// -32050 a missing model, -32603 overload/5xx/403 (403 is not re-auth).
func turnErrorMessage(err error) *message {
	m := &message{Role: "bot", Kind: "notice", Error: true, Text: err.Error(), Action: "retry"}
	var rpc *rpcError
	if !errors.As(err, &rpc) {
		if strings.Contains(err.Error(), "process stopped") {
			m.Text = "The agent stopped unexpectedly. The next message resumes this thread."
		}
		return m
	}
	m.ErrorCode = rpc.Code
	detail := rpc.Error()
	switch rpc.Code {
	case -32000:
		m.Text, m.Action = "The provider rejected the credentials (401). Sign in again or paste a new API key.", "providers"
	case -32003:
		m.Text = "Rate limited by the provider. Wait a moment and retry."
		if strings.Contains(detail, "free-usage-exhausted") {
			m.Text, m.Action = "The free usage for this provider is used up. Switch provider or wait for the reset.", "providers"
		}
	case -32050:
		m.Text, m.Action = rpc.Message, "model"
		if m.Text == "" {
			m.Text = "No model is configured. Connect a provider."
		}
	case -32002:
		m.Text, m.Action = "The provider could not find that model or URL (404). Check the model.", "model"
	case -32602:
		m.Text = "The request was rejected: " + clip(detail, 300)
		if strings.Contains(detail, "unknown session") {
			m.Text = "This thread's session was not found. The next message starts a fresh session."
		}
	case -32603:
		m.Text = clip(detail, 300)
		if unauthorizedDetail(detail) {
			// The released CLI reports some provider 401s as internal errors.
			m.Text, m.Action = "The provider rejected the credentials (401). Sign in again or paste a new API key.", "providers"
		} else if strings.Contains(strings.ToLower(detail), "overloaded") {
			m.Text = "The model is temporarily overloaded. Try again in a moment."
		}
	default:
		m.Text = clip(detail, 300)
	}
	return m
}

// unauthorizedDetail spots a provider 401 inside a generic error message.
// A 403 is not re-auth, so it is never matched.
func unauthorizedDetail(detail string) bool {
	d := strings.ToLower(detail)
	return strings.Contains(d, "unauthorized (401)") || strings.Contains(d, "http 401") || strings.Contains(d, "status 401")
}
