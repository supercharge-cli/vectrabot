package main

import (
	"encoding/json"
	"net/http"
)

func serveAgents(state *diskState, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"agents": state.listAgents()})
	case http.MethodPut:
		var agent agentRecord
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&agent); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent"})
			return
		}
		if agent.Engine != "" && agent.Engine != "supercharge" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only the supercharge engine is allowed"})
			return
		}
		agent.Engine = "supercharge"
		if err := state.putAgent(agent); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agent": agent, "engine": "supercharge"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func serveSettings(state *diskState, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, state.readSettings())
	case http.MethodPut:
		var next settingsRecord
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&next); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid settings"})
			return
		}
		if err := state.putSettings(next); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, state.readSettings())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}
