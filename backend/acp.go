package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// acpConn speaks the Agent Client Protocol (JSON-RPC 2.0, one message per
// line) with a long-lived `supercharge agent stdio` process. The agent sends
// session updates as notifications and asks the client (us) for permissions,
// answers and plan approval as requests.

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		var text string
		if json.Unmarshal(e.Data, &text) == nil && text != "" {
			return e.Message + ": " + text
		}
	}
	return e.Message
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// acpHandler receives what the agent initiates.
type acpHandler interface {
	// notify gets every notification (session/update and extensions).
	notify(method string, params json.RawMessage)
	// request answers an agent request; it may block until a person answers.
	request(ctx context.Context, method string, params json.RawMessage) (any, error)
}

type acpConn struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	handler acpHandler

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[string]chan rpcMessage
	closed  chan struct{}
	err     error
	// ctx cancels requests the agent made when the process goes away.
	ctx    context.Context
	cancel context.CancelFunc
}

func startACP(cmd *exec.Cmd, handler acpHandler) (*acpConn, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &acpConn{cmd: cmd, stdin: stdin, handler: handler, pending: map[string]chan rpcMessage{}, closed: make(chan struct{}), ctx: ctx, cancel: cancel}
	go c.read(stdout)
	return c, nil
}

func (c *acpConn) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			go c.answer(msg)
		case msg.Method != "":
			c.handler.notify(msg.Method, msg.Params)
		case len(msg.ID) > 0:
			c.mu.Lock()
			ch := c.pending[string(msg.ID)]
			delete(c.pending, string(msg.ID))
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}
	err := scanner.Err()
	waitErr := c.cmd.Wait()
	if err == nil {
		err = waitErr
	}
	if err == nil {
		err = errors.New("agent exited")
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		ch <- rpcMessage{Error: &rpcError{Code: -32000, Message: "the agent process stopped"}}
		delete(c.pending, id)
	}
	c.mu.Unlock()
	c.cancel()
	close(c.closed)
}

func (c *acpConn) answer(msg rpcMessage) {
	result, err := c.handler.request(c.ctx, msg.Method, msg.Params)
	reply := map[string]any{"jsonrpc": "2.0", "id": msg.ID}
	if err != nil {
		var rpcErr *rpcError
		if errors.As(err, &rpcErr) {
			reply["error"] = rpcErr
		} else {
			reply["error"] = &rpcError{Code: -32603, Message: err.Error()}
		}
	} else {
		reply["result"] = result
	}
	_ = c.write(reply)
}

func (c *acpConn) write(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(raw, '\n'))
	return err
}

// call sends a request and waits for its result.
func (c *acpConn) call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	id := json.RawMessage(fmt.Sprint(c.nextID))
	ch := make(chan rpcMessage, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()
	if err := c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		return err
	}
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		return ctx.Err()
	}
}

func (c *acpConn) notifyAgent(method string, params any) error {
	return c.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *acpConn) alive() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

func (c *acpConn) close() {
	_ = c.stdin.Close()
	select {
	case <-c.closed:
	case <-time.After(3 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-c.closed
	}
}

// initializeACP runs the protocol handshake and returns the agent's reply.
func initializeACP(ctx context.Context, c *acpConn) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		// The agent reads and writes the workspace itself; no client fs or terminal.
		"clientCapabilities": map[string]any{"fs": map[string]bool{"readTextFile": false, "writeTextFile": false}, "terminal": false},
		"clientInfo":         map[string]string{"name": "vectra-host", "version": "1"},
	}, &out)
	if err != nil {
		log.Printf("acp initialize: %v", err)
	}
	return out, err
}
