package main

import (
	"net/http"
	"sync/atomic"
)

// activity counts work that should keep the sandbox awake. The control plane
// polls GET /v1/status and extends the sandbox's idle timeout while any of it
// is running, so an open terminal or desktop is not paused under the user.
type activity struct {
	turns     atomic.Int64
	terminals atomic.Int64
	viewers   atomic.Int64
}

func (a *activity) track(counter *atomic.Int64) func() {
	counter.Add(1)
	return func() { counter.Add(-1) }
}

func (a *activity) serveStatus(w http.ResponseWriter, _ *http.Request) {
	turns, terminals, viewers := a.turns.Load(), a.terminals.Load(), a.viewers.Load()
	tasks := []map[string]any{}
	if terminals > 0 {
		tasks = append(tasks, map[string]any{"id": "terminal", "status": "running", "count": terminals})
	}
	if viewers > 0 {
		tasks = append(tasks, map[string]any{"id": "desktop-viewer", "status": "running", "count": viewers})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"engine":          "supercharge",
		"turn":            map[string]any{"busy": turns > 0, "count": turns},
		"backgroundTasks": tasks,
	})
}
