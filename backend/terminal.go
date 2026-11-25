package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

type resizeMessage struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func serveTerminal(root string, w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	shell := os.Getenv("VECTRABOT_SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell)
	cmd.Dir = root
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "pty failed")
		return
	}
	defer func() { _ = ptmx.Close(); _ = cmd.Process.Kill() }()
	ctx := r.Context()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				_ = conn.Write(ctx, websocket.MessageBinary, buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if len(data) > 0 && data[0] == '{' {
			var msg resizeMessage
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
				continue
			}
		}
		if _, err := ptmx.Write(data); err != nil && err != io.EOF {
			return
		}
	}
}
