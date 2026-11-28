package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// botEvents turns what one bot process sends into thread messages.
type botEvents struct {
	hub *botHub
	rt  *botRuntime
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Path     string          `json:"path"`
	OldText  *string         `json:"oldText"`
	NewText  string          `json:"newText"`
	Content  json.RawMessage `json:"content"`
	Data     string          `json:"data"`
	MimeType string          `json:"mimeType"`
}

type sessionUpdate struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       json.RawMessage `json:"content"`
	ToolCallID    string          `json:"toolCallId"`
	Title         *string         `json:"title"`
	Kind          string          `json:"kind"`
	Status        string          `json:"status"`
	RawInput      json.RawMessage `json:"rawInput"`
	RawOutput     json.RawMessage `json:"rawOutput"`
	Locations     []struct {
		Path string `json:"path"`
	} `json:"locations"`
	Entries           []planEntry                `json:"entries"`
	AvailableCommands json.RawMessage            `json:"availableCommands"`
	CurrentModeID     string                     `json:"currentModeId"`
	ConfigOptions     json.RawMessage            `json:"configOptions"`
	Meta              map[string]json.RawMessage `json:"_meta"`
}

func (e *botEvents) threadFor(sessionID string) (string, *threadLive) {
	tid := e.rt.sessions[sessionID]
	if tid == "" {
		return "", nil
	}
	return tid, e.hub.live[tid]
}

func (e *botEvents) notify(method string, params json.RawMessage) {
	var head struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	_ = json.Unmarshal(params, &head)
	h := e.hub
	switch method {
	case "session/update":
		var u sessionUpdate
		if json.Unmarshal(head.Update, &u) != nil {
			return
		}
		if raw, ok := u.Meta["isReplay"]; ok && string(raw) == "true" {
			return // history we already keep
		}
		h.store.mu.Lock()
		tid, live := e.threadFor(head.SessionID)
		if live == nil {
			h.store.mu.Unlock()
			return
		}
		publish := h.applyUpdateLocked(tid, live, u)
		h.store.mu.Unlock()
		if publish {
			h.publishSession(tid)
		}
	case "_supercharge/session/context_usage", "_x.ai/session/update":
		h.store.mu.Lock()
		tid, live := e.threadFor(head.SessionID)
		if live != nil {
			if method == "_supercharge/session/context_usage" {
				live.usage = append(json.RawMessage(nil), params...)
			}
		}
		h.store.mu.Unlock()
		if live != nil && method == "_supercharge/session/context_usage" {
			h.publishSession(tid)
		}
	}
}

func (h *botHub) saveChatImage(data, mime string) (chatImage, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data))
	if err != nil || len(raw) == 0 || len(raw) > 6<<20 {
		return chatImage{}, false
	}
	ext := ""
	switch mime {
	case "image/png":
		ext = "png"
	case "image/jpeg":
		ext = "jpg"
	case "image/webp":
		ext = "webp"
	case "image/gif":
		ext = "gif"
	default:
		return chatImage{}, false
	}
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return chatImage{}, false
	}
	id := hex.EncodeToString(buf)
	dir := filepath.Join(h.root, ".vectrabot", "chat-images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return chatImage{}, false
	}
	if err := os.WriteFile(filepath.Join(dir, id+"."+ext), raw, 0o600); err != nil {
		return chatImage{}, false
	}
	return chatImage{ID: id, Mime: mime}, true
}

func (h *botHub) serveChatImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if len(id) != 16 || strings.Trim(id, "0123456789abcdef") != "" {
		notFound(w, "image")
		return
	}
	dir := filepath.Join(h.root, ".vectrabot", "chat-images")
	matches, _ := filepath.Glob(filepath.Join(dir, id+".*"))
	if len(matches) != 1 {
		notFound(w, "image")
		return
	}
	switch filepath.Ext(matches[0]) {
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".webp":
		w.Header().Set("Content-Type", "image/webp")
	case ".gif":
		w.Header().Set("Content-Type", "image/gif")
	default:
		notFound(w, "image")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, matches[0])
}

func textOf(raw json.RawMessage) string {
	var block contentBlock
	if json.Unmarshal(raw, &block) == nil && block.Type == "text" {
		return block.Text
	}
	return ""
}

func compactJSON(raw json.RawMessage, max int) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return clip(s, max)
	}
	return clip(string(raw), max)
}

