package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type sessionOverlayState struct {
	Version   int           `json:"version"`
	SessionID string        `json:"session_id"`
	Events    []AstralEvent `json:"events,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
}

type astralControlState struct {
	Version   int           `json:"version"`
	Events    []AstralEvent `json:"events,omitempty"`
	UpdatedAt string        `json:"updated_at,omitempty"`
}

func loadOverlayStates(dataDir string) map[string]sessionOverlayState {
	out := map[string]sessionOverlayState{}
	files, _ := filepath.Glob(filepath.Join(dataDir, "overlays", "*.json"))
	for _, path := range files {
		var state sessionOverlayState
		if body, err := os.ReadFile(path); err == nil && json.Unmarshal(body, &state) == nil && state.SessionID != "" {
			out[state.SessionID] = normalizeOverlayState(state)
		}
	}
	return out
}

func normalizeOverlayState(state sessionOverlayState) sessionOverlayState {
	if state.Version == 0 {
		state.Version = 1
	}
	for i := range state.Events {
		state.Events[i].Raw = nil
	}
	sort.SliceStable(state.Events, func(i, j int) bool {
		if state.Events[i].Seq == state.Events[j].Seq {
			return state.Events[i].TS < state.Events[j].TS
		}
		return state.Events[i].Seq < state.Events[j].Seq
	})
	return state
}

func loadControlState(dataDir string) astralControlState {
	var state astralControlState
	path := filepath.Join(dataDir, "control_state.json")
	if body, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(body, &state)
	}
	return normalizeControlState(state)
}

func normalizeControlState(state astralControlState) astralControlState {
	if state.Version == 0 {
		state.Version = 1
	}
	for i := range state.Events {
		state.Events[i].Raw = nil
	}
	sort.SliceStable(state.Events, func(i, j int) bool {
		if state.Events[i].Seq == state.Events[j].Seq {
			return state.Events[i].TS < state.Events[j].TS
		}
		return state.Events[i].Seq < state.Events[j].Seq
	})
	return state
}

func (s *store) overlayEventsSnapshot(sessionID string) []AstralEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionID != "" {
		state := s.overlays[sessionID]
		return append([]AstralEvent(nil), state.Events...)
	}
	out := []AstralEvent{}
	for _, state := range s.overlays {
		out = append(out, state.Events...)
	}
	return out
}

func (s *store) controlEventsSnapshot() []AstralEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AstralEvent(nil), s.controlState.Events...)
}

func (s *store) persistOverlayEvent(ev AstralEvent) error {
	if ev.SessionID == "" {
		return nil
	}
	ev.Raw = nil
	s.mu.Lock()
	state := s.overlays[ev.SessionID]
	if state.SessionID == "" {
		state = sessionOverlayState{Version: 1, SessionID: ev.SessionID}
	}
	state.Events = upsertStateEvent(state.Events, ev)
	state.UpdatedAt = firstString(ev.TS, time.Now().UTC().Format(time.RFC3339Nano))
	state = normalizeOverlayState(state)
	s.overlays[ev.SessionID] = state
	s.mu.Unlock()
	return writeJSONFile(filepath.Join(s.dataDir, "overlays", ev.SessionID+".json"), state, 0o600)
}

func (s *store) persistControlStateEvent(ev AstralEvent) error {
	ev.Raw = nil
	s.mu.Lock()
	state := s.controlState
	if state.Version == 0 {
		state.Version = 1
	}
	state.Events = upsertStateEvent(state.Events, ev)
	state.UpdatedAt = firstString(ev.TS, time.Now().UTC().Format(time.RFC3339Nano))
	state = normalizeControlState(state)
	s.controlState = state
	s.mu.Unlock()
	return writeJSONFile(filepath.Join(s.dataDir, "control_state.json"), state, 0o600)
}

func (s *store) removeOverlayState(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	delete(s.overlays, sessionID)
	s.mu.Unlock()
	_ = os.Remove(filepath.Join(s.dataDir, "overlays", sessionID+".json"))
}

func (s *store) removeWorkspaceControlState(workspaceID string) {
	if workspaceID == "" {
		return
	}
	s.mu.Lock()
	state := s.controlState
	next := state.Events[:0]
	for _, ev := range state.Events {
		if ev.WorkspaceID != workspaceID {
			next = append(next, ev)
		}
	}
	state.Events = append([]AstralEvent(nil), next...)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.controlState = normalizeControlState(state)
	s.mu.Unlock()
	_ = writeJSONFile(filepath.Join(s.dataDir, "control_state.json"), s.controlState, 0o600)
}

func (s *store) removeSessionControlState(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	state := s.controlState
	next := state.Events[:0]
	for _, ev := range state.Events {
		if ev.SessionID != sessionID {
			next = append(next, ev)
		}
	}
	state.Events = append([]AstralEvent(nil), next...)
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.controlState = normalizeControlState(state)
	s.mu.Unlock()
	_ = writeJSONFile(filepath.Join(s.dataDir, "control_state.json"), s.controlState, 0o600)
}

func upsertStateEvent(events []AstralEvent, ev AstralEvent) []AstralEvent {
	if ev.Seq > 0 {
		for index := range events {
			if events[index].Seq == ev.Seq {
				events[index] = ev
				return events
			}
		}
	}
	return append(events, ev)
}

func isOverlayEventKind(kind string) bool {
	return kind == "turn.replaced"
}

func isControlStateEventKind(kind string) bool {
	switch {
	case strings.HasPrefix(kind, "queue."):
		return true
	case strings.HasPrefix(kind, "approval."):
		return true
	case strings.HasPrefix(kind, "ask."):
		return true
	case strings.HasPrefix(kind, "control.terminal."):
		return true
	case kind == "workspace.connection":
		return true
	default:
		return false
	}
}

func isAgentTranscriptEventKind(kind string) bool {
	switch {
	case strings.HasPrefix(kind, "message."):
		return true
	case strings.HasPrefix(kind, "tool."):
		return true
	case strings.HasPrefix(kind, "reasoning."):
		return true
	case strings.HasPrefix(kind, "plan."):
		return true
	case strings.HasPrefix(kind, "hook."):
		return true
	case strings.HasPrefix(kind, "memory."):
		return true
	case kind == "turn.started" || kind == "turn.completed" || kind == "turn.failed" || kind == "turn.cancelled":
		return true
	default:
		return false
	}
}
