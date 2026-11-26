package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// eventHub fans host events out to every connected app over SSE. A short ring
// lets a client that reconnects with ?since=<cursor> catch up.
type eventHub struct {
	mu    sync.Mutex
	seq   int64
	ring  []hubFrame
	subs  map[chan hubFrame]struct{}
	epoch string
}

type hubFrame struct {
	Seq  int64
	Data []byte
}

const eventRing = 2000

func newEventHub() *eventHub {
	return &eventHub{subs: map[chan hubFrame]struct{}{}, epoch: strconv.FormatInt(time.Now().UnixMilli(), 36)}
}

func (h *eventHub) publish(kind string, payload map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	body := map[string]any{"kind": kind, "seq": h.seq, "at": nowMillis()}
	for k, v := range payload {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return
	}
	frame := hubFrame{Seq: h.seq, Data: raw}
	h.ring = append(h.ring, frame)
	if len(h.ring) > eventRing {
		h.ring = h.ring[len(h.ring)-eventRing:]
	}
	for ch := range h.subs {
		select {
		case ch <- frame:
		default: // a stalled client misses frames and resyncs on reconnect
		}
	}
}

func (h *eventHub) cursor() string { return fmt.Sprintf("%s.%d", h.epoch, h.seq) }

func (h *eventHub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan hubFrame, 512)
	h.mu.Lock()
	var backlog []hubFrame
	resumed := false
	since := r.URL.Query().Get("since")
	if since == "" {
		since = r.Header.Get("Last-Event-ID")
	}
	if epoch, n, found := cutCursor(since); found && epoch == h.epoch {
		for _, f := range h.ring {
			if f.Seq > n {
				backlog = append(backlog, f)
			}
		}
		resumed = len(h.ring) == 0 || h.ring[0].Seq <= n+1
	}
	h.subs[ch] = struct{}{}
	hello, _ := json.Marshal(map[string]any{"kind": "hello", "cursor": fmt.Sprintf("%s.%d", h.epoch, h.seq), "resumed": resumed})
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()
	write := func(seq int64, data []byte) bool {
		if seq > 0 {
			if _, err := fmt.Fprintf(w, "id: %s.%d\n", h.epoch, seq); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write(0, hello) {
		return
	}
	for _, f := range backlog {
		if !write(f.Seq, f.Data) {
			return
		}
	}
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-ch:
			if !write(f.Seq, f.Data) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func cutCursor(raw string) (string, int64, bool) {
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '.' {
			n, err := strconv.ParseInt(raw[i+1:], 10, 64)
			return raw[:i], n, err == nil
		}
	}
	return "", 0, false
}