func clip(s string, max int) string {
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// applyUpdateLocked folds one session update into the thread. It returns true
// when session-level state (mode, commands, options) changed.
func (h *botHub) applyUpdateLocked(tid string, live *threadLive, u sessionUpdate) bool {
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk":
		text := textOf(u.Content)
		if text == "" {
			return false
		}
		slot, kind := &live.textMsg, "text"
		if u.SessionUpdate == "agent_thought_chunk" {
			slot, kind = &live.thoughtMsg, "thought"
		}
		if m := h.findLocked(tid, *slot); m != nil {
			m.Text = publicizeLinks(m.Text + text)
			h.touchLocked(tid, live, m.ID)
			return false
		}
		m := &message{Role: "bot", Kind: kind, Text: publicizeLinks(text)}
		h.appendLocked(tid, m)
		*slot = m.ID
		if kind == "text" {
			live.thoughtMsg = ""
		}
	case "tool_call", "tool_call_update":
		id := live.tools[u.ToolCallID]
		m := h.findLocked(tid, id)
		if m == nil {
			// Text after a tool call starts a new bubble below it.
			live.textMsg, live.thoughtMsg = "", ""
			m = &message{Role: "bot", Kind: "tool", Tool: &toolCard{ID: u.ToolCallID, Status: "pending"}}
			h.appendLocked(tid, m)
			live.tools[u.ToolCallID] = m.ID
		}
		card := m.Tool
		if u.Title != nil && *u.Title != "" {
			card.Title = *u.Title
		}
		if u.Kind != "" {
			card.Kind = u.Kind
		}
		if u.Status != "" {
			card.Status = u.Status
		}
		if raw, ok := u.Meta["x.ai/tool"]; ok {
			var meta struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &meta) == nil && meta.Name != "" {
				card.Name = meta.Name
			}
		}
		if in := compactJSON(u.RawInput, 4000); in != "" && in != "{}" {
			card.Input = in
		}
		for _, loc := range u.Locations {
			if loc.Path != "" && !contains(card.Locations, loc.Path) {
				card.Locations = append(card.Locations, loc.Path)
			}
		}
		if len(u.Content) > 0 && string(u.Content) != "null" {
			var blocks []contentBlock
			if json.Unmarshal(u.Content, &blocks) == nil {
				var out []string
				for _, b := range blocks {
					switch b.Type {
					case "diff":
						old := ""
						if b.OldText != nil {
							old = *b.OldText
						}
						card.Diffs = upsertDiff(card.Diffs, toolDiff{Path: b.Path, OldText: clip(old, 60000), NewText: clip(b.NewText, 60000)})
					case "content":
						if t := textOf(b.Content); t != "" {
							out = append(out, t)
						}
						var inner contentBlock
						if json.Unmarshal(b.Content, &inner) == nil && inner.Type == "image" {
							if img, ok := h.saveChatImage(inner.Data, inner.MimeType); ok {
								card.Images = append(card.Images, img)
							}
						}
					case "image":
						if img, ok := h.saveChatImage(b.Data, b.MimeType); ok {
							card.Images = append(card.Images, img)
						}
					case "terminal":
						out = append(out, "(terminal output)")
					}
				}
				if len(out) > 0 {
					card.Output = publicizeLinks(clip(strings.Join(out, "\n"), 20000))
					if card.Status == "" || card.Status == "pending" {
						card.Status = "completed"
					}
				}
			}
		}
		if card.Output == "" && (card.Status == "completed" || card.Status == "failed") {
			if o := compactJSON(u.RawOutput, 20000); o != "" && o != "{}" {
				card.Output = publicizeLinks(o)
			}
		}
		if card.Status == "completed" || card.Status == "failed" {
			h.emitLocked(tid, m)
		} else {
			h.touchLocked(tid, live, m.ID)
		}
	case "plan":
		m := h.findLocked(tid, live.todoMsg)
		if m == nil {
			m = &message{Role: "bot", Kind: "todo"}
			h.appendLocked(tid, m)
			live.todoMsg = m.ID
			live.textMsg = ""
		}
		m.Entries = u.Entries
		h.emitLocked(tid, m)
	case "available_commands_update":
		live.commands = u.AvailableCommands
		return true
	case "current_mode_update":
		live.modeID = u.CurrentModeID
		if t := h.store.threads[tid]; t != nil {
			t.Mode = u.CurrentModeID
		}
		return true
	case "config_option_update":
		if len(u.ConfigOptions) > 0 {
			live.configOptions = u.ConfigOptions
		}
		return true
	case "session_info_update":
		// The agent titles a session from its first prompt; skip titles taken
		// from the bot profile block and keep the user's own words instead.
		if t := h.store.threads[tid]; t != nil && u.Title != nil && strings.TrimSpace(*u.Title) != "" && !t.Renamed && !strings.Contains(*u.Title, "bot_profile") {
			t.Title = strings.TrimSpace(*u.Title)
			_ = h.store.saveLocked()
			h.events.publish("thread", map[string]any{"botId": t.BotID, "threadId": t.ID, "thread": *t})
		}
	}
	return false
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func upsertDiff(list []toolDiff, d toolDiff) []toolDiff {
	for i := range list {
		if list[i].Path == d.Path {
			list[i] = d
			return list
		}
	}
	return append(list, d)
}

