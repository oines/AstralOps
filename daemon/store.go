package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type store struct {
	mu         sync.Mutex
	dataDir    string
	workspaces map[string]Workspace
	sessions   map[string]Session
	events     []AstralEvent
	nextSeq    int64
}

func loadStore(dataDir string) (*store, error) {
	st := &store{
		dataDir:    dataDir,
		workspaces: map[string]Workspace{},
		sessions:   map[string]Session{},
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "workspaces"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "events"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "projections"), 0o700); err != nil {
		return nil, err
	}

	workspaceFiles, _ := filepath.Glob(filepath.Join(dataDir, "workspaces", "*", "workspace.json"))
	for _, p := range workspaceFiles {
		var ws Workspace
		if body, err := os.ReadFile(p); err == nil && json.Unmarshal(body, &ws) == nil {
			st.workspaces[ws.ID] = ws
		}
	}

	eventFiles, _ := filepath.Glob(filepath.Join(dataDir, "events", "*.jsonl"))
	for _, p := range eventFiles {
		if err := st.loadEventFile(p); err != nil {
			return nil, err
		}
	}
	st.hydrateSessionsFromEvents()
	return st, nil
}

func (s *store) loadEventFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var ev AstralEvent
			if decodeErr := json.Unmarshal(line, &ev); decodeErr == nil {
				s.events = append(s.events, ev)
				if ev.Seq > s.nextSeq {
					s.nextSeq = ev.Seq
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *store) hydrateSessionsFromEvents() {
	for _, ev := range s.events {
		switch ev.Kind {
		case "session.started":
			var ss Session
			body, _ := json.Marshal(ev.Normalized)
			if json.Unmarshal(body, &ss) != nil || ss.ID == "" {
				ss = Session{
					ID:              ev.SessionID,
					WorkspaceID:     ev.WorkspaceID,
					Agent:           ev.Agent,
					Status:          "idle",
					NativeSessionID: stringValue(mapValue(ev.Normalized)["native_session_id"]),
					NativeThreadID:  stringValue(mapValue(ev.Normalized)["native_thread_id"]),
					CreatedAt:       ev.TS,
					UpdatedAt:       ev.TS,
				}
			}
			if ss.Agent == "" {
				ss.Agent = ev.Agent
			}
			ss.Status = "idle"
			s.sessions[ss.ID] = ss
		case "session.native":
			ss, ok := s.sessions[ev.SessionID]
			if !ok {
				ss = Session{
					ID:          ev.SessionID,
					WorkspaceID: ev.WorkspaceID,
					Agent:       ev.Agent,
					Status:      "idle",
					CreatedAt:   ev.TS,
				}
			}
			value := mapValue(ev.Normalized)
			if nativeSessionID := stringValue(value["native_session_id"]); nativeSessionID != "" {
				ss.NativeSessionID = nativeSessionID
			}
			if nativeThreadID := stringValue(value["native_thread_id"]); nativeThreadID != "" {
				ss.NativeThreadID = nativeThreadID
			}
			if ss.Agent == "" {
				ss.Agent = ev.Agent
			}
			if ss.WorkspaceID == "" {
				ss.WorkspaceID = ev.WorkspaceID
			}
			if ss.CreatedAt == "" {
				ss.CreatedAt = ev.TS
			}
			ss.Status = "idle"
			ss.UpdatedAt = ev.TS
			s.sessions[ss.ID] = ss
		case "session.deleted":
			delete(s.sessions, ev.SessionID)
		}
	}
}

func (s *store) listWorkspaces() []Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		out = append(out, ws)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (s *store) listSessions(workspaceID string) []Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.sessions))
	titles := s.sessionTitlesLocked()
	for _, ss := range s.sessions {
		if workspaceID != "" && ss.WorkspaceID != workspaceID {
			continue
		}
		ss.Title = titles[ss.ID]
		out = append(out, ss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (s *store) sessionTitlesLocked() map[string]string {
	titles := map[string]string{}
	for _, ev := range s.events {
		if ev.Kind != "message.user" {
			continue
		}
		if _, exists := titles[ev.SessionID]; exists {
			continue
		}
		text := normalizedSessionTitleText(ev.Normalized)
		if text == "" {
			continue
		}
		titles[ev.SessionID] = text
	}
	return titles
}

func normalizedSessionTitleText(normalized any) string {
	text := strings.Join(strings.Fields(stringValue(mapValue(normalized)["text"])), " ")
	if text == "" || shouldSkipSessionTitleText(text) {
		return ""
	}
	return text
}

func shouldSkipSessionTitleText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{
		"user accepted",
		"user declined",
		"user rejected",
		"plan approved",
		"plan declined",
		"plan rejected",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func (s *store) getWorkspace(id string) (Workspace, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws, ok := s.workspaces[id]
	return ws, ok
}

func (s *store) latestWorkspaceConnection(workspaceID string) (WorkspaceConnection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for index := len(s.events) - 1; index >= 0; index-- {
		ev := s.events[index]
		if ev.WorkspaceID != workspaceID || ev.Kind != "workspace.connection" {
			continue
		}
		var state WorkspaceConnection
		body, _ := json.Marshal(ev.Normalized)
		if err := json.Unmarshal(body, &state); err != nil || state.WorkspaceID == "" {
			continue
		}
		return state, true
	}
	return WorkspaceConnection{}, false
}

func (s *store) createWorkspace(req createWorkspaceRequest) (Workspace, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	id := "ws_" + randomID(12)
	projection := filepath.Join(s.dataDir, "projections", id, "root")
	ws := Workspace{
		ID:                  id,
		Name:                strings.TrimSpace(req.Name),
		Target:              req.Target,
		Agent:               req.Agent,
		LocalProjectionRoot: projection,
		LocalCWD:            strings.TrimSpace(req.LocalCWD),
		SSH:                 req.SSH,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if ws.Name == "" {
		ws.Name = id
	}
	if ws.Target == "local" && ws.LocalCWD == "" {
		ws.LocalCWD, _ = os.Getwd()
	}
	if ws.Target == "ssh" {
		if ws.SSH == nil {
			return Workspace{}, errors.New("ssh workspace requires ssh config")
		}
		ws.SSH.Endpoint = strings.TrimSpace(ws.SSH.Endpoint)
		ws.SSH.RemoteCWD = filepath.Clean(strings.TrimSpace(ws.SSH.RemoteCWD))
		if ws.SSH.Endpoint == "" {
			return Workspace{}, errors.New("ssh endpoint is required")
		}
		if ws.SSH.Port <= 0 {
			ws.SSH.Port = 22
		}
		if !filepath.IsAbs(ws.SSH.RemoteCWD) {
			return Workspace{}, errors.New("ssh remote cwd must be an absolute path")
		}
		ws.LocalCWD = ""
	}
	if ws.Agent != AgentClaude && ws.Agent != AgentCodex {
		return Workspace{}, errors.New("agent must be claude or codex")
	}
	if err := os.MkdirAll(projection, 0o700); err != nil {
		return Workspace{}, err
	}
	dir := filepath.Join(s.dataDir, "workspaces", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Workspace{}, err
	}
	body, _ := json.MarshalIndent(ws, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "workspace.json"), body, 0o600); err != nil {
		return Workspace{}, err
	}
	s.mu.Lock()
	s.workspaces[id] = ws
	s.mu.Unlock()
	return ws, nil
}

func (s *store) createSession(workspace Workspace, agent AgentKind) Session {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if agent == "" {
		agent = workspace.Agent
	}
	ss := Session{
		ID:              "sess_" + randomID(12),
		WorkspaceID:     workspace.ID,
		Agent:           agent,
		Status:          "idle",
		NativeSessionID: randomUUID(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.mu.Lock()
	s.sessions[ss.ID] = ss
	s.mu.Unlock()
	return ss
}

func (s *store) getSession(id string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[id]
	return ss, ok
}

func (s *store) updateSessionStatus(id, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[id]
	if !ok {
		return
	}
	ss.Status = status
	ss.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.sessions[id] = ss
}

func (s *store) updateSessionNativeThreadID(id, nativeThreadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.sessions[id]
	if !ok {
		return
	}
	ss.NativeThreadID = nativeThreadID
	ss.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.sessions[id] = ss
}

func (s *store) hasEventKind(sessionID, kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.SessionID == sessionID && ev.Kind == kind {
			return true
		}
	}
	return false
}

func (s *store) deleteSession(id string) {
	s.mu.Lock()
	session, sessionOK := s.sessions[id]
	delete(s.sessions, id)
	filtered := s.events[:0]
	for _, ev := range s.events {
		if ev.SessionID != id {
			filtered = append(filtered, ev)
		}
	}
	s.events = filtered
	s.mu.Unlock()

	_ = os.Remove(filepath.Join(s.dataDir, "events", id+".jsonl"))
	if sessionOK {
		_ = session
	}
}

func (s *store) deleteWorkspace(id string) {
	s.mu.Lock()
	delete(s.workspaces, id)
	sessionIDs := []string{}
	for sessionID, ss := range s.sessions {
		if ss.WorkspaceID == id {
			sessionIDs = append(sessionIDs, sessionID)
			delete(s.sessions, sessionID)
		}
	}
	filtered := s.events[:0]
	for _, ev := range s.events {
		if ev.WorkspaceID != id {
			filtered = append(filtered, ev)
		}
	}
	s.events = filtered
	s.mu.Unlock()

	_ = os.RemoveAll(filepath.Join(s.dataDir, "workspaces", id))
	_ = os.RemoveAll(filepath.Join(s.dataDir, "projections", id))
	for _, sessionID := range sessionIDs {
		_ = os.Remove(filepath.Join(s.dataDir, "events", sessionID+".jsonl"))
	}
}

func (s *store) appendEvent(ev AstralEvent) (AstralEvent, error) {
	s.mu.Lock()
	s.nextSeq++
	ev.Seq = s.nextSeq
	ev.TS = time.Now().UTC().Format(time.RFC3339Nano)
	s.events = append(s.events, ev)
	s.mu.Unlock()

	path := filepath.Join(s.dataDir, "events", ev.SessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return ev, err
	}
	defer f.Close()
	body, _ := json.Marshal(ev)
	_, err = f.Write(append(body, '\n'))
	return ev, err
}

func (s *store) queryEvents(workspaceID, sessionID string, afterSeq int64) []AstralEvent {
	return s.queryEventsWindow(workspaceID, sessionID, afterSeq, 0, 0)
}

func (s *store) queryEventsWindow(workspaceID, sessionID string, afterSeq, beforeSeq int64, limit int) []AstralEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []AstralEvent{}
	matches := func(ev AstralEvent) bool {
		if afterSeq > 0 && ev.Seq <= afterSeq {
			return false
		}
		if beforeSeq > 0 && ev.Seq >= beforeSeq {
			return false
		}
		if workspaceID != "" && ev.WorkspaceID != workspaceID {
			return false
		}
		if sessionID != "" && ev.SessionID != sessionID {
			return false
		}
		return true
	}
	if limit > 0 {
		for index := len(s.events) - 1; index >= 0; index-- {
			ev := s.events[index]
			if !matches(ev) {
				continue
			}
			out = append(out, ev)
			if len(out) >= limit {
				break
			}
		}
		for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
			out[left], out[right] = out[right], out[left]
		}
		return out
	}
	for _, ev := range s.events {
		if matches(ev) {
			out = append(out, ev)
		}
	}
	return out
}
