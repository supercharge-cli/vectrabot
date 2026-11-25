package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func servePreview(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "port required"})
		return
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"listening": false, "port": port})
		return
	}
	_ = conn.Close()
	writeJSON(w, http.StatusOK, map[string]any{"listening": true, "host": "127.0.0.1", "port": port})
}

func serveScreen(w http.ResponseWriter, _ *http.Request) {
	url := os.Getenv("VECTRABOT_DESKTOP_URL")
	writeJSON(w, http.StatusOK, map[string]any{
		"configured": url != "",
		"url":        url,
	})
}
