package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Routines run a bot prompt on a schedule: once, daily at a time (optionally
// on chosen weekdays), or every N minutes. Each routine posts into its own
// thread of the bot. They run while the workspace is awake.

type routineSchedule struct {
	Type         string `json:"type"`                   // once | daily | interval
	At           int64  `json:"at,omitempty"`           // once: unix ms
	Time         string `json:"time,omitempty"`         // daily: HH:MM (workspace local time)
	Weekdays     []int  `json:"weekdays,omitempty"`     // daily: 0=Sunday; empty = every day
	EveryMinutes int    `json:"everyMinutes,omitempty"` // interval
	AnchorAt     int64  `json:"anchorAt,omitempty"`
}

type routineRecord struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Prompt    string          `json:"prompt"`
	BotID     string          `json:"botId"`
	ThreadID  string          `json:"threadId,omitempty"`
	Enabled   bool            `json:"enabled"`
	Schedule  routineSchedule `json:"schedule"`
	NextRunAt int64           `json:"nextRunAt,omitempty"`
	LastRunAt int64           `json:"lastRunAt,omitempty"`
	CreatedAt int64           `json:"createdAt"`
	UpdatedAt int64           `json:"updatedAt"`
}

type routineRun struct {
	ID           string `json:"id"`
	RoutineID    string `json:"routineId"`
	RoutineName  string `json:"routineName"`
	BotID        string `json:"botId"`
	ThreadID     string `json:"threadId"`
	ScheduledFor int64  `json:"scheduledFor"`
	StartedAt    int64  `json:"startedAt"`
	Status       string `json:"status"` // started | queued | failed
	Manual       bool   `json:"manual"`
	Error        string `json:"error,omitempty"`
}

type routineStore struct {
	mu       sync.Mutex
	file     string
	routines []*routineRecord
	runs     []routineRun
	hub      *botHub
}

func openRoutines(root string, hub *botHub) *routineStore {
	s := &routineStore{file: filepath.Join(root, ".vectrabot", "routines.json"), hub: hub}
	if raw, err := os.ReadFile(s.file); err == nil {
		var body struct {
			Routines []*routineRecord `json:"routines"`
			Runs     []routineRun     `json:"runs"`
		}
		if json.Unmarshal(raw, &body) == nil {
			s.routines, s.runs = body.Routines, body.Runs
		}
	}
	return s
}

func (s *routineStore) saveLocked() error {
	raw, err := json.MarshalIndent(map[string]any{"routines": s.routines, "runs": s.runs}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.file, raw)
}

func validateSchedule(sc routineSchedule) error {
	switch sc.Type {
	case "once":
		if sc.At <= 0 {
			return errors.New("once needs at")
		}
	case "daily":
		if _, err := time.Parse("15:04", sc.Time); err != nil {
			return errors.New("daily needs time HH:MM")
		}
		for _, d := range sc.Weekdays {
			if d < 0 || d > 6 {
				return errors.New("weekdays are 0-6")
			}
		}
	case "interval":
		if sc.EveryMinutes < 5 || sc.EveryMinutes > 7*24*60 {
			return errors.New("everyMinutes must be 5 to 10080")
		}
	default:
		return errors.New("schedule type must be once, daily or interval")
	}
	return nil
}

// nextRun is the first run time strictly after `after`, or 0 for none.
func nextRun(sc routineSchedule, after time.Time) int64 {
	switch sc.Type {
	case "once":
		if sc.At > after.UnixMilli() {
			return sc.At
		}
		return 0
	case "interval":
		step := time.Duration(sc.EveryMinutes) * time.Minute
		anchor := time.UnixMilli(sc.AnchorAt)
		if sc.AnchorAt == 0 || anchor.After(after) {
			if sc.AnchorAt != 0 {
				return sc.AnchorAt
			}
			return after.Add(step).UnixMilli()
		}
		n := after.Sub(anchor)/step + 1
		return anchor.Add(n * step).UnixMilli()
	case "daily":
		t, err := time.Parse("15:04", sc.Time)
		if err != nil {
			return 0
		}
		loc := after.Location()
		day := time.Date(after.Year(), after.Month(), after.Day(), t.Hour(), t.Minute(), 0, 0, loc)
		for i := 0; i < 8; i++ {
			c := day.AddDate(0, 0, i)
			if !c.After(after) {
				continue
			}
			if len(sc.Weekdays) == 0 || containsInt(sc.Weekdays, int(c.Weekday())) {
				return c.UnixMilli()
			}
		}
	}
	return 0
}

func containsInt(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *routineStore) list() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := append([]routineRun(nil), s.runs...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt > runs[j].StartedAt })
	return map[string]any{"routines": append([]*routineRecord{}, s.routines...), "runs": runs}
}

type routineInput struct {
	Name     *string          `json:"name"`
	Prompt   *string          `json:"prompt"`
	BotID    *string          `json:"botId"`
	Enabled  *bool            `json:"enabled"`
	Schedule *routineSchedule `json:"schedule"`
}

