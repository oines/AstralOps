package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oines/astralops/pkg/protocol"
)

type store struct {
	mu               sync.Mutex
	dataDir          string
	deviceIdentity   DeviceIdentity
	devicePrivateKey []byte
	cloudMembership  cloudMembershipState
	trustGrants      map[string]TrustGrant
	pairingRequests  map[string]PairingRequest
	knownHosts       map[string]KnownHost
	workspaces       map[string]Workspace
	sessions         map[string]Session
	events           []AstralEvent
	overlays         map[string]sessionOverlayState
	controlState     astralControlState
	nextSeq          int64
}

func loadStore(dataDir string) (*store, error) {
	identity, privateKey, err := loadDeviceIdentity(dataDir)
	if err != nil {
		return nil, err
	}
	trustGrants, err := loadTrustGrants(dataDir)
	if err != nil {
		return nil, err
	}
	pairingRequests, err := loadPairingRequests(dataDir)
	if err != nil {
		return nil, err
	}
	knownHosts, err := loadKnownHosts(dataDir)
	if err != nil {
		return nil, err
	}
	cloudMembership, err := loadCloudMembership(dataDir)
	if err != nil {
		return nil, err
	}
	st := &store{
		dataDir:          dataDir,
		deviceIdentity:   identity,
		devicePrivateKey: privateKey,
		cloudMembership:  cloudMembership,
		trustGrants:      trustGrants,
		pairingRequests:  pairingRequests,
		knownHosts:       knownHosts,
		workspaces:       map[string]Workspace{},
		sessions:         map[string]Session{},
		overlays:         map[string]sessionOverlayState{},
		controlState:     astralControlState{Version: 1},
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "workspaces"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "sessions"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "events"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "overlays"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "migrations"), 0o700); err != nil {
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

	sessionFiles, _ := filepath.Glob(filepath.Join(dataDir, "sessions", "*.json"))
	for _, p := range sessionFiles {
		var ss Session
		if body, err := os.ReadFile(p); err == nil && json.Unmarshal(body, &ss) == nil && ss.ID != "" {
			normalizeStoredSession(&ss)
			st.sessions[ss.ID] = ss
		}
	}

	st.overlays = loadOverlayStates(dataDir)
	st.controlState = loadControlState(dataDir)
	if err := st.migrateLegacyNativeHistory(); err != nil {
		return nil, err
	}
	if err := st.persistLoadedSessions(); err != nil {
		return nil, err
	}
	return st, nil
}

func (s *store) applySessionEventLocked(ev AstralEvent) {
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
		if ss.WorkspaceID == "" {
			ss.WorkspaceID = ev.WorkspaceID
		}
		normalizeStoredSession(&ss)
		if ss.Status == "" {
			ss.Status = "idle"
		}
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
		normalizeStoredSession(&ss)
		ss.Status = "idle"
		ss.UpdatedAt = ev.TS
		s.sessions[ss.ID] = ss
	case "session.deleted":
		delete(s.sessions, ev.SessionID)
	}
}

