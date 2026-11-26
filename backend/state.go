package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type agentRecord struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Engine       string `json:"engine"`
}

type settingsRecord struct {
	Engine        string `json:"engine"`
	WorkspaceRoot string `json:"workspaceRoot"`
	PreviewPort   int    `json:"previewPort"`
	DesktopURL    string `json:"desktopUrl"`
}

type diskState struct {
	mu       sync.Mutex
	root     string
	agents   []agentRecord
	settings settingsRecord
}

func openState(root string) *diskState {
	state := &diskState{root: root, settings: settingsRecord{Engine: "supercharge", WorkspaceRoot: root, PreviewPort: 3000}}
	_ = state.load()
	if len(state.agents) == 0 {
		state.agents = []agentRecord{{ID: "main", Name: "Chief", Instructions: "You are the main agent.", Engine: "supercharge"}}
		_ = state.save()
	}
	return state
}

func (s *diskState) file() string { return filepath.Join(s.root, ".vectrabot", "state.json") }

func (s *diskState) load() error {
	raw, err := os.ReadFile(s.file())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var body struct {
		Agents   []agentRecord  `json:"agents"`
		Settings settingsRecord `json:"settings"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	s.agents = body.Agents
	if body.Settings.Engine != "" {
		s.settings = body.Settings
	}
	s.settings.Engine = "supercharge"
	return nil
}

func (s *diskState) save() error {
	if err := os.MkdirAll(filepath.Dir(s.file()), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(struct {
		Agents   []agentRecord  `json:"agents"`
		Settings settingsRecord `json:"settings"`
	}{Agents: s.agents, Settings: s.settings}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.file(), raw, 0o644)
}

func validAgentID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func (s *diskState) listAgents() []agentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]agentRecord, len(s.agents))
	copy(out, s.agents)
	return out
}

func (s *diskState) putAgent(agent agentRecord) error {
	agent.Name = strings.TrimSpace(agent.Name)
	agent.Engine = "supercharge"
	if !validAgentID(agent.ID) || agent.Name == "" || len(agent.Name) > 80 {
		return errors.New("agent id and name are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.agents {
		if s.agents[i].ID == agent.ID {
			s.agents[i] = agent
			return s.save()
		}
	}
	s.agents = append(s.agents, agent)
	return s.save()
}

func (s *diskState) agent(id string) (agentRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, agent := range s.agents {
		if agent.ID == id {
			return agent, true
		}
	}
	return agentRecord{}, false
}

func (s *diskState) readSettings() settingsRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

func (s *diskState) putSettings(next settingsRecord) error {
	if next.PreviewPort < 1 || next.PreviewPort > 65535 {
		return errors.New("preview port is invalid")
	}
	next.Engine = "supercharge"
	next.WorkspaceRoot = s.root
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = next
	return s.save()
}
