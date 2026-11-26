package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

type resizeMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Terminals outlive their connection: a device that drops off (sleep, a
// network change, a reload) reconnects to the same shell with ?session=<id>
// and gets the recent output replayed. Binary frames carry PTY bytes; text
// frames carry JSON control messages.
const (
	terminalScrollback = 128 << 10
	terminalMaxShells  = 16
)

var terminalIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func terminalLinger() time.Duration {
	if raw := os.Getenv("VECTRABOT_TERMINAL_LINGER"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 10 * time.Minute
}

type shell struct {
	id   string
	cmd  *exec.Cmd
	ptmx *os.File
	done chan struct{}

	mu         sync.Mutex
	scrollback []byte
	conn       *websocket.Conn
	detached   time.Time
}

type shellPool struct {
	root string
	mu   sync.Mutex
	all  map[string]*shell
}

func newShellPool(root string) *shellPool { return &shellPool{root: root, all: map[string]*shell{}} }

func (p *shellPool) start(id string) (*shell, error) {
	name := os.Getenv("VECTRABOT_SHELL")
	if name == "" {
		name = "/bin/bash"
	}
	cmd := exec.Command(name)
	cmd.Env = append(childEnv(nil), "TERM=xterm-256color")
	cmd.Dir = p.root
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		return nil, err
	}
	s := &shell{id: id, cmd: cmd, ptmx: ptmx, done: make(chan struct{})}
	go s.pump()
	go func() { _ = cmd.Wait(); _ = ptmx.Close(); close(s.done); p.forget(s) }()
	return s, nil
}

// pump copies PTY output into the scrollback and to the attached connection.
func (s *shell) pump() {
	buf := make([]byte, 8192)
	for {
		n, err := s.ptmx.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			s.scrollback = append(s.scrollback, chunk...)
			if over := len(s.scrollback) - terminalScrollback; over > 0 {
				s.scrollback = append([]byte(nil), s.scrollback[over:]...)
			}
			conn := s.conn
			s.mu.Unlock()
			if conn != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = conn.Write(ctx, websocket.MessageBinary, chunk)
				cancel()
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *shell) kill() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (p *shellPool) forget(s *shell) {
	p.mu.Lock()
	if p.all[s.id] == s {
		delete(p.all, s.id)
	}
	p.mu.Unlock()
}

// acquire returns the shell for id (resumed=true) or starts one. Detached
// shells past their linger time, and the oldest detached shells beyond the
// cap, are ended here.
func (p *shellPool) acquire(id string) (*shell, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	linger := terminalLinger()
	var oldest *shell
	for key, s := range p.all {
		s.mu.Lock()
		idle := s.conn == nil && !s.detached.IsZero()
		expired := idle && time.Since(s.detached) > linger
		if idle && (oldest == nil || s.detached.Before(oldest.detached)) {
			oldest = s
		}
		s.mu.Unlock()
		if expired {
			s.kill()
			delete(p.all, key)
		}
	}
	if id != "" {
		if s, ok := p.all[id]; ok {
			return s, true, nil
		}
	}
	if len(p.all) >= terminalMaxShells && oldest != nil {
		oldest.kill()
		delete(p.all, oldest.id)
	}
	s, err := p.start(id)
	if err != nil {
		return nil, false, err
	}
	if id != "" {
		p.all[id] = s
	}
	return s, false, nil
}

func (p *shellPool) reapLater(s *shell) {
	time.AfterFunc(terminalLinger()+time.Second, func() {
		s.mu.Lock()
		idle := s.conn == nil && time.Since(s.detached) >= terminalLinger()
		s.mu.Unlock()
		if idle {
			s.kill()
			p.forget(s)
		}
	})
}

func control(ctx context.Context, conn *websocket.Conn, msg map[string]any) {
	raw, _ := json.Marshal(msg)
	_ = conn.Write(ctx, websocket.MessageText, raw)
}

func (p *shellPool) serve(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	if id != "" && !terminalIDPattern.MatchString(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "terminal session id is invalid"})
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(1 << 20)
	ctx := r.Context()
	s, resumed, err := p.acquire(id)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "pty failed")
		return
	}
	s.mu.Lock()
	previous := s.conn
	s.conn = conn
	s.detached = time.Time{}
	replay := append([]byte(nil), s.scrollback...)
	s.mu.Unlock()
	if previous != nil {
		_ = previous.Close(websocket.StatusPolicyViolation, "opened on another screen")
	}
	control(ctx, conn, map[string]any{"type": "attached", "resumed": resumed})
	if resumed && len(replay) > 0 {
		_ = conn.Write(ctx, websocket.MessageBinary, replay)
	}
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
			s.detached = time.Now()
		}
		s.mu.Unlock()
		if id == "" {
			s.kill() // an unnamed terminal ends with its connection
		} else {
			p.reapLater(s)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()
	go func() {
		select {
		case <-s.done:
			control(context.Background(), conn, map[string]any{"type": "exit"})
			_ = conn.Close(websocket.StatusNormalClosure, "shell exited")
		case <-ctx.Done():
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if len(data) > 0 && data[0] == '{' {
			var msg resizeMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
				if msg.Cols > 0 && msg.Rows > 0 && msg.Cols <= 1000 && msg.Rows <= 500 {
					_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
				}
				continue
			}
		}
		if _, err := s.ptmx.Write(data); err != nil && err != io.EOF {
			return
		}
	}
}
