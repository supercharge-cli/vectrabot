package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
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

// isAppPort leaves out the sandbox's own services and system daemons (ssh,
// rpcbind, ...): privileged ports other than 80 and 443 are not the user's app.
func isAppPort(port int) bool {
	if port < 1024 {
		return port == 80 || port == 443
	}
	return !infraPorts[port]
}

// infraPorts are the sandbox's own services; they are not the user's app.
var infraPorts = map[int]bool{5900: true, 6080: true, 8788: true, 8790: true, 9222: true, 9223: true, 49982: true, 49983: true}

// listeningPorts reads the kernel's TCP tables for sockets in LISTEN state.
func listeningPorts(files ...string) []int {
	seen := map[int]bool{}
	var ports []int
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if i == 0 || len(fields) < 4 || fields[3] != "0A" {
				continue
			}
			idx := strings.LastIndex(fields[1], ":")
			if idx < 0 {
				continue
			}
			hexPort := fields[1][idx+1:]
			port, err := strconv.ParseInt(hexPort, 16, 32)
			if err != nil || !isAppPort(int(port)) || seen[int(port)] {
				continue
			}
			seen[int(port)] = true
			ports = append(ports, int(port))
		}
	}
	sort.Ints(ports)
	return ports
}

func servePorts(w http.ResponseWriter, _ *http.Request) {
	ports := listeningPorts("/proc/net/tcp", "/proc/net/tcp6")
	if ports == nil {
		ports = []int{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ports": ports})
}
