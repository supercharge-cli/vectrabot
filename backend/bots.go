package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A bot is a named agent with its own instructions, settings, files and
// threads. Every thread is one Supercharge session.

type botSettings struct {
	// Model is a model id from the CLI catalog; empty keeps the CLI default.
	Model string `json:"model"`
	// PermissionMode is the CLI's ask | auto | always-approve.
	PermissionMode string `json:"permissionMode"`
	// Mode is the session mode new threads start in: default | plan | ask.
	Mode string `json:"mode"`
}

type botRecord struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	Instructions string      `json:"instructions"`
	Color        string      `json:"color"`
	Settings     botSettings `json:"settings"`
	ThreadID     string      `json:"threadId"`
	CreatedAt    int64       `json:"createdAt"`
	UpdatedAt    int64       `json:"updatedAt"`
}

type threadRecord struct {
	ID         string `json:"threadId"`
	BotID      string `json:"botId"`
	Title      string `json:"title"`
	SessionID  string `json:"sessionId,omitempty"`
	Mode       string `json:"mode,omitempty"`
	Pinned     bool   `json:"pinned"`
	ArchivedAt int64  `json:"archivedAt,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	// Primed is set once the bot's instructions went into the session.
	Primed bool `json:"primed"`
	// Renamed keeps a title the owner chose over the agent's own.
	Renamed bool `json:"renamed,omitempty"`
}

type toolDiff struct {
	Path    string `json:"path"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type toolCard struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	Title     string     `json:"title"`
	Kind      string     `json:"kind,omitempty"`
	Status    string     `json:"status"`
	Input     string     `json:"input,omitempty"`
	Output    string     `json:"output,omitempty"`
	Locations []string   `json:"locations,omitempty"`
	Diffs     []toolDiff `json:"diffs,omitempty"`
}

type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type questionItem struct {
	Question    string           `json:"question"`
	Header      string           `json:"header,omitempty"`
	Options     []questionOption `json:"options"`
	MultiSelect bool             `json:"multiSelect"`
}

type permissionOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// requestCard is something the agent asked the person: a permission, a set of
// questions, or a plan to approve.
type requestCard struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`  // permission | question | plan
	State     string             `json:"state"` // pending | answered | cancelled
	Title     string             `json:"title,omitempty"`
	Options   []permissionOption `json:"options,omitempty"`
	Questions []questionItem     `json:"questions,omitempty"`
	Plan      string             `json:"plan,omitempty"`
	Answer    any                `json:"answer,omitempty"`
}

type planEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

type message struct {
	ID       string       `json:"id"`
	ThreadID string       `json:"threadId"`
	Role     string       `json:"role"` // user | bot
	Kind     string       `json:"kind"` // text | thought | tool | request | todo | notice
	At       int64        `json:"at"`
	Text     string       `json:"text,omitempty"`
	Tool     *toolCard    `json:"tool,omitempty"`
	Request  *requestCard `json:"request,omitempty"`
	Entries  []planEntry  `json:"entries,omitempty"`
	Error    bool         `json:"error,omitempty"`
	Changes  []fileChange `json:"changes,omitempty"`
}

func nowMillis() int64 { return time.Now().UnixMilli() }

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

var errNotFound = errors.New("not found")

// botStore keeps bots, threads and messages as JSON files under the
// workspace's .vectrabot directory.
type botStore struct {
	mu       sync.Mutex
	dir      string
	bots     []*botRecord
	threads  map[string]*threadRecord
	messages map[string][]*message // loaded lazily
}

func openBotStore(root string, seed []agentRecord) *botStore {
	s := &botStore{dir: filepath.Join(root, ".vectrabot", "bots"), threads: map[string]*threadRecord{}, messages: map[string][]*message{}}
	_ = os.MkdirAll(filepath.Join(s.dir, "threads"), 0o700)
	if raw, err := os.ReadFile(filepath.Join(s.dir, "index.json")); err == nil {
		var body struct {
			Bots    []*botRecord    `json:"bots"`
			Threads []*threadRecord `json:"threads"`
		}
		if json.Unmarshal(raw, &body) == nil {
			s.bots = body.Bots
			for _, t := range body.Threads {
				s.threads[t.ID] = t
			}
		}
	}
	if len(s.bots) == 0 {
		for _, agent := range seed {
			s.createLocked(agent.Name, agent.Instructions, agent.ID)
		}
		if len(s.bots) == 0 {
			s.createLocked("Chief", "", "")
		}
		_ = s.saveLocked()
	}
	return s
}

func (s *botStore) saveLocked() error {
	threads := make([]*threadRecord, 0, len(s.threads))
	for _, t := range s.threads {
		threads = append(threads, t)
	}
	sort.Slice(threads, func(i, j int) bool { return threads[i].CreatedAt < threads[j].CreatedAt })
	raw, err := json.MarshalIndent(map[string]any{"bots": s.bots, "threads": threads}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(s.dir, "index.json"), raw)
}

func writeFileAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

var botColors = []string{"#4c8dff", "#3fb68b", "#e5a33d", "#d9665b", "#9b8afb", "#46b5c8"}

func (s *botStore) createLocked(name, instructions, id string) *botRecord {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "New bot"
	}
	if id == "" || s.botLocked(id) != nil {
		id = newID("bot_")
	}
	now := nowMillis()
	bot := &botRecord{ID: id, Name: name, Instructions: strings.TrimSpace(instructions), Color: botColors[len(s.bots)%len(botColors)], CreatedAt: now, UpdatedAt: now,
		Settings: botSettings{PermissionMode: "ask", Mode: "default"}}
	s.bots = append(s.bots, bot)
	t := s.newThreadLocked(bot, "")
	bot.ThreadID = t.ID
	return bot
}

func (s *botStore) newThreadLocked(bot *botRecord, title string) *threadRecord {
	now := nowMillis()
	t := &threadRecord{ID: newID("thr_"), BotID: bot.ID, Title: strings.TrimSpace(title), CreatedAt: now, UpdatedAt: now, Mode: bot.Settings.Mode}
	s.threads[t.ID] = t
	s.messages[t.ID] = []*message{}
	return t
}

func (s *botStore) botLocked(id string) *botRecord {
	for _, b := range s.bots {
		if b.ID == id {
			return b
		}
	}
	return nil
}

// threadsOfLocked lists a bot's threads, newest activity first, pinned on top.
func (s *botStore) threadsOfLocked(botID string) []*threadRecord {
	var out []*threadRecord
	for _, t := range s.threads {
		if t.BotID == botID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func (s *botStore) messagesLocked(threadID string) []*message {
	if list, ok := s.messages[threadID]; ok {
		return list
	}
	var list []*message
	if raw, err := os.ReadFile(s.threadFile(threadID)); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	if list == nil {
		list = []*message{}
	}
	s.messages[threadID] = list
	return list
}

func (s *botStore) threadFile(threadID string) string {
	return filepath.Join(s.dir, "threads", filepath.Base(threadID)+".json")
}

func (s *botStore) saveThreadLocked(threadID string) error {
	raw, err := json.Marshal(s.messagesLocked(threadID))
	if err != nil {
		return err
	}
	return writeFileAtomic(s.threadFile(threadID), raw)
}

// botFilesDir holds files the owner gives one bot; the agent can read them.
func (s *botStore) botFilesDir(botID string) string {
	return filepath.Join(s.dir, "files", filepath.Base(botID))
}

// copy helpers keep callers from holding pointers into the store.
func cloneMessage(m *message) *message {
	raw, _ := json.Marshal(m)
	var out message
	_ = json.Unmarshal(raw, &out)
	return &out
}