func (s *store) touchSessionForEventLocked(ev AstralEvent) (Session, bool) {
	if ev.SessionID == "" || ev.TS == "" {
		return Session{}, false
	}
	ss, ok := s.sessions[ev.SessionID]
	if !ok {
		return Session{}, false
	}
	ss.UpdatedAt = ev.TS
	s.sessions[ev.SessionID] = ss
	return ss, true
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
	titles := s.sessionTitlesLocked()
	managed := make([]Session, 0, len(s.sessions))
	for _, ss := range s.sessions {
		if workspaceID != "" && ss.WorkspaceID != workspaceID {
			continue
		}
		if title := titles[ss.ID]; title != "" {
			ss.Title = title
		}
		normalizeStoredSession(&ss)
		managed = append(managed, ss)
	}
	s.mu.Unlock()

	out := make([]Session, 0, len(managed))
	managedByNative := map[string]int{}
	for _, ss := range managed {
		if key := sessionNativeKey(ss); key != "" {
			if existing, ok := managedByNative[key]; ok {
				out[existing] = mergeDuplicateNativeSession(out[existing], ss)
				continue
			}
			managedByNative[key] = len(out)
		}
		out = append(out, ss)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (s *store) listNativeSessionCandidates(workspaceID string) []Session {
	s.mu.Lock()
	workspaces := make([]Workspace, 0, len(s.workspaces))
	for _, ws := range s.workspaces {
		if workspaceID != "" && ws.ID != workspaceID {
			continue
		}
		workspaces = append(workspaces, ws)
	}
	managedNative := map[string]bool{}
	for _, ss := range s.sessions {
		if workspaceID != "" && ss.WorkspaceID != workspaceID {
			continue
		}
		normalizeStoredSession(&ss)
		if key := sessionNativeKey(ss); key != "" {
			managedNative[key] = true
		}
	}
	s.mu.Unlock()

	out := []Session{}
	index := nativeSessionIndex{}
	for _, ws := range workspaces {
		for _, record := range index.List(ws) {
			ss := record.Session
			if key := sessionNativeKey(ss); key != "" && managedNative[key] {
				continue
			}
			ss = sanitizeNativeSessionCandidate(ss)
			out = append(out, ss)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func sanitizeNativeSessionCandidate(ss Session) Session {
	if ss.NativeRef != nil {
		ref := *ss.NativeRef
		ref.LocalPath = ""
		ref.RemotePath = ""
		ref.WorkspaceCWD = ""
		ss.NativeRef = &ref
	}
	return ss
}

func (s *store) importNativeSession(workspaceID, candidateID string) (Session, error) {
	candidateID = strings.TrimSpace(candidateID)
	if candidateID == "" {
		return Session{}, errors.New("native session id required")
	}
	workspace, ok := s.getWorkspace(workspaceID)
	if !ok {
		return Session{}, errors.New("workspace not found")
	}
	for _, existing := range s.listSessions(workspaceID) {
		if existing.ID == candidateID {
			return existing, nil
		}
	}
	for _, record := range (nativeSessionIndex{}).List(workspace) {
		ss := record.Session
		if ss.ID != candidateID {
			continue
		}
		if key := sessionNativeKey(ss); key != "" {
			for _, existing := range s.listSessions(workspaceID) {
				if sessionNativeKey(existing) == key {
					return existing, nil
				}
			}
		}
		ss.Source = SessionSourceLinked
		ss.ManagedByAstralOps = true
		ss.Status = firstString(ss.Status, string(SessionStatusIdle))
		ss.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		normalizeStoredSession(&ss)
		s.mu.Lock()
		s.sessions[ss.ID] = ss
		s.mu.Unlock()
		if err := s.writeSessionFile(ss); err != nil {
			return Session{}, err
		}
		return ss, nil
	}
	return Session{}, errors.New("native session not found")
}

func mergeDiscoveredNativeRef(managed Session, discovered Session) Session {
	if managed.NativeRef == nil {
		managed.NativeRef = discovered.NativeRef
	} else if discovered.NativeRef != nil {
		if managed.NativeRef.LocalPath == "" {
			managed.NativeRef.LocalPath = discovered.NativeRef.LocalPath
		}
		if managed.NativeRef.WorkspaceCWD == "" {
			managed.NativeRef.WorkspaceCWD = discovered.NativeRef.WorkspaceCWD
		}
		if managed.NativeRef.NativeSessionID == "" {
			managed.NativeRef.NativeSessionID = discovered.NativeRef.NativeSessionID
		}
		if managed.NativeRef.NativeThreadID == "" {
			managed.NativeRef.NativeThreadID = discovered.NativeRef.NativeThreadID
		}
	}
	if discovered.Title != "" {
		managed.Title = discovered.Title
	}
	if discovered.UpdatedAt > managed.UpdatedAt {
		managed.UpdatedAt = discovered.UpdatedAt
	}
	if managed.Source == SessionSourceManaged {
		managed.Source = SessionSourceLinked
	}
	return managed
}

func mergeDuplicateNativeSession(primary Session, duplicate Session) Session {
	if duplicate.UpdatedAt > primary.UpdatedAt {
		primary, duplicate = duplicate, primary
	}
	if primary.Title == "" {
		primary.Title = duplicate.Title
	}
	if primary.CreatedAt == "" || (duplicate.CreatedAt != "" && duplicate.CreatedAt < primary.CreatedAt) {
		primary.CreatedAt = duplicate.CreatedAt
	}
	if primary.NativeRef == nil {
		primary.NativeRef = duplicate.NativeRef
	} else if duplicate.NativeRef != nil {
		if primary.NativeRef.LocalPath == "" {
			primary.NativeRef.LocalPath = duplicate.NativeRef.LocalPath
		}
		if primary.NativeRef.WorkspaceCWD == "" {
			primary.NativeRef.WorkspaceCWD = duplicate.NativeRef.WorkspaceCWD
		}
	}
	if primary.NativeSessionID == "" {
		primary.NativeSessionID = duplicate.NativeSessionID
	}
	if primary.NativeThreadID == "" {
		primary.NativeThreadID = duplicate.NativeThreadID
	}
	return primary
}

func (s *store) sessionTitlesLocked() map[string]string {
	type candidate struct {
		text string
		rank int
	}
	titles := map[string]candidate{}
	for _, ev := range s.events {
		value := mapValue(ev.Normalized)
		text := ""
		rank := 0
		switch ev.Kind {
		case "session.native", "session.updated":
			text, rank = normalizedNativeSessionTitle(value)
		case "message.user":
			text = normalizedSessionTitleText(value)
			rank = 10
		default:
			continue
		}
		if text == "" {
			continue
		}
		if current, exists := titles[ev.SessionID]; exists {
			if current.rank > rank || (current.rank == rank && rank <= 10) {
				continue
			}
		}
		titles[ev.SessionID] = candidate{text: text, rank: rank}
	}
	out := map[string]string{}
	for sessionID, title := range titles {
		out[sessionID] = title.text
	}
	return out
}

func (s *store) sessionTitle(sessionID string) string {
	s.mu.Lock()
	title := s.sessionTitlesLocked()[sessionID]
	if title == "" {
		if ss, ok := s.sessions[sessionID]; ok {
			title = ss.Title
		}
	}
	s.mu.Unlock()
	return title
}

func (s *store) runtimeEventsSnapshot() []AstralEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AstralEvent(nil), s.events...)
}

func (s *store) runtimeEventsSnapshotForSession(sessionID string) []AstralEvent {
	if sessionID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []AstralEvent{}
	for _, ev := range s.events {
		if ev.SessionID == sessionID {
			out = append(out, ev)
		}
	}
	return out
}

func (s *store) latestSessionIDForWorkspace(workspaceID string) (string, bool) {
	var latest Session
	for _, ss := range s.listSessions(workspaceID) {
		if ss.WorkspaceID != workspaceID {
			continue
		}
		if latest.ID == "" || ss.UpdatedAt > latest.UpdatedAt {
			latest = ss
		}
	}
	if latest.ID == "" {
		return "", false
	}
	return latest.ID, true
}

func (s *store) currentSeq() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextSeq
}

func normalizedNativeSessionTitle(value map[string]any) (string, int) {
	candidates := []struct {
		rank int
		keys []string
	}{
		{50, []string{"agent_name", "agentName", "custom_title", "customTitle"}},
		{40, []string{"thread_name", "threadName", "name", "title"}},
		{30, []string{"summary", "ai_title", "aiTitle"}},
		{20, []string{"last_prompt", "lastPrompt"}},
		{10, []string{"preview", "first_prompt", "firstPrompt"}},
	}
	for _, candidate := range candidates {
		for _, key := range candidate.keys {
			text := normalizeSessionTitleString(stringValue(value[key]))
			if text != "" {
				return text, candidate.rank
			}
		}
	}
	return "", 0
}

func normalizedSessionTitleText(normalized any) string {
	text := normalizeSessionTitleString(stringValue(mapValue(normalized)["text"]))
	if text == "" || shouldSkipSessionTitleText(text) {
		return ""
	}
	return text
}

func normalizeSessionTitleString(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func shouldSkipSessionTitleText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lower, "<") || strings.HasPrefix(lower, "[request interrupted by user") {
		return true
	}
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
	for index := len(s.controlState.Events) - 1; index >= 0; index-- {
		ev := s.controlState.Events[index]
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
		Agent:               workspaceAgentOrDefault(req.Agent),
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
		ws.SSH.RemoteCWD = remotePathClean(ws.SSH.RemoteCWD)
		if ws.SSH.Endpoint == "" {
			return Workspace{}, errors.New("ssh endpoint is required")
		}
		if ws.SSH.Port <= 0 {
			ws.SSH.Port = 22
		}
		if !remotePathIsAbs(ws.SSH.RemoteCWD) {
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

func workspaceAgentOrDefault(agent AgentKind) AgentKind {
	if agent == "" {
		return AgentClaude
	}
	return agent
}

func (s *store) createSession(workspace Workspace, agent AgentKind) Session {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if agent == "" {
		agent = workspace.Agent
	}
	ss := Session{
		ID:                 "sess_" + randomID(12),
		WorkspaceID:        workspace.ID,
		Agent:              agent,
		Status:             "idle",
		Source:             SessionSourceManaged,
		ManagedByAstralOps: true,
		NativeSessionID:    randomUUID(),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	ss.NativeRef = nativeRefForManagedSession(workspace, ss)
	s.mu.Lock()
	s.sessions[ss.ID] = ss
	s.mu.Unlock()
	_ = s.writeSessionFile(ss)
	return ss
}

func (s *store) createForkSession(workspace Workspace, source Session, anchor forkAnchor) Session {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	ss := Session{
		ID:                     "sess_" + randomID(12),
		WorkspaceID:            workspace.ID,
		Agent:                  source.Agent,
		Status:                 "idle",
		Source:                 SessionSourceManaged,
		ManagedByAstralOps:     true,
		NativeSessionID:        randomUUID(),
		ForkedFromSessionID:    source.ID,
		ForkedFromEventSeq:     anchor.EventSeq,
		ForkedFromNativeAnchor: anchor.NativeAnchor,
		ForkedFromTitle:        anchor.SourceTitle,
		CreatedAt:              now,
		UpdatedAt:              now,
	}
	ss.NativeRef = nativeRefForManagedSession(workspace, ss)
	s.mu.Lock()
	s.sessions[ss.ID] = ss
	s.mu.Unlock()
	_ = s.writeSessionFile(ss)
	return ss
}

func (s *store) getSession(id string) (Session, bool) {
	s.mu.Lock()
	ss, ok := s.sessions[id]
	if ok {
		normalizeStoredSession(&ss)
		s.mu.Unlock()
		return s.resolveSessionNativeRef(ss), true
	}
	s.mu.Unlock()
	for _, candidate := range s.listSessions("") {
		if candidate.ID == id {
			return candidate, true
		}
	}
	return Session{}, false
}

func (s *store) updateSessionStatus(id, status string) {
	s.mu.Lock()
	ss, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	ss.Status = status
	ss.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.sessions[id] = ss
	s.mu.Unlock()
	_ = s.writeSessionFile(ss)
}

func (s *store) updateSessionNativeThreadID(id, nativeThreadID string) {
	s.mu.Lock()
	ss, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	ss.NativeThreadID = nativeThreadID
	if ss.NativeRef != nil {
		ss.NativeRef.NativeThreadID = nativeThreadID
	}
	ss.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	s.sessions[id] = ss
	s.mu.Unlock()
	_ = s.writeSessionFile(ss)
}

func (s *store) hasEventKind(sessionID, kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.SessionID == sessionID && string(ev.Kind) == kind {
			return true
		}
	}
	return false
}

func (s *store) shouldResumeClaudeSession(session Session) bool {
	if session.Agent != AgentClaude || session.NativeSessionID == "" {
		return false
	}
	if session.Source == SessionSourceLinked {
		return true
	}
	if session.NativeRef != nil && session.NativeRef.LocalPath != "" {
		return true
	}
	return s.hasEventKind(session.ID, "session.native")
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
	s.removeOverlayState(id)
	s.removeSessionControlState(id)
	if sessionOK {
		_ = session
		_ = os.Remove(filepath.Join(s.dataDir, "sessions", id+".json"))
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
	s.removeWorkspaceControlState(id)
	for _, sessionID := range sessionIDs {
		_ = os.Remove(filepath.Join(s.dataDir, "events", sessionID+".jsonl"))
		_ = os.Remove(filepath.Join(s.dataDir, "sessions", sessionID+".json"))
		s.removeOverlayState(sessionID)
	}
}

func normalizeStoredSession(ss *Session) {
	if ss == nil {
		return
	}
	if ss.Source == "" {
		ss.Source = SessionSourceManaged
	}
	if ss.Source == SessionSourceManaged || ss.Source == SessionSourceLinked {
		ss.ManagedByAstralOps = true
	}
	if ss.Status == "" {
		ss.Status = string(SessionStatusIdle)
	}
	if ss.NativeRef == nil && (ss.NativeSessionID != "" || ss.NativeThreadID != "") {
		ss.NativeRef = &NativeSessionRef{
			Agent:           ss.Agent,
			NativeSessionID: ss.NativeSessionID,
			NativeThreadID:  ss.NativeThreadID,
		}
	}
	if ss.NativeRef != nil {
		if ss.NativeRef.Agent == "" {
			ss.NativeRef.Agent = ss.Agent
		}
		if ss.NativeSessionID == "" {
			ss.NativeSessionID = ss.NativeRef.NativeSessionID
		}
		if ss.NativeThreadID == "" {
			ss.NativeThreadID = ss.NativeRef.NativeThreadID
		}
	}
}

func (s *store) resolveSessionNativeRef(ss Session) Session {
	if ss.WorkspaceID == "" {
		return ss
	}
	workspace, ok := s.getWorkspace(ss.WorkspaceID)
	if !ok {
		return ss
	}
	resolved := resolveNativeRefForSession(workspace, ss)
	if resolved == nil {
		return ss
	}
	nativeTitle := nativeSessionTitleFromRef(resolved)
	if ss.NativeRef == nil || ss.NativeRef.LocalPath == "" || ss.NativeRef.WorkspaceCWD == "" {
		ss.NativeRef = resolved
		if ss.NativeSessionID == "" {
			ss.NativeSessionID = resolved.NativeSessionID
		}
		if ss.NativeThreadID == "" {
			ss.NativeThreadID = resolved.NativeThreadID
		}
		if nativeTitle != "" {
			ss.Title = nativeTitle
		}
		if resolved.LocalPath != "" {
			s.mu.Lock()
			current, ok := s.sessions[ss.ID]
			if ok {
				current.NativeRef = resolved
				if current.NativeSessionID == "" {
					current.NativeSessionID = resolved.NativeSessionID
				}
				if current.NativeThreadID == "" {
					current.NativeThreadID = resolved.NativeThreadID
				}
				if nativeTitle != "" {
					current.Title = nativeTitle
				}
				s.sessions[ss.ID] = current
				ss = current
			}
			s.mu.Unlock()
			_ = s.writeSessionFile(ss)
		}
	} else if nativeTitle != "" && nativeTitle != ss.Title {
		ss.Title = nativeTitle
		s.mu.Lock()
		current, ok := s.sessions[ss.ID]
		if ok {
			current.Title = nativeTitle
			s.sessions[ss.ID] = current
			ss = current
		}
		s.mu.Unlock()
		if ok {
			_ = s.writeSessionFile(ss)
		}
	}
	return ss
}

func (s *store) persistLoadedSessions() error {
	for _, ss := range s.sessions {
		normalizeStoredSession(&ss)
		if err := s.writeSessionFile(ss); err != nil {
			return err
		}
	}
	return nil
}

func (s *store) writeSessionFile(ss Session) error {
	normalizeStoredSession(&ss)
	if ss.ID == "" {
		return nil
	}
	dir := filepath.Join(s.dataDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(ss, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ss.ID+".json"), body, 0o600)
}

func nativeRefForManagedSession(workspace Workspace, ss Session) *NativeSessionRef {
	ref := &NativeSessionRef{
		Agent:           ss.Agent,
		NativeSessionID: ss.NativeSessionID,
		NativeThreadID:  ss.NativeThreadID,
	}
	if workspace.Target == string(protocol.WorkspaceTargetLocal) {
		ref.WorkspaceCWD = workspace.LocalCWD
	} else if workspace.SSH != nil {
		ref.WorkspaceCWD = workspace.LocalProjectionRoot
		ref.RemotePath = workspace.SSH.RemoteCWD
	}
	return ref
}

func sessionNativeKey(ss Session) string {
	normalizeStoredSession(&ss)
	if ss.NativeRef == nil {
		return ""
	}
	parts := []string{
		ss.WorkspaceID,
		string(ss.NativeRef.Agent),
		ss.NativeRef.NativeSessionID,
		ss.NativeRef.NativeThreadID,
	}
	if parts[0] == "" || parts[1] == "" || (parts[2] == "" && parts[3] == "") {
		return ""
	}
	return strings.Join(parts, "\x00")
}

func shouldPersistEventToDomainLog(ev AstralEvent) bool {
	kind := string(ev.Kind)
	switch {
	case strings.HasPrefix(kind, "message."):
		return false
	case strings.HasPrefix(kind, "tool."):
		return false
	case strings.HasPrefix(kind, "reasoning."):
		return false
	case strings.HasPrefix(kind, "plan."):
		return false
	case strings.HasPrefix(kind, "hook."):
		return false
	case strings.HasPrefix(kind, "memory."):
		return false
	case kind == "turn.started" || kind == "turn.completed" || kind == "turn.failed" || kind == "turn.cancelled":
		return false
	case kind == "control.status" || kind == "control.rate_limit" || kind == "control.warning" || kind == "control.model" || kind == "control.raw":
		return false
	default:
		return true
	}
}

func (s *store) appendEvent(ev AstralEvent) (AstralEvent, error) {
	var touched Session
	var touchedOK bool
	s.mu.Lock()
	s.nextSeq++
	ev.Seq = s.nextSeq
	ev.TS = time.Now().UTC().Format(time.RFC3339Nano)
	s.events = append(s.events, ev)
	s.applySessionEventLocked(ev)
	touched, touchedOK = s.touchSessionForEventLocked(ev)
	s.mu.Unlock()
	if touchedOK {
		_ = s.writeSessionFile(touched)
	}
	if isOverlayEventKind(string(ev.Kind)) {
		if err := s.persistOverlayEvent(ev); err != nil {
			return ev, err
		}
	}
	if isControlStateEventKind(string(ev.Kind)) {
		if err := s.persistControlStateEvent(ev); err != nil {
			return ev, err
		}
	}
	if ev.Kind == "session.deleted" && ev.SessionID != "" {
		_ = os.Remove(filepath.Join(s.dataDir, "sessions", ev.SessionID+".json"))
		s.removeOverlayState(ev.SessionID)
		s.removeSessionControlState(ev.SessionID)
	}
	return ev, nil
}

func (s *store) AppendEvent(ev AstralEvent) (AstralEvent, error) {
	return s.appendEvent(ev)
}

func filterEvents(events []AstralEvent, workspaceID, sessionID string, afterSeq, beforeSeq int64) []AstralEvent {
	out := []AstralEvent{}
	for _, ev := range events {
		if afterSeq > 0 && ev.Seq <= afterSeq {
			continue
		}
		if beforeSeq > 0 && ev.Seq >= beforeSeq {
			continue
		}
		if workspaceID != "" && ev.WorkspaceID != workspaceID {
			continue
		}
		if sessionID != "" && ev.SessionID != sessionID {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func (s *store) sessionsForEventProjection(workspaceID, sessionID string) []Session {
	if sessionID != "" {
		ss, ok := s.getSession(sessionID)
		if !ok {
			return nil
		}
		if workspaceID != "" && ss.WorkspaceID != workspaceID {
			return nil
		}
		return []Session{ss}
	}
	return s.listSessions(workspaceID)
}
