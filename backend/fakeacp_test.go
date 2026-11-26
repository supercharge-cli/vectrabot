package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
)

// TestMain doubles as a scripted ACP agent when the host under test starts
// this binary as its "supercharge agent stdio".
func TestMain(m *testing.M) {
	if os.Getenv("VECTRABOT_FAKE_ACP") == "1" {
		fakeAgent()
		return
	}
	os.Exit(m.Run())
}

func fakeAgent() {
	var mu sync.Mutex
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		raw, _ := json.Marshal(v)
		mu.Lock()
		out.Write(append(raw, '\n'))
		out.Flush()
		mu.Unlock()
	}
	update := func(sid string, u map[string]any) {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": u}})
	}
	chunk := func(sid, text string) {
		update(sid, map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": text}})
	}
	var waitMu sync.Mutex
	waiting := map[string]chan json.RawMessage{}
	cancelled := map[string]chan struct{}{}
	next := 1000
	ask := func(method string, params any) json.RawMessage {
		waitMu.Lock()
		next++
		id := fmt.Sprint(next)
		ch := make(chan json.RawMessage, 1)
		waiting[id] = ch
		waitMu.Unlock()
		send(map[string]any{"jsonrpc": "2.0", "id": next, "method": method, "params": params})
		return <-ch
	}
	sessions := 0
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for in.Scan() {
		var msg rpcMessage
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			continue
		}
		if msg.Method == "" {
			waitMu.Lock()
			ch := waiting[string(msg.ID)]
			delete(waiting, string(msg.ID))
			waitMu.Unlock()
			if ch != nil {
				ch <- msg.Result
			}
			continue
		}
		var p map[string]any
		_ = json.Unmarshal(msg.Params, &p)
		sid, _ := p["sessionId"].(string)
		reply := func(result any) { send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result}) }
		modes := map[string]any{"currentModeId": "default", "availableModes": []map[string]string{{"id": "default", "name": "Default"}, {"id": "plan", "name": "Plan"}, {"id": "ask", "name": "Ask"}}}
		switch msg.Method {
		case "initialize":
			reply(map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": true}, "modelState": map[string]any{"currentModelId": "m1", "availableModels": []map[string]string{{"modelId": "m1", "name": "Model One"}}}})
		case "session/new":
			sessions++
			reply(map[string]any{"sessionId": fmt.Sprintf("sess-%d-%d", os.Getpid(), sessions), "modes": modes, "configOptions": []map[string]any{{"id": "permission_mode", "currentValue": "ask"}}})
		case "session/load":
			update(sid, map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]string{"type": "text", "text": "old"}, "_meta": map[string]any{"isReplay": true}})
			reply(map[string]any{"modes": modes})
		case "session/set_mode":
			update(sid, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": p["modeId"]})
			reply(map[string]any{})
		case "session/set_config_option":
			reply(map[string]any{"configOptions": []map[string]any{{"id": p["configId"], "currentValue": p["value"]}}})
		case "session/cancel":
			waitMu.Lock()
			if ch := cancelled[sid]; ch != nil {
				close(ch)
				delete(cancelled, sid)
			}
			waitMu.Unlock()
		case "_supercharge/setup/status":
			reply(map[string]any{"configured": true, "hasApiKey": true, "apiKey": "sk-should-never-leak-1234", "providers": []map[string]any{{"id": "openai", "auth": "api_key"}}})
		case "_supercharge/setup/save":
			reply(map[string]any{"saved": true})
		case "session/prompt":
			blocks, _ := p["prompt"].([]any)
			last := blocks[len(blocks)-1].(map[string]any)["text"].(string)
			primed := strings.Contains(blocks[0].(map[string]any)["text"].(string), "<bot_profile>")
			id := msg.ID
			go func() {
				done := func(reason string) {
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]string{"stopReason": reason}})
				}
				switch last {
				case "ask":
					a := ask("_x.ai/ask_user_question", map[string]any{"sessionId": sid, "toolCallId": "t1", "questions": []map[string]any{{"question": "Color?", "options": []map[string]string{{"label": "Red"}, {"label": "Blue"}}, "multiSelect": false}}})
					chunk(sid, "answer: "+string(a))
				case "plan":
					a := ask("_x.ai/exit_plan_mode", map[string]any{"sessionId": sid, "toolCallId": "t2", "planContent": "1. do it"})
					chunk(sid, "plan: "+string(a))
				case "perm":
					a := ask("session/request_permission", map[string]any{"sessionId": sid, "toolCall": map[string]any{"toolCallId": "t3", "title": "Run ls"}, "options": []map[string]string{{"optionId": "allow-once", "kind": "allow_once", "name": "Allow"}, {"optionId": "reject-once", "kind": "reject_once", "name": "Reject"}}})
					chunk(sid, "perm: "+string(a))
				case "pid":
					chunk(sid, fmt.Sprintf("pid=%d", os.Getpid()))
				case "fail401internal":
					send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "Internal error", "data": "Unauthorized (401) from http://127.0.0.1:1/v1/chat/completions: invalid_api_key"}})
					return
				case "fail403":
					send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "Internal error", "data": "Forbidden (403) from http://127.0.0.1:1/v1/chat/completions"}})
					return
				case "fail401":
					send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": "Authentication required"}})
					return
				case "slow":
					ch := make(chan struct{})
					waitMu.Lock()
					cancelled[sid] = ch
					waitMu.Unlock()
					chunk(sid, "working")
					<-ch
					done("cancelled")
					return
				case "edit":
					update(sid, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "e1", "title": "Write notes.md", "kind": "edit", "status": "pending", "rawInput": map[string]string{"path": "notes.md"}, "_meta": map[string]any{"x.ai/tool": map[string]string{"name": "write"}}})
					_ = os.WriteFile("notes.md", []byte("hi\n"), 0o644)
					update(sid, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "e1", "status": "completed", "content": []map[string]any{{"type": "diff", "path": "notes.md", "oldText": nil, "newText": "hi\n"}}, "locations": []map[string]string{{"path": "notes.md"}}})
					chunk(sid, "edited")
				default:
					update(sid, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": []map[string]string{{"name": "compact", "description": "Compact"}}})
					if primed {
						chunk(sid, "[primed] ")
					}
					chunk(sid, "Hello")
					chunk(sid, " world")
				}
				done("end_turn")
			}()
		default:
			send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32601, "message": "Method not found"}})
		}
	}
}