func (s *routineStore) upsert(id string, in routineInput) (*routineRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r *routineRecord
	if id != "" {
		for _, x := range s.routines {
			if x.ID == id {
				r = x
			}
		}
		if r == nil {
			return nil, errNotFound
		}
	} else {
		r = &routineRecord{ID: newID("rtn_"), Enabled: true, CreatedAt: nowMillis()}
	}
	next := *r
	if in.Name != nil {
		next.Name = strings.TrimSpace(*in.Name)
	}
	if in.Prompt != nil {
		next.Prompt = strings.TrimSpace(*in.Prompt)
	}
	if in.BotID != nil {
		next.BotID = *in.BotID
		if next.BotID != r.BotID {
			next.ThreadID = ""
		}
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.Schedule != nil {
		next.Schedule = *in.Schedule
	}
	if next.Name == "" || next.Prompt == "" {
		return nil, errors.New("name and prompt required")
	}
	s.hub.store.mu.Lock()
	ok := s.hub.store.botLocked(next.BotID) != nil
	s.hub.store.mu.Unlock()
	if !ok {
		return nil, errors.New("unknown bot")
	}
	if err := validateSchedule(next.Schedule); err != nil {
		return nil, err
	}
	if next.Schedule.Type == "interval" && next.Schedule.AnchorAt == 0 {
		next.Schedule.AnchorAt = time.Now().Add(time.Duration(next.Schedule.EveryMinutes) * time.Minute).UnixMilli()
	}
	next.NextRunAt = 0
	if next.Enabled {
		next.NextRunAt = nextRun(next.Schedule, time.Now())
	}
	next.UpdatedAt = nowMillis()
	*r = next
	if id == "" {
		s.routines = append(s.routines, r)
	}
	_ = s.saveLocked()
	cp := *r
	s.hub.events.publish("routine", map[string]any{"routine": cp})
	return &cp, nil
}

func (s *routineStore) remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.routines {
		if r.ID == id {
			s.routines = append(s.routines[:i], s.routines[i+1:]...)
			_ = s.saveLocked()
			s.hub.events.publish("routine.deleted", map[string]any{"routineId": id})
			return nil
		}
	}
	return errNotFound
}

// removeBot drops routines of a deleted bot.
func (s *routineStore) removeBot(botID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.routines[:0]
	for _, r := range s.routines {
		if r.BotID != botID {
			kept = append(kept, r)
		}
	}
	s.routines = kept
	_ = s.saveLocked()
}

// fire runs one routine now.
func (s *routineStore) fire(id string, manual bool) (routineRun, error) {
	s.mu.Lock()
	var r *routineRecord
	for _, x := range s.routines {
		if x.ID == id {
			r = x
		}
	}
	if r == nil {
		s.mu.Unlock()
		return routineRun{}, errNotFound
	}
	scheduled := r.NextRunAt
	if manual || scheduled == 0 {
		scheduled = nowMillis()
	}
	threadID := r.ThreadID
	s.mu.Unlock()

	h := s.hub
	h.store.mu.Lock()
	bot := h.store.botLocked(r.BotID)
	if bot == nil {
		h.store.mu.Unlock()
		return routineRun{}, errors.New("the routine's bot is gone")
	}
	if threadID == "" || h.store.threads[threadID] == nil {
		t := h.store.newThreadLocked(bot, "Routine · "+r.Name)
		t.Renamed = true
		threadID = t.ID
		_ = h.store.saveLocked()
		h.events.publish("thread", map[string]any{"botId": bot.ID, "threadId": t.ID, "thread": *t})
	}
	h.store.mu.Unlock()
	run := routineRun{ID: newID("run_"), RoutineID: r.ID, RoutineName: r.Name, BotID: r.BotID, ThreadID: threadID, ScheduledFor: scheduled, StartedAt: nowMillis(), Manual: manual, Status: "started"}
	queued, err := h.send(context.Background(), threadID, r.Prompt)
	if err != nil {
		run.Status, run.Error = "failed", err.Error()
	} else if queued {
		run.Status = "queued"
	}
	s.mu.Lock()
	r.ThreadID = threadID
	r.LastRunAt = run.StartedAt
	if !manual {
		r.NextRunAt = nextRun(r.Schedule, time.Now())
		if r.Schedule.Type == "once" {
			r.Enabled = false
		}
	}
	s.runs = append(s.runs, run)
	if len(s.runs) > 200 {
		s.runs = s.runs[len(s.runs)-200:]
	}
	_ = s.saveLocked()
	cp := *r
	s.mu.Unlock()
	h.events.publish("routine", map[string]any{"routine": cp, "run": run})
	return run, nil
}

// schedule wakes every 20s and fires what is due. A routine missed while the
// workspace slept runs once on wake, then follows its schedule.
func (s *routineStore) schedule() {
	for range time.Tick(20 * time.Second) {
		s.tick(time.Now())
	}
}

func (s *routineStore) tick(now time.Time) {
	s.mu.Lock()
	var due []string
	for _, r := range s.routines {
		if r.Enabled && r.NextRunAt > 0 && r.NextRunAt <= now.UnixMilli() {
			due = append(due, r.ID)
		}
	}
	s.mu.Unlock()
	for _, id := range due {
		_, _ = s.fire(id, false)
	}
}