// request answers what the agent asks the client. Permission prompts,
// questions and plan approvals become cards the apps answer through
// POST /api/threads/:id/respond.
func (e *botEvents) request(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/request_permission":
		var p struct {
			SessionID string `json:"sessionId"`
			ToolCall  struct {
				ToolCallID string  `json:"toolCallId"`
				Title      *string `json:"title"`
			} `json:"toolCall"`
			Options []permissionOption `json:"options"`
		}
		_ = json.Unmarshal(params, &p)
		title := "Allow this action?"
		if p.ToolCall.Title != nil && *p.ToolCall.Title != "" {
			title = *p.ToolCall.Title
		}
		answer, ok := e.ask(ctx, p.SessionID, &requestCard{Type: "permission", Title: title, Options: p.Options})
		var a struct {
			OptionID string `json:"optionId"`
		}
		if !ok || json.Unmarshal(answer, &a) != nil || a.OptionID == "" {
			return map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}, nil
		}
		return map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": a.OptionID}}, nil
	case "_x.ai/ask_user_question", "x.ai/ask_user_question":
		var p struct {
			SessionID string         `json:"sessionId"`
			Questions []questionItem `json:"questions"`
			Mode      string         `json:"mode"`
		}
		_ = json.Unmarshal(params, &p)
		answer, ok := e.ask(ctx, p.SessionID, &requestCard{Type: "question", Title: "Questions", Questions: p.Questions, Mode: p.Mode})
		if !ok {
			return map[string]any{"outcome": "cancelled"}, nil
		}
		var a struct {
			Outcome     string              `json:"outcome"`
			Answers     map[string][]string `json:"answers"`
			Annotations json.RawMessage     `json:"annotations"`
		}
		_ = json.Unmarshal(answer, &a)
		switch a.Outcome {
		case "chat_about_this", "skip_interview":
			// Partial answers are one string per question.
			partial := map[string]string{}
			for q, v := range a.Answers {
				partial[q] = strings.Join(v, ", ")
			}
			return map[string]any{"outcome": a.Outcome, "partial_answers": partial}, nil
		case "cancelled":
			return map[string]any{"outcome": "cancelled"}, nil
		}
		reply := map[string]any{"outcome": "accepted", "answers": nonNilAnswers(a.Answers)}
		if len(a.Annotations) > 0 && string(a.Annotations) != "null" {
			reply["annotations"] = a.Annotations
		}
		return reply, nil
	case "_x.ai/exit_plan_mode", "x.ai/exit_plan_mode":
		var p struct {
			SessionID   string `json:"sessionId"`
			PlanContent string `json:"planContent"`
		}
		_ = json.Unmarshal(params, &p)
		answer, ok := e.ask(ctx, p.SessionID, &requestCard{Type: "plan", Title: "Plan ready for review", Plan: p.PlanContent})
		if !ok {
			return map[string]any{"outcome": "abandoned"}, nil
		}
		var a struct {
			Outcome  string `json:"outcome"`
			Feedback string `json:"feedback"`
		}
		_ = json.Unmarshal(answer, &a)
		switch a.Outcome {
		case "approved":
			return map[string]any{"outcome": "approved"}, nil
		case "abandoned":
			return map[string]any{"outcome": "abandoned"}, nil
		default:
			reply := map[string]any{"outcome": "cancelled"}
			if strings.TrimSpace(a.Feedback) != "" {
				reply["feedback"] = strings.TrimSpace(a.Feedback)
			}
			return reply, nil
		}
	case "x.ai/mcp/elicit", "_x.ai/mcp/elicit":
		return map[string]any{"action": "decline"}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "Method not found"}
}

func nonNilAnswers(a map[string][]string) map[string][]string {
	if a == nil {
		return map[string][]string{}
	}
	return a
}

// ask posts a request card and blocks until it is answered, the turn is
// cancelled, or the process stops.
func (e *botEvents) ask(ctx context.Context, sessionID string, card *requestCard) (json.RawMessage, bool) {
	h := e.hub
	h.store.mu.Lock()
	tid, live := e.threadFor(sessionID)
	if live == nil {
		h.store.mu.Unlock()
		return nil, false
	}
	card.ID = newID("req_")
	card.State = "pending"
	m := &message{Role: "bot", Kind: "request", Request: card}
	live.textMsg, live.thoughtMsg = "", ""
	h.appendLocked(tid, m)
	p := &pendingRequest{threadID: tid, messageID: m.ID, answer: make(chan json.RawMessage, 1)}
	live.pending[card.ID] = p
	_ = h.store.saveThreadLocked(tid)
	h.store.mu.Unlock()
	h.publishSession(tid)

	var answer json.RawMessage
	ok := false
	select {
	case a, open := <-p.answer:
		answer, ok = a, open && a != nil
	case <-ctx.Done():
	}
	h.store.mu.Lock()
	delete(live.pending, card.ID)
	if msg := h.findLocked(tid, m.ID); msg != nil {
		if ok {
			msg.Request.State = "answered"
			var shown any
			_ = json.Unmarshal(answer, &shown)
			msg.Request.Answer = shown
		} else {
			msg.Request.State = "cancelled"
		}
		h.emitLocked(tid, msg)
	}
	_ = h.store.saveThreadLocked(tid)
	h.store.mu.Unlock()
	h.publishSession(tid)
	return answer, ok
}
