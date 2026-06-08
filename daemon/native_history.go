package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimeevents "github.com/oines/astralops/daemon/internal/runtimes/events"
	"github.com/oines/astralops/pkg/protocol"
)

type nativeSessionIndex struct{}

type nativeSessionRecord struct {
	Session Session
	Path    string
}

func (nativeSessionIndex) List(workspace Workspace) []nativeSessionRecord {
	records := []nativeSessionRecord{}
	switch workspace.Target {
	case string(protocol.WorkspaceTargetLocal):
		if strings.TrimSpace(workspace.LocalCWD) == "" {
			return nil
		}
		records = append(records, listClaudeNativeSessions(workspace)...)
		records = append(records, listCodexNativeSessions(workspace)...)
	case string(protocol.WorkspaceTargetSSH):
		if strings.TrimSpace(workspace.LocalProjectionRoot) == "" {
			return nil
		}
		records = append(records, listClaudeNativeSessions(workspace)...)
	default:
		return nil
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Session.UpdatedAt == records[j].Session.UpdatedAt {
			return records[i].Session.ID < records[j].Session.ID
		}
		return records[i].Session.UpdatedAt > records[j].Session.UpdatedAt
	})
	return records
}

func listClaudeNativeSessions(workspace Workspace) []nativeSessionRecord {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	cwd := nativeDiscoveryCWD(workspace, AgentClaude)
	if cwd == "" {
		return nil
	}
	dir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cwd))
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil
	}
	out := []nativeSessionRecord{}
	for _, path := range files {
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			continue
		}
		record, ok := claudeNativeSessionRecord(workspace, cwd, path)
		if ok {
			out = append(out, record)
		}
	}
	return out
}

func claudeNativeSessionRecord(workspace Workspace, cwd, path string) (nativeSessionRecord, bool) {
	fileID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if fileID == "" {
		return nativeSessionRecord{}, false
	}
	meta := scanNativeTranscriptMeta(path, AgentClaude)
	sessionID := firstString(meta.nativeSessionID, fileID)
	createdAt := firstString(meta.createdAt, fileModTime(path))
	updatedAt := firstString(meta.updatedAt, createdAt)
	title := normalizeSessionTitleString(firstString(meta.title, sessionID))
	ss := Session{
		ID:                 stableNativeSessionID(AgentClaude, cwd, sessionID),
		WorkspaceID:        workspace.ID,
		Agent:              AgentClaude,
		Title:              title,
		Status:             string(protocol.SessionStatusIdle),
		Source:             SessionSourceDiscovered,
		ManagedByAstralOps: false,
		NativeSessionID:    sessionID,
		NativeRef: &NativeSessionRef{
			Agent:           AgentClaude,
			LocalPath:       path,
			NativeSessionID: sessionID,
			WorkspaceCWD:    cwd,
		},
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	return nativeSessionRecord{Session: ss, Path: path}, true
}

func listCodexNativeSessions(workspace Workspace) []nativeSessionRecord {
	if workspace.Target != string(protocol.WorkspaceTargetLocal) {
		return nil
	}
	cwd := nativeDiscoveryCWD(workspace, AgentCodex)
	if cwd == "" {
		return nil
	}
	index := cachedCodexNativeIndex()
	out := []nativeSessionRecord{}
	for _, entry := range index {
		if cleanLocalPath(entry.cwd) != cwd || entry.id == "" || entry.path == "" {
			continue
		}
		title := normalizeSessionTitleString(firstString(entry.title, entry.id))
		ss := Session{
			ID:                 stableNativeSessionID(AgentCodex, cwd, entry.id),
			WorkspaceID:        workspace.ID,
			Agent:              AgentCodex,
			Title:              title,
			Status:             string(protocol.SessionStatusIdle),
			Source:             SessionSourceDiscovered,
			ManagedByAstralOps: false,
			NativeThreadID:     entry.id,
			NativeRef: &NativeSessionRef{
				Agent:          AgentCodex,
				LocalPath:      entry.path,
				NativeThreadID: entry.id,
				WorkspaceCWD:   cwd,
			},
			CreatedAt: entry.createdAt,
			UpdatedAt: entry.updatedAt,
		}
		out = append(out, nativeSessionRecord{Session: ss, Path: entry.path})
	}
	return out
}

func resolveNativeRefForSession(workspace Workspace, session Session) *NativeSessionRef {
	normalizeStoredSession(&session)
	switch session.Agent {
	case AgentClaude:
		return resolveClaudeNativeRef(workspace, session)
	case AgentCodex:
		return resolveCodexNativeRef(workspace, session)
	default:
		return session.NativeRef
	}
}

func resolveClaudeNativeRef(workspace Workspace, session Session) *NativeSessionRef {
	cwd := nativeDiscoveryCWD(workspace, AgentClaude)
	if cwd == "" {
		return session.NativeRef
	}
	nativeID := firstString(session.NativeSessionID, nativeRefNativeSessionID(session.NativeRef))
	if nativeID == "" {
		return session.NativeRef
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return session.NativeRef
	}
	dir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(cwd))
	candidates := []string{filepath.Join(dir, nativeID+".jsonl")}
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	candidates = append(candidates, files...)
	for _, path := range candidates {
		if strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			continue
		}
		fileID := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if fileID != nativeID {
			meta := scanNativeTranscriptMeta(path, AgentClaude)
			if meta.nativeSessionID != nativeID {
				continue
			}
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		ref := cloneNativeRef(session.NativeRef)
		ref.Agent = AgentClaude
		ref.LocalPath = path
		ref.WorkspaceCWD = cwd
		ref.NativeSessionID = nativeID
		if workspace.Target == string(protocol.WorkspaceTargetSSH) && workspace.SSH != nil {
			ref.RemotePath = workspace.SSH.RemoteCWD
		}
		return ref
	}
	return session.NativeRef
}

func resolveCodexNativeRef(workspace Workspace, session Session) *NativeSessionRef {
	threadID := firstString(session.NativeThreadID, nativeRefNativeThreadID(session.NativeRef))
	if threadID == "" {
		return session.NativeRef
	}
	for _, entry := range cachedCodexNativeIndex() {
		if entry.id != threadID || entry.path == "" {
			continue
		}
		if workspace.Target == string(protocol.WorkspaceTargetLocal) && cleanLocalPath(entry.cwd) != cleanLocalPath(workspace.LocalCWD) {
			continue
		}
		ref := cloneNativeRef(session.NativeRef)
		ref.Agent = AgentCodex
		ref.LocalPath = entry.path
		ref.NativeThreadID = threadID
		ref.WorkspaceCWD = entry.cwd
		if workspace.Target == string(protocol.WorkspaceTargetSSH) && workspace.SSH != nil {
			ref.RemotePath = workspace.SSH.RemoteCWD
		}
		return ref
	}
	return session.NativeRef
}

func cloneNativeRef(ref *NativeSessionRef) *NativeSessionRef {
	if ref == nil {
		return &NativeSessionRef{}
	}
	copy := *ref
	return &copy
}

func nativeRefNativeSessionID(ref *NativeSessionRef) string {
	if ref == nil {
		return ""
	}
	return ref.NativeSessionID
}

func nativeRefNativeThreadID(ref *NativeSessionRef) string {
	if ref == nil {
		return ""
	}
	return ref.NativeThreadID
}

func nativeSessionTitleFromRef(ref *NativeSessionRef) string {
	if ref == nil || ref.LocalPath == "" {
		return ""
	}
	switch ref.Agent {
	case AgentClaude:
		return scanNativeTranscriptMeta(ref.LocalPath, AgentClaude).title
	case AgentCodex:
		threadID := ref.NativeThreadID
		for _, entry := range cachedCodexNativeIndex() {
			if entry.path == ref.LocalPath || (threadID != "" && entry.id == threadID) {
				if title := normalizeSessionTitleString(entry.title); title != "" {
					return title
				}
			}
		}
		return scanNativeTranscriptMeta(ref.LocalPath, AgentCodex).title
	default:
		return ""
	}
}

func nativeDiscoveryCWD(workspace Workspace, agent AgentKind) string {
	switch workspace.Target {
	case string(protocol.WorkspaceTargetLocal):
		return cleanLocalPath(workspace.LocalCWD)
	case string(protocol.WorkspaceTargetSSH):
		if agent == AgentClaude {
			return cleanLocalPath(workspace.LocalProjectionRoot)
		}
		return ""
	default:
		return ""
	}
}

type codexNativeEntry struct {
	id        string
	cwd       string
	path      string
	title     string
	titleRank int
	createdAt string
	updatedAt string
}

type codexNativeTitle struct {
	title     string
	updatedAt string
}

var codexNativeIndexCache = struct {
	sync.Mutex
	loadedAt  time.Time
	signature string
	entries   []codexNativeEntry
}{}

const codexNativeIndexCacheTTL = 30 * time.Second
const nativeMetaHeadScanLimit = 200
const nativeMetaTailBytes = 512 * 1024

func cachedCodexNativeIndex() []codexNativeEntry {
	codexNativeIndexCache.Lock()
	defer codexNativeIndexCache.Unlock()
	if time.Since(codexNativeIndexCache.loadedAt) < codexNativeIndexCacheTTL {
		return append([]codexNativeEntry(nil), codexNativeIndexCache.entries...)
	}
	signature := codexNativeIndexSignature()
	if signature != "" && signature == codexNativeIndexCache.signature {
		codexNativeIndexCache.loadedAt = time.Now()
		return append([]codexNativeEntry(nil), codexNativeIndexCache.entries...)
	}
	entries := loadCodexNativeIndex()
	codexNativeIndexCache.entries = entries
	codexNativeIndexCache.signature = signature
	codexNativeIndexCache.loadedAt = time.Now()
	return append([]codexNativeEntry(nil), entries...)
}

func codexNativeIndexSignature() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	parts := []string{}
	for _, root := range []string{
		filepath.Join(home, ".codex", "session_index.jsonl"),
		filepath.Join(home, ".codex", "sessions"),
		filepath.Join(home, ".codex", "archived_sessions"),
	} {
		if strings.HasSuffix(root, ".jsonl") {
			if fp := nativeTranscriptFingerprint(root); fp != "" {
				parts = append(parts, fp)
			}
			continue
		}
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			if fp := nativeTranscriptFingerprint(path); fp != "" {
				parts = append(parts, fp)
			}
			return nil
		})
	}
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

func loadCodexNativeIndex() []codexNativeEntry {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	titles := loadCodexSessionTitles(filepath.Join(home, ".codex", "session_index.jsonl"))
	files := []string{}
	for _, root := range []string{
		filepath.Join(home, ".codex", "sessions"),
		filepath.Join(home, ".codex", "archived_sessions"),
	} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			files = append(files, path)
			return nil
		})
	}
	out := []codexNativeEntry{}
	for _, path := range files {
		entry, ok := codexNativeSessionEntry(path)
		if !ok {
			continue
		}
		if title, ok := titles[entry.id]; ok {
			if normalizedTitle := normalizeSessionTitleString(title.title); normalizedTitle != "" {
				entry.title = normalizedTitle
				entry.titleRank = 50
			}
			entry.updatedAt = firstString(title.updatedAt, entry.updatedAt)
		}
		if entry.updatedAt == "" {
			entry.updatedAt = fileModTime(path)
		}
		if entry.createdAt == "" {
			entry.createdAt = entry.updatedAt
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].updatedAt > out[j].updatedAt })
	return out
}

func loadCodexSessionTitles(path string) map[string]codexNativeTitle {
	out := map[string]codexNativeTitle{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		id := stringValue(raw["id"])
		if id == "" {
			continue
		}
		out[id] = codexNativeTitle{
			title:     stringValue(raw["thread_name"]),
			updatedAt: nativeTimeString(raw["updated_at"]),
		}
	}
	return out
}

func codexNativeSessionEntry(path string) (codexNativeEntry, bool) {
	f, err := os.Open(path)
	if err != nil {
		return codexNativeEntry{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineCount := 0
	var id string
	var cwd string
	var firstUser string
	var title string
	var titleRank int
	var createdAt string
	var updatedAt string
	for scanner.Scan() {
		lineCount++
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		ts := nativeTimeString(raw["timestamp"])
		if createdAt == "" {
			createdAt = ts
		}
		if ts != "" {
			updatedAt = ts
		}
		payload := mapValue(raw["payload"])
		if stringValue(raw["type"]) == "session_meta" {
			id = stringValue(payload["id"])
			cwd = stringValue(payload["cwd"])
			updateNativeTitleCandidate(firstString(payload["thread_name"], payload["threadName"], payload["name"]), 40, &title, &titleRank)
			if id == "" || cwd == "" {
				return codexNativeEntry{}, false
			}
		}
		if firstUser == "" && stringValue(raw["type"]) == "response_item" {
			if stringValue(payload["type"]) == "message" && stringValue(payload["role"]) == "user" {
				firstUser = codexUserMessageText(payload)
				updateNativeTitleCandidate(firstUser, 10, &title, &titleRank)
			}
		}
		if stringValue(raw["type"]) == "event_msg" && stringValue(payload["type"]) == "thread_name_updated" {
			updateNativeTitleCandidate(stringValue(payload["thread_name"]), 40, &title, &titleRank)
		}
		if id != "" && cwd != "" && titleRank >= 40 {
			break
		}
		if lineCount > 200 {
			break
		}
	}
	if id == "" || cwd == "" {
		return codexNativeEntry{}, false
	}
	return codexNativeEntry{
		id:        id,
		cwd:       cwd,
		path:      path,
		title:     firstString(title, firstUser),
		titleRank: titleRank,
		createdAt: firstString(createdAt, fileModTime(path)),
		updatedAt: firstString(updatedAt, createdAt, fileModTime(path)),
	}, true
}

type nativeTranscriptMeta struct {
	nativeSessionID string
	nativeThreadID  string
	title           string
	titleRank       int
	createdAt       string
	updatedAt       string
}

type nativeTranscriptMetaCacheEntry struct {
	fingerprint string
	meta        nativeTranscriptMeta
}

type nativeTranscriptEventsCacheEntry struct {
	fingerprint string
	events      []AstralEvent
}

var nativeTranscriptMetaCache = struct {
	sync.Mutex
	entries map[string]nativeTranscriptMetaCacheEntry
}{entries: map[string]nativeTranscriptMetaCacheEntry{}}

var nativeTranscriptEventsCache = struct {
	sync.Mutex
	entries map[string]nativeTranscriptEventsCacheEntry
}{entries: map[string]nativeTranscriptEventsCacheEntry{}}

func scanNativeTranscriptMeta(path string, agent AgentKind) nativeTranscriptMeta {
	fingerprint := nativeTranscriptFingerprint(path)
	if fingerprint == "" {
		return nativeTranscriptMeta{}
	}
	cacheKey := string(agent) + "\x00" + path
	nativeTranscriptMetaCache.Lock()
	if cached, ok := nativeTranscriptMetaCache.entries[cacheKey]; ok && cached.fingerprint == fingerprint {
		nativeTranscriptMetaCache.Unlock()
		return cached.meta
	}
	nativeTranscriptMetaCache.Unlock()

	meta := scanNativeTranscriptMetaUncached(path, agent)
	nativeTranscriptMetaCache.Lock()
	nativeTranscriptMetaCache.entries[cacheKey] = nativeTranscriptMetaCacheEntry{fingerprint: fingerprint, meta: meta}
	nativeTranscriptMetaCache.Unlock()
	return meta
}

func scanNativeTranscriptMetaUncached(path string, agent AgentKind) nativeTranscriptMeta {
	f, err := os.Open(path)
	if err != nil {
		return nativeTranscriptMeta{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	meta := nativeTranscriptMeta{}
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		applyNativeTranscriptMetaLine(raw, agent, &meta)
		if meta.titleRank >= 50 && meta.updatedAt != "" && (meta.nativeSessionID != "" || meta.nativeThreadID != "") {
			break
		}
		if lineCount > nativeMetaHeadScanLimit {
			break
		}
	}
	scanNativeTranscriptTailMeta(path, agent, &meta)
	return meta
}

func applyNativeTranscriptMetaLine(raw map[string]any, agent AgentKind, meta *nativeTranscriptMeta) {
	ts := nativeTimeString(raw["timestamp"])
	if meta.createdAt == "" {
		meta.createdAt = ts
	}
	if ts != "" {
		meta.updatedAt = ts
	}
	switch agent {
	case AgentClaude:
		recordType := stringValue(raw["type"])
		if meta.nativeSessionID == "" {
			meta.nativeSessionID = stringValue(raw["sessionId"])
		}
		switch recordType {
		case "user":
			updateNativeTitleCandidate(claudeUserMessageText(raw), 10, &meta.title, &meta.titleRank)
		case "last-prompt":
			updateNativeTitleCandidate(stringValue(raw["lastPrompt"]), 20, &meta.title, &meta.titleRank)
		case "system":
			updateNativeTitleCandidate(firstString(raw["customTitle"], raw["aiTitle"], raw["title"], raw["summary"], raw["firstPrompt"]), 30, &meta.title, &meta.titleRank)
		case "ai-title":
			updateNativeTitleCandidate(stringValue(raw["aiTitle"]), 30, &meta.title, &meta.titleRank)
		case "custom-title":
			updateNativeTitleCandidate(stringValue(raw["customTitle"]), 50, &meta.title, &meta.titleRank)
		case "agent-name":
			updateNativeTitleCandidate(stringValue(raw["agentName"]), 50, &meta.title, &meta.titleRank)
		}
	case AgentCodex:
		payload := mapValue(raw["payload"])
		if stringValue(raw["type"]) == "session_meta" {
			meta.nativeThreadID = stringValue(payload["id"])
			updateNativeTitleCandidate(firstString(payload["thread_name"], payload["threadName"], payload["name"]), 40, &meta.title, &meta.titleRank)
		}
		if stringValue(raw["type"]) == "response_item" && stringValue(payload["type"]) == "message" && stringValue(payload["role"]) == "user" {
			updateNativeTitleCandidate(codexUserMessageText(payload), 10, &meta.title, &meta.titleRank)
		}
		if stringValue(raw["type"]) == "event_msg" && stringValue(payload["type"]) == "thread_name_updated" {
			updateNativeTitleCandidate(stringValue(payload["thread_name"]), 40, &meta.title, &meta.titleRank)
		}
	}
}

func scanNativeTranscriptTailMeta(path string, agent AgentKind, meta *nativeTranscriptMeta) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 {
		return
	}
	size := info.Size()
	readSize := int64(nativeMetaTailBytes)
	if size < readSize {
		readSize = size
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	buf := make([]byte, readSize)
	if _, err := f.ReadAt(buf, size-readSize); err != nil && !errors.Is(err, io.EOF) {
		return
	}
	text := string(buf)
	if size > readSize {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			text = text[index+1:]
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		applyNativeTranscriptMetaLine(raw, agent, meta)
	}
}

func updateNativeTitleCandidate(candidate string, rank int, current *string, currentRank *int) {
	text := normalizeSessionTitleString(candidate)
	if text == "" {
		return
	}
	if rank <= 10 && shouldSkipSessionTitleText(text) {
		return
	}
	if *current != "" && *currentRank > rank {
		return
	}
	if *current != "" && *currentRank == rank && rank <= 10 {
		return
	}
	*current = text
	*currentRank = rank
}

func readNativeTranscriptEvents(session Session) []AstralEvent {
	if session.NativeRef == nil || session.NativeRef.LocalPath == "" {
		return nil
	}
	fingerprint := nativeTranscriptFingerprint(session.NativeRef.LocalPath)
	if fingerprint == "" {
		return nil
	}
	cacheKey := strings.Join([]string{
		session.ID,
		session.WorkspaceID,
		string(session.Agent),
		string(session.NativeRef.Agent),
		session.NativeRef.LocalPath,
		session.NativeRef.NativeSessionID,
		session.NativeRef.NativeThreadID,
	}, "\x00")
	nativeTranscriptEventsCache.Lock()
	if cached, ok := nativeTranscriptEventsCache.entries[cacheKey]; ok && cached.fingerprint == fingerprint {
		nativeTranscriptEventsCache.Unlock()
		return copyAstralEvents(cached.events)
	}
	nativeTranscriptEventsCache.Unlock()

	var events []AstralEvent
	switch session.NativeRef.Agent {
	case AgentClaude:
		events = readClaudeNativeTranscript(session)
	case AgentCodex:
		events = readCodexNativeTranscript(session)
	default:
		return nil
	}
	nativeTranscriptEventsCache.Lock()
	nativeTranscriptEventsCache.entries[cacheKey] = nativeTranscriptEventsCacheEntry{fingerprint: fingerprint, events: copyAstralEvents(events)}
	nativeTranscriptEventsCache.Unlock()
	return events
}

func copyAstralEvents(events []AstralEvent) []AstralEvent {
	if len(events) == 0 {
		return nil
	}
	return append([]AstralEvent(nil), events...)
}

func readClaudeNativeTranscript(session Session) []AstralEvent {
	path := session.NativeRef.LocalPath
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	out := []AstralEvent{}
	seq := int64(0)
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			events := normalizeClaudeNativeHistoryLine(session, line)
			for _, ev := range events {
				if ev.SessionID == "" {
					ev.SessionID = session.ID
				}
				if ev.WorkspaceID == "" {
					ev.WorkspaceID = session.WorkspaceID
				}
				if ev.Agent == "" {
					ev.Agent = AgentClaude
				}
				if ev.TS == "" {
					ev.TS = nativeLineTimestamp(line)
				}
				seq++
				ev.Seq = seq
				ev.Raw = nil
				out = append(out, ev)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			break
		}
	}
	return synthesizeNativeTurnBoundaries(session, out)
}

func normalizeClaudeNativeHistoryLine(session Session, line []byte) []AstralEvent {
	var raw map[string]any
	if json.Unmarshal(line, &raw) == nil {
		switch stringValue(raw["type"]) {
		case "assistant":
			if isClaudeNativeAPIError(raw) {
				return []AstralEvent{nativeEvent(session, "turn.failed", map[string]any{
					"source":  "claude",
					"status":  "failed",
					"message": claudeNativeAPIErrorMessage(raw),
					"reason":  "api_error",
				})}
			}
			return normalizeClaudeNativeAssistantLine(session, raw, line)
		case "user":
			if text := claudeUserMessageText(raw); text != "" {
				if normalized := nativeUserMessageNormalized("claude", text); len(normalized) > 0 {
					return []AstralEvent{nativeEvent(session, "message.user", normalized)}
				}
			}
		case "attachment":
			return normalizeClaudeNativeAttachment(session, raw)
		case "queue-operation":
			return normalizeClaudeNativeQueueOperation(session, raw)
		case "permission-mode":
			return []AstralEvent{nativeEvent(session, "control.status", map[string]any{
				"source":          "claude",
				"kind":            "permission_mode",
				"status":          stringValue(raw["permissionMode"]),
				"permission_mode": stringValue(raw["permissionMode"]),
			})}
		case "ai-title", "custom-title", "agent-name":
			return []AstralEvent{nativeEvent(session, "session.updated", map[string]any{
				"source": "claude",
				"type":   stringValue(raw["type"]),
				"title":  firstString(raw["customTitle"], raw["aiTitle"], raw["agentName"]),
				"name":   firstString(raw["customTitle"], raw["aiTitle"], raw["agentName"]),
			})}
		case "last-prompt":
			return []AstralEvent{nativeEvent(session, "session.updated", map[string]any{
				"source":      "claude",
				"type":        "last-prompt",
				"title":       stringValue(raw["lastPrompt"]),
				"name":        stringValue(raw["lastPrompt"]),
				"last_prompt": stringValue(raw["lastPrompt"]),
			})}
		}
	}
	return runtimeevents.NormalizeClaudeStreamJSON(session, line)
}

func normalizeClaudeNativeAssistantLine(session Session, raw map[string]any, line []byte) []AstralEvent {
	events := runtimeevents.NormalizeClaudeStreamJSON(session, line)
	text := claudeMessageText(raw)
	if text == "" {
		return withClaudeNativeAssistantIdentity(events, raw)
	}
	for _, event := range events {
		if event.Kind == protocol.AstralEventKindMessageAssistant {
			return withClaudeNativeAssistantIdentity(events, raw)
		}
	}
	out := make([]AstralEvent, 0, len(events))
	inserted := false
	assistantNormalized := claudeNativeAssistantNormalized(raw, text)
	for _, event := range events {
		if event.Kind == protocol.AstralEventKindMessageDelta {
			if !inserted {
				out = append(out, nativeEvent(session, "message.assistant", assistantNormalized))
				inserted = true
			}
			continue
		}
		out = append(out, event)
	}
	if !inserted {
		out = append(out, nativeEvent(session, "message.assistant", assistantNormalized))
	}
	return out
}

func withClaudeNativeAssistantIdentity(events []AstralEvent, raw map[string]any) []AstralEvent {
	nativeUUID := claudeNativeAssistantUUID(raw)
	if nativeUUID == "" {
		return events
	}
	out := append([]AstralEvent(nil), events...)
	for i, event := range out {
		if event.Kind != protocol.AstralEventKindMessageAssistant {
			continue
		}
		normalized := mapValue(event.Normalized)
		if stringValue(normalized["native_message_uuid"]) == "" {
			normalized["native_message_uuid"] = nativeUUID
		}
		if stringValue(normalized["message_id"]) == "" {
			normalized["message_id"] = nativeUUID
		}
		out[i].Normalized = eventNormalized(event.Kind, normalized)
	}
	return out
}

func claudeNativeAssistantNormalized(raw map[string]any, text string) map[string]any {
	normalized := map[string]any{
		"source": "claude",
		"text":   text,
	}
	if nativeUUID := claudeNativeAssistantUUID(raw); nativeUUID != "" {
		normalized["native_message_uuid"] = nativeUUID
		normalized["message_id"] = nativeUUID
	}
	return normalized
}

func claudeNativeAssistantUUID(raw map[string]any) string {
	message := mapValue(raw["message"])
	return firstString(raw["uuid"], message["id"])
}

func normalizeClaudeNativeAttachment(session Session, raw map[string]any) []AstralEvent {
	attachment := mapValue(raw["attachment"])
	switch stringValue(attachment["type"]) {
	case "file":
		content := mapValue(attachment["content"])
		file := mapValue(content["file"])
		path := firstString(file["filePath"], file["file_path"], attachment["filename"], attachment["displayPath"])
		if path == "" {
			return nil
		}
		resultFile := map[string]any{
			"filePath":   path,
			"file_path":  path,
			"path":       path,
			"content":    firstString(file["content"], content["content"]),
			"startLine":  firstPositiveNumber(file["startLine"], file["start_line"], 1),
			"totalLines": firstPositiveNumber(file["totalLines"], file["total_lines"], file["numLines"], file["num_lines"]),
		}
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "claude",
			"id":       firstString(raw["uuid"], path),
			"name":     "Read",
			"category": "read",
			"status":   "completed",
			"result": map[string]any{
				"structuredContent": map[string]any{
					"file": resultFile,
				},
			},
			"text": resultFile["content"],
		})}
	case "goal_status":
		status := "active"
		if boolValue(attachment["met"]) {
			status = "complete"
		}
		return []AstralEvent{nativeEvent(session, "control.status", map[string]any{
			"source":  "claude",
			"kind":    "goal",
			"status":  status,
			"message": attachment["condition"],
		})}
	case "budget_usd":
		return []AstralEvent{nativeEvent(session, "control.status", map[string]any{
			"source":  "claude",
			"kind":    "budget",
			"status":  "updated",
			"message": "Budget updated",
		})}
	default:
		return nil
	}
}

func firstPositiveNumber(values ...any) float64 {
	for _, value := range values {
		if number := numberValue(value); number > 0 {
			return number
		}
	}
	return 0
}

func normalizeClaudeNativeQueueOperation(session Session, raw map[string]any) []AstralEvent {
	operation := stringValue(raw["operation"])
	text := stringValue(raw["content"])
	queueID := firstString(raw["uuid"], raw["timestamp"], text)
	switch operation {
	case "enqueue":
		return []AstralEvent{nativeEvent(session, "queue.queued", map[string]any{
			"queue_id": queueID,
			"text":     text,
		})}
	case "dequeue":
		return []AstralEvent{nativeEvent(session, "queue.dequeued", map[string]any{
			"queue_id": queueID,
			"text":     text,
			"internal": true,
		})}
	case "cancel":
		return []AstralEvent{nativeEvent(session, "queue.cancelled", map[string]any{
			"queue_id": queueID,
			"text":     text,
			"reason":   firstString(raw["reason"], "cancelled"),
		})}
	default:
		return nil
	}
}

func readCodexNativeTranscript(session Session) []AstralEvent {
	path := session.NativeRef.LocalPath
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	records := []map[string]any{}
	assistantResponseTexts := map[string]bool{}
	userResponseTexts := map[string]bool{}
	toolEndByCallID := map[string]map[string]any{}
	responseToolOutputCallIDs := map[string]bool{}
	for scanner.Scan() {
		var raw map[string]any
		if json.Unmarshal(scanner.Bytes(), &raw) != nil {
			continue
		}
		records = append(records, raw)
		if text := codexNativeAssistantResponseText(raw); text != "" {
			assistantResponseTexts[codexTranscriptTextKey(text)] = true
		}
		if text := codexNativeUserResponseText(raw); text != "" {
			userResponseTexts[codexTranscriptTextKey(text)] = true
		}
		if callID := codexResponseItemToolOutputCallID(raw); callID != "" {
			responseToolOutputCallIDs[callID] = true
		}
		if payload := codexNativeToolEndPayload(raw); len(payload) > 0 {
			if callID := stringValue(payload["call_id"]); callID != "" {
				toolEndByCallID[callID] = payload
			}
		}
	}

	out := []AstralEvent{}
	seq := int64(0)
	for _, raw := range records {
		if text := codexNativeAgentMessageText(raw); text != "" && assistantResponseTexts[codexTranscriptTextKey(text)] {
			continue
		}
		if text := codexNativeEventUserMessageText(raw); text != "" && userResponseTexts[codexTranscriptTextKey(text)] {
			continue
		}
		if payload := codexNativeToolEndPayload(raw); len(payload) > 0 && responseToolOutputCallIDs[stringValue(payload["call_id"])] {
			continue
		}
		for _, ev := range normalizeCodexNativeHistoryEvent(session, raw, toolEndByCallID) {
			seq++
			ev.Seq = seq
			if ev.TS == "" {
				ev.TS = nativeTimeString(raw["timestamp"])
			}
			ev.Raw = nil
			out = append(out, ev)
		}
	}
	return synthesizeNativeTurnBoundaries(session, dedupeCodexNativeTranscriptEvents(out))
}

func synthesizeNativeTurnBoundaries(session Session, events []AstralEvent) []AstralEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]AstralEvent, 0, len(events)+8)
	turnOpen := false
	turnHasContent := false
	turnIndex := 0
	turnID := ""
	lastTurnTS := ""

	openTurn := func(seed AstralEvent) {
		if turnOpen {
			return
		}
		turnIndex++
		turnID = firstString(nativeEventTurnID(seed), fmt.Sprintf("native-%d", turnIndex))
		out = append(out, nativeSyntheticTurnEvent(session, "turn.started", turnID, "running", seed.TS))
		turnOpen = true
		turnHasContent = false
		lastTurnTS = seed.TS
	}
	closeTurn := func(kind string, ts string) {
		if !turnOpen {
			return
		}
		if !turnHasContent {
			turnOpen = false
			turnID = ""
			lastTurnTS = ""
			return
		}
		status := "idle"
		if kind == "turn.failed" {
			status = "failed"
		} else if kind == "turn.cancelled" {
			status = "cancelled"
		}
		out = append(out, nativeSyntheticTurnEvent(session, kind, turnID, status, firstString(ts, lastTurnTS)))
		turnOpen = false
		turnHasContent = false
		turnID = ""
		lastTurnTS = ""
	}

	for _, event := range events {
		switch event.Kind {
		case protocol.AstralEventKindTurnStarted:
			if turnOpen && turnHasContent {
				closeTurn("turn.completed", lastTurnTS)
			}
			turnOpen = true
			turnHasContent = false
			turnID = firstString(nativeEventTurnID(event), fmt.Sprintf("native-%d", turnIndex+1))
			if nativeEventTurnID(event) == "" {
				normalized := mapValue(event.Normalized)
				normalized["turn_id"] = turnID
				event.Normalized = eventNormalized(event.Kind, normalized)
			}
			turnIndex++
			lastTurnTS = event.TS
			out = append(out, event)
		case protocol.AstralEventKindMessageUser:
			if turnOpen && turnHasContent {
				closeTurn("turn.completed", lastTurnTS)
			}
			openTurn(event)
			turnHasContent = true
			lastTurnTS = event.TS
			out = append(out, event)
		case protocol.AstralEventKindTurnCompleted, protocol.AstralEventKindTurnFailed, protocol.AstralEventKindTurnCancelled:
			if !turnOpen {
				openTurn(event)
			}
			if nativeEventTurnID(event) == "" && turnID != "" {
				normalized := mapValue(event.Normalized)
				normalized["turn_id"] = turnID
				event.Normalized = eventNormalized(event.Kind, normalized)
			}
			turnHasContent = true
			lastTurnTS = event.TS
			out = append(out, event)
			turnOpen = false
			turnHasContent = false
			turnID = ""
			lastTurnTS = ""
		default:
			if nativeTurnContentKind(event.Kind) {
				openTurn(event)
				turnHasContent = true
				lastTurnTS = event.TS
			} else if turnOpen && turnHasContent {
				closeTurn("turn.completed", lastTurnTS)
			}
			out = append(out, event)
		}
	}
	if turnOpen && turnHasContent {
		closeTurn("turn.completed", lastTurnTS)
	}
	for i := range out {
		out[i].Seq = int64(i + 1)
	}
	return out
}

func nativeSyntheticTurnEvent(session Session, kind string, turnID string, status string, ts string) AstralEvent {
	event := nativeEvent(session, kind, map[string]any{
		"source":  string(session.Agent),
		"turn_id": turnID,
		"status":  status,
	})
	event.TS = ts
	return event
}

func nativeEventTurnID(event AstralEvent) string {
	return stringValue(mapValue(event.Normalized)["turn_id"])
}

func nativeTurnContentKind(kind protocol.AstralEventKind) bool {
	switch kind {
	case protocol.AstralEventKindMessageAssistant,
		protocol.AstralEventKindMessageDelta,
		protocol.AstralEventKindMessageMedia,
		protocol.AstralEventKindReasoningStarted,
		protocol.AstralEventKindReasoningDelta,
		protocol.AstralEventKindReasoningCompleted,
		protocol.AstralEventKindPlanDelta,
		protocol.AstralEventKindPlanUpdated,
		protocol.AstralEventKindToolStarted,
		protocol.AstralEventKindToolProgress,
		protocol.AstralEventKindToolOutputDelta,
		protocol.AstralEventKindToolCompleted,
		protocol.AstralEventKindToolDiff,
		protocol.AstralEventKindToolTodo,
		protocol.AstralEventKindApprovalRequested,
		protocol.AstralEventKindApprovalResolved,
		protocol.AstralEventKindAskRequested,
		protocol.AstralEventKindAskResolved,
		protocol.AstralEventKindControlContext,
		protocol.AstralEventKindControlError,
		protocol.AstralEventKindControlWarning,
		protocol.AstralEventKindControlRateLimit,
		protocol.AstralEventKindHookStarted,
		protocol.AstralEventKindHookProgress,
		protocol.AstralEventKindHookCompleted:
		return true
	default:
		return false
	}
}

func codexNativeAssistantResponseText(raw map[string]any) string {
	if stringValue(raw["type"]) != "response_item" {
		return ""
	}
	payload := mapValue(raw["payload"])
	if stringValue(payload["type"]) != "message" || stringValue(payload["role"]) != "assistant" {
		return ""
	}
	return codexMessageText(payload)
}

func codexNativeUserResponseText(raw map[string]any) string {
	if stringValue(raw["type"]) != "response_item" {
		return ""
	}
	payload := mapValue(raw["payload"])
	if stringValue(payload["type"]) != "message" || stringValue(payload["role"]) != "user" {
		return ""
	}
	return codexUserMessageText(payload)
}

func codexNativeAgentMessageText(raw map[string]any) string {
	if stringValue(raw["type"]) != "event_msg" {
		return ""
	}
	payload := mapValue(raw["payload"])
	if stringValue(payload["type"]) != "agent_message" {
		return ""
	}
	return stringValue(payload["message"])
}

func codexNativeEventUserMessageText(raw map[string]any) string {
	if stringValue(raw["type"]) != "event_msg" {
		return ""
	}
	payload := mapValue(raw["payload"])
	if stringValue(payload["type"]) != "user_message" {
		return ""
	}
	text := stringValue(payload["message"])
	if isCodexHiddenUserContext(text) {
		return ""
	}
	visible, _ := splitNativeAttachmentManifest(text)
	return visible
}

func codexTranscriptTextKey(text string) string {
	return strings.TrimSpace(text)
}

func codexResponseItemToolOutputCallID(raw map[string]any) string {
	if stringValue(raw["type"]) != "response_item" {
		return ""
	}
	payload := mapValue(raw["payload"])
	switch stringValue(payload["type"]) {
	case "function_call_output", "custom_tool_call_output", "tool_search_output":
		return stringValue(payload["call_id"])
	default:
		return ""
	}
}

func codexNativeToolEndPayload(raw map[string]any) map[string]any {
	if stringValue(raw["type"]) != "event_msg" {
		return nil
	}
	payload := mapValue(raw["payload"])
	if !isCodexNativeToolEndType(stringValue(payload["type"])) {
		return nil
	}
	return payload
}

func isCodexNativeToolEndType(kind string) bool {
	switch kind {
	case "exec_command_end", "patch_apply_end", "mcp_tool_call_end", "web_search_end":
		return true
	default:
		return false
	}
}

func dedupeCodexNativeTranscriptEvents(events []AstralEvent) []AstralEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]AstralEvent, 0, len(events))
	for _, event := range events {
		if len(out) > 0 && isDuplicateCodexNativeAssistantMessage(out[len(out)-1], event) {
			out[len(out)-1] = event
			continue
		}
		out = append(out, event)
	}
	for index := range out {
		out[index].Seq = int64(index + 1)
	}
	return out
}

func isDuplicateCodexNativeAssistantMessage(previous, next AstralEvent) bool {
	if previous.Agent != AgentCodex || next.Agent != AgentCodex {
		return false
	}
	if previous.Kind != protocol.AstralEventKindMessageAssistant || next.Kind != protocol.AstralEventKindMessageAssistant {
		return false
	}
	previousText := strings.TrimSpace(stringValue(mapValue(previous.Normalized)["text"]))
	nextText := strings.TrimSpace(stringValue(mapValue(next.Normalized)["text"]))
	return previousText != "" && previousText == nextText
}

func normalizeCodexNativeHistoryEvent(session Session, raw map[string]any, toolEndByCallID map[string]map[string]any) []AstralEvent {
	rawType := stringValue(raw["type"])
	payload := mapValue(raw["payload"])
	switch rawType {
	case "session_meta":
		return []AstralEvent{nativeEvent(session, "session.native", map[string]any{
			"source":           "codex",
			"native_thread_id": stringValue(payload["id"]),
			"name":             payload["originator"],
		})}
	case "event_msg":
		return normalizeCodexNativeEventMsg(session, payload)
	case "response_item":
		return normalizeCodexNativeResponseItem(session, payload, toolEndByCallID)
	case "compacted":
		return []AstralEvent{nativeEvent(session, "memory.compacted", map[string]any{
			"source":   "codex",
			"metadata": payload["metadata"],
		})}
	default:
		return nil
	}
}

func normalizeCodexNativeEventMsg(session Session, payload map[string]any) []AstralEvent {
	switch stringValue(payload["type"]) {
	case "task_started":
		return []AstralEvent{nativeEvent(session, "turn.started", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(payload["turn_id"]),
			"status":  "running",
		})}
	case "task_complete", "task_completed":
		return []AstralEvent{nativeEvent(session, "turn.completed", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(payload["turn_id"]),
			"status":  "idle",
		})}
	case "turn_aborted":
		return []AstralEvent{nativeEvent(session, "turn.cancelled", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(payload["turn_id"]),
			"status":  "cancelled",
			"reason":  firstString(payload["reason"], "aborted"),
		})}
	case "user_message":
		text := stringValue(payload["message"])
		if text != "" && !isCodexHiddenUserContext(text) {
			if normalized := nativeUserMessageNormalized("codex", text); len(normalized) > 0 {
				return []AstralEvent{nativeEvent(session, "message.user", normalized)}
			}
		}
	case "agent_message":
		if text := stringValue(payload["message"]); text != "" {
			return []AstralEvent{nativeEvent(session, "message.assistant", map[string]any{
				"source": "codex",
				"text":   text,
			})}
		}
	case "agent_reasoning":
		if text := stringValue(payload["text"]); text != "" {
			return []AstralEvent{nativeEvent(session, "reasoning.completed", map[string]any{
				"source": "codex",
				"text":   text,
				"status": "completed",
			})}
		}
	case "item_completed":
		return normalizeCodexNativeItemCompleted(session, payload)
	case "context_compacted":
		return []AstralEvent{nativeEvent(session, "memory.compacted", map[string]any{
			"source": "codex",
		})}
	case "thread_name_updated":
		return []AstralEvent{nativeEvent(session, "session.updated", map[string]any{
			"source":      "codex",
			"thread_name": payload["thread_name"],
			"name":        payload["thread_name"],
		})}
	case "thread_goal_updated":
		goal := mapValue(payload["goal"])
		return []AstralEvent{nativeEvent(session, "control.status", map[string]any{
			"source":  "codex",
			"kind":    "thread_goal",
			"status":  firstString(goal["status"], "updated"),
			"message": goal["objective"],
		})}
	case "thread_rolled_back":
		return []AstralEvent{nativeEvent(session, "control.status", map[string]any{
			"source":  "codex",
			"kind":    "thread_rollback",
			"status":  "completed",
			"message": "Rolled back " + strconv.FormatInt(int64(numberValue(payload["num_turns"])), 10) + " turn(s)",
		})}
	case "error":
		return []AstralEvent{nativeEvent(session, "control.error", map[string]any{
			"source":  "codex",
			"code":    firstString(payload["codex_error_info"], "codex_error"),
			"status":  "failed",
			"message": firstString(payload["message"], "Codex error"),
			"details": map[string]any{
				"codex_error_info": payload["codex_error_info"],
			},
		})}
	case "view_image_tool_call":
		return normalizeCodexNativeViewImage(session, payload)
	case "image_generation_end":
		return normalizeCodexNativeImageGenerationEnd(session, payload)
	case "token_count":
		info := mapValue(payload["info"])
		total := mapValue(info["total_token_usage"])
		last := mapValue(info["last_token_usage"])
		normalized := map[string]any{
			"source":                                 "codex",
			"token_usage":                            total,
			"last":                                   last,
			"cumulative_total_tokens":                total["total_tokens"],
			"cumulative_input_tokens":                total["input_tokens"],
			"cumulative_output_tokens":               total["output_tokens"],
			"cumulative_cached_input_tokens":         total["cached_input_tokens"],
			"cumulative_cache_creation_input_tokens": total["cache_creation_input_tokens"],
			"model_context_window":                   info["model_context_window"],
		}
		if len(last) > 0 {
			normalized["scope"] = "current"
			normalized["total_tokens"] = last["total_tokens"]
			normalized["input_tokens"] = last["input_tokens"]
			normalized["cached_input_tokens"] = last["cached_input_tokens"]
			normalized["output_tokens"] = last["output_tokens"]
			normalized["reasoning_tokens"] = last["reasoning_output_tokens"]
			if percent := contextUsedPercent(normalized); percent > 0 {
				normalized["used_percent"] = percent
			}
		} else {
			normalized["scope"] = "aggregate"
		}
		return []AstralEvent{nativeEvent(session, "control.context", normalized)}
	}
	if isCodexNativeToolEndType(stringValue(payload["type"])) {
		return normalizeCodexNativeToolEnd(session, payload)
	}
	return nil
}

func normalizeCodexNativeResponseItem(session Session, payload map[string]any, toolEndByCallID map[string]map[string]any) []AstralEvent {
	switch stringValue(payload["type"]) {
	case "message":
		role := stringValue(payload["role"])
		switch role {
		case "user":
			normalized := codexUserMessageNormalized(payload)
			if len(normalized) == 0 {
				return nil
			}
			return []AstralEvent{nativeEvent(session, "message.user", normalized)}
		case "assistant":
			text := codexMessageText(payload)
			if text == "" {
				return nil
			}
			normalized := map[string]any{
				"source": "codex",
				"text":   text,
			}
			if id := stringValue(payload["id"]); id != "" {
				normalized["item_id"] = id
			}
			return []AstralEvent{nativeEvent(session, "message.assistant", normalized)}
		}
	case "function_call":
		input := codexFunctionCallInput(payload["arguments"])
		name := stringValue(payload["name"])
		if name == "update_plan" {
			inputMap := mapValue(input)
			return []AstralEvent{nativeEvent(session, "tool.todo", map[string]any{
				"source":   "codex",
				"id":       stringValue(payload["call_id"]),
				"name":     name,
				"category": "todo",
				"input":    input,
				"todos":    inputMap["plan"],
				"status":   "updated",
			})}
		}
		normalized := map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     name,
			"category": codexNativeToolCategory(name),
			"input":    input,
		}
		inputMap := mapValue(input)
		if command := firstString(inputMap["cmd"], inputMap["command"]); command != "" {
			normalized["command"] = command
		}
		if cwd := stringValue(inputMap["workdir"]); cwd != "" {
			normalized["cwd"] = cwd
		}
		return []AstralEvent{nativeEvent(session, "tool.started", normalized)}
	case "function_call_output":
		if strings.TrimSpace(stringValue(payload["output"])) == "Plan updated" {
			return nil
		}
		if end := toolEndByCallID[stringValue(payload["call_id"])]; len(end) > 0 {
			return normalizeCodexNativeToolEnd(session, end)
		}
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"category": "command",
			"status":   "completed",
			"result":   payload["output"],
			"text":     stringValue(payload["output"]),
		})}
	case "custom_tool_call":
		name := stringValue(payload["name"])
		return []AstralEvent{nativeEvent(session, "tool.started", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     name,
			"category": codexNativeToolCategory(name),
			"input":    payload["input"],
			"status":   firstString(payload["status"], "running"),
		})}
	case "custom_tool_call_output":
		if end := toolEndByCallID[stringValue(payload["call_id"])]; len(end) > 0 {
			return normalizeCodexNativeToolEnd(session, end)
		}
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source": "codex",
			"id":     stringValue(payload["call_id"]),
			"status": firstString(payload["status"], "completed"),
			"result": codexFunctionCallInput(payload["output"]),
			"text":   stringValue(payload["output"]),
		})}
	case "web_search_call":
		action := mapValue(payload["action"])
		query := firstString(action["query"], payload["query"])
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       firstString(payload["call_id"], payload["id"], query),
			"name":     "web_search",
			"category": "search",
			"status":   firstString(payload["status"], "completed"),
			"result":   action,
			"text":     query,
		})}
	case "tool_search_call":
		args := mapValue(payload["arguments"])
		return []AstralEvent{nativeEvent(session, "tool.started", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     "tool_search",
			"category": "search",
			"input":    args,
			"status":   firstString(payload["status"], "running"),
			"text":     stringValue(args["query"]),
		})}
	case "tool_search_output":
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     "tool_search",
			"category": "search",
			"status":   firstString(payload["status"], "completed"),
			"result":   payload["tools"],
		})}
	case "reasoning":
		if text := codexReasoningText(payload); text != "" {
			return []AstralEvent{nativeEvent(session, "reasoning.completed", map[string]any{
				"source": "codex",
				"text":   text,
				"status": "completed",
			})}
		}
	}
	return nil
}

func normalizeCodexNativeViewImage(session Session, payload map[string]any) []AstralEvent {
	path := stringValue(payload["path"])
	if path == "" {
		return nil
	}
	callID := firstString(payload["call_id"], path)
	media := codexNativeMessageMediaPayload(payload, path, "completed")
	return []AstralEvent{
		nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       callID,
			"name":     "view_image",
			"category": "read",
			"status":   "completed",
			"result": map[string]any{
				"path": path,
			},
			"text": path,
		}),
		nativeEvent(session, "message.media", media),
	}
}

func normalizeCodexNativeImageGenerationEnd(session Session, payload map[string]any) []AstralEvent {
	path := firstString(payload["saved_path"], payload["savedPath"])
	status := firstString(payload["status"], "completed")
	if path != "" && status == "generating" {
		status = "completed"
	}
	media := codexNativeMessageMediaPayload(payload, path, status)
	if stringValue(media["media_id"]) == "" {
		return nil
	}
	return []AstralEvent{nativeEvent(session, "message.media", media)}
}

func codexNativeMessageMediaPayload(payload map[string]any, path string, status string) map[string]any {
	mediaID := firstString(payload["call_id"], payload["id"], path)
	name := firstString(payload["name"], filepath.Base(path))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = firstString(mediaID, "image") + ".png"
	}
	mimeType := firstString(payload["mime_type"], payload["mimeType"], "image/png")
	return map[string]any{
		"source":         "codex",
		"id":             mediaID,
		"media_id":       mediaID,
		"item_id":        mediaID,
		"kind":           "image",
		"name":           name,
		"path":           path,
		"saved_path":     path,
		"mime_type":      mimeType,
		"status":         firstString(status, "completed"),
		"revised_prompt": payload["revised_prompt"],
	}
}

func normalizeCodexNativeToolEnd(session Session, payload map[string]any) []AstralEvent {
	switch stringValue(payload["type"]) {
	case "exec_command_end":
		text := firstString(payload["aggregated_output"], payload["formatted_output"], payload["stdout"], payload["stderr"])
		status := firstString(payload["status"], "completed")
		if exitCode := numberValue(payload["exit_code"]); exitCode != 0 {
			status = "failed"
		}
		result := map[string]any{
			"stdout":            payload["stdout"],
			"stderr":            payload["stderr"],
			"aggregated_output": payload["aggregated_output"],
			"exit_code":         payload["exit_code"],
			"duration":          payload["duration"],
		}
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     "exec_command",
			"category": "command",
			"status":   status,
			"command":  codexNativeCommandText(payload),
			"cwd":      payload["cwd"],
			"result":   result,
			"text":     text,
		})}
	case "patch_apply_end":
		text := firstString(payload["stdout"], payload["stderr"])
		status := "completed"
		if !boolValue(payload["success"]) {
			status = "failed"
		}
		events := []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     "apply_patch",
			"category": "file",
			"status":   status,
			"result":   payload,
			"text":     text,
		})}
		if diff := codexNativePatchDiff(payload); diff != "" {
			events = append(events, nativeEvent(session, "tool.diff", map[string]any{
				"source": "codex",
				"diff":   diff,
			}))
		}
		return events
	case "mcp_tool_call_end":
		invocation := mapValue(payload["invocation"])
		name := strings.Trim(strings.Join([]string{stringValue(invocation["server"]), stringValue(invocation["tool"])}, "."), ".")
		result := mapValue(payload["result"])
		status := "completed"
		if codexNativeMCPResultIsError(result) {
			status = "failed"
		}
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       stringValue(payload["call_id"]),
			"name":     name,
			"category": "tool",
			"status":   status,
			"result":   payload["result"],
			"text":     codexNativeMCPResultText(result),
		})}
	case "web_search_end":
		action := mapValue(payload["action"])
		query := firstString(payload["query"], action["query"])
		return []AstralEvent{nativeEvent(session, "tool.completed", map[string]any{
			"source":   "codex",
			"id":       firstString(payload["call_id"], query),
			"name":     "web_search",
			"category": "search",
			"status":   "completed",
			"result":   action,
			"text":     query,
		})}
	default:
		return nil
	}
}

func normalizeCodexNativeItemCompleted(session Session, payload map[string]any) []AstralEvent {
	item := mapValue(payload["item"])
	switch stringValue(item["type"]) {
	case "Plan":
		text := stringValue(item["text"])
		if text == "" {
			return nil
		}
		return []AstralEvent{nativeEvent(session, "plan.updated", map[string]any{
			"source":  "codex",
			"turn_id": stringValue(payload["turn_id"]),
			"item_id": stringValue(item["id"]),
			"text":    text,
		})}
	default:
		return nil
	}
}

func codexNativeToolCategory(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "apply_patch") || strings.Contains(lower, "edit") || strings.Contains(lower, "write"):
		return "file"
	case strings.Contains(lower, "search") || strings.Contains(lower, "grep") || strings.Contains(lower, "glob"):
		return "search"
	case strings.Contains(lower, "exec") || strings.Contains(lower, "command") || strings.Contains(lower, "bash"):
		return "command"
	default:
		return "tool"
	}
}

func codexNativeCommandText(payload map[string]any) string {
	if parsed := arrayValue(payload["parsed_cmd"]); len(parsed) > 0 {
		if cmd := stringValue(mapValue(parsed[0])["cmd"]); cmd != "" {
			return cmd
		}
	}
	if command := strings.Join(stringSlice(payload["command"]), " "); command != "" {
		return command
	}
	return ""
}

func codexNativePatchDiff(payload map[string]any) string {
	changes := mapValue(payload["changes"])
	if len(changes) == 0 {
		return ""
	}
	parts := []string{}
	for path, value := range changes {
		change := mapValue(value)
		diff := firstString(change["unified_diff"], change["diff"], change["patch"])
		if diff == "" {
			continue
		}
		if strings.HasPrefix(diff, "diff --git ") {
			parts = append(parts, diff)
			continue
		}
		parts = append(parts, "diff --git a/"+path+" b/"+path+"\n--- a/"+path+"\n+++ b/"+path+"\n"+diff)
	}
	return strings.Join(parts, "\n")
}

func codexNativeMCPResultIsError(result map[string]any) bool {
	ok := mapValue(result["Ok"])
	return boolValue(ok["isError"])
}

func codexNativeMCPResultText(result map[string]any) string {
	ok := mapValue(result["Ok"])
	parts := []string{}
	for _, item := range arrayValue(ok["content"]) {
		if text := stringValue(mapValue(item)["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func nativeEvent(session Session, kind string, normalized map[string]any) AstralEvent {
	return AstralEvent{
		WorkspaceID: session.WorkspaceID,
		SessionID:   session.ID,
		Agent:       session.Agent,
		Kind:        protocol.AstralEventKind(kind),
		Normalized:  eventNormalized(kind, normalized),
	}
}

const nativeAttachmentManifestHeader = "Attached files available to the agent:"

func nativeUserMessageNormalized(source string, text string) map[string]any {
	visible, attachments := splitNativeAttachmentManifest(text)
	if strings.TrimSpace(visible) == "" && len(attachments) == 0 {
		return nil
	}
	normalized := map[string]any{
		"source": source,
		"text":   strings.TrimSpace(visible),
	}
	if len(attachments) > 0 {
		normalized["attachments"] = transcriptAttachmentValues(attachments)
	}
	return normalized
}

func splitNativeAttachmentManifest(text string) (string, []InputAttachment) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}
	index := strings.Index(text, nativeAttachmentManifestHeader)
	if index < 0 {
		return stripNativeImagePlaceholders(text), nil
	}
	visible := stripNativeImagePlaceholders(strings.TrimSpace(text[:index]))
	manifest := text[index+len(nativeAttachmentManifestHeader):]
	return visible, parseNativeAttachmentManifest(manifest)
}

func parseNativeAttachmentManifest(manifest string) []InputAttachment {
	attachments := []InputAttachment{}
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "<image ") || !strings.HasPrefix(line, "- [") {
			continue
		}
		attachment, ok := nativeAttachmentFromManifestLine(line)
		if ok {
			attachments = append(attachments, attachment)
		}
	}
	return attachments
}

func nativeAttachmentFromManifestLine(line string) (InputAttachment, bool) {
	rest, ok := strings.CutPrefix(line, "- [")
	if !ok {
		return InputAttachment{}, false
	}
	kind, rest, ok := strings.Cut(rest, "] ")
	if !ok {
		return InputAttachment{}, false
	}
	separator := strings.LastIndex(rest, ": ")
	if separator < 0 {
		return InputAttachment{}, false
	}
	label := strings.TrimSpace(rest[:separator])
	path := strings.TrimSpace(rest[separator+2:])
	if marker := strings.Index(path, " <image "); marker >= 0 {
		path = strings.TrimSpace(path[:marker])
	}
	if path == "" || strings.HasPrefix(path, "<image ") {
		return InputAttachment{}, false
	}
	name := label
	mimeType := ""
	if strings.HasSuffix(label, ")") {
		if open := strings.LastIndex(label, " ("); open >= 0 {
			mimeType = strings.TrimSpace(strings.TrimSuffix(label[open+2:], ")"))
			name = strings.TrimSpace(label[:open])
		}
	}
	if kind != "image" {
		kind = "file"
	}
	if name == "" {
		name = filepath.Base(path)
	}
	size := int64(0)
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		size = info.Size()
	}
	return InputAttachment{
		ID:       nativeAttachmentID(path),
		Kind:     kind,
		Path:     path,
		Name:     name,
		MIMEType: mimeType,
		Size:     size,
	}, true
}

func nativeAttachmentID(path string) string {
	sum := sha1.Sum([]byte(filepath.Clean(path)))
	return "native_" + hex.EncodeToString(sum[:])[:24]
}

func stripNativeImagePlaceholders(text string) string {
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if isNativeImagePlaceholderLine(trimmed) {
			continue
		}
		line = strings.ReplaceAll(line, "</image>", "")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isNativeImagePlaceholderLine(text string) bool {
	if text == "" {
		return false
	}
	if text == "</image>" {
		return true
	}
	return strings.HasPrefix(text, "<image") && strings.HasSuffix(text, ">")
}

func codexMessageText(payload map[string]any) string {
	parts := []string{}
	for _, item := range arrayValue(payload["content"]) {
		value := mapValue(item)
		switch stringValue(value["type"]) {
		case "input_text", "output_text", "text":
			if text := stringValue(value["text"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func codexFunctionCallInput(value any) any {
	text := stringValue(value)
	if text == "" {
		return value
	}
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return value
	}
	return decoded
}

func codexUserMessageNormalized(payload map[string]any) map[string]any {
	parts := []string{}
	attachments := []InputAttachment{}
	for _, item := range arrayValue(payload["content"]) {
		value := mapValue(item)
		switch stringValue(value["type"]) {
		case "input_text", "text":
			text := stringValue(value["text"])
			if text == "" || isCodexHiddenUserContext(text) {
				continue
			}
			visible, parsed := splitNativeAttachmentManifest(text)
			if visible != "" {
				parts = append(parts, visible)
			}
			attachments = append(attachments, parsed...)
		}
	}
	normalized := nativeUserMessageNormalized("codex", strings.Join(parts, "\n"))
	if len(normalized) == 0 && len(attachments) > 0 {
		normalized = map[string]any{"source": "codex", "text": ""}
	}
	if len(attachments) > 0 {
		normalized["attachments"] = transcriptAttachmentValues(attachments)
	}
	return normalized
}

func codexUserMessageText(payload map[string]any) string {
	parts := []string{}
	for _, item := range arrayValue(payload["content"]) {
		value := mapValue(item)
		switch stringValue(value["type"]) {
		case "input_text", "text":
			text := stringValue(value["text"])
			if text != "" && !isCodexHiddenUserContext(text) {
				visible, _ := splitNativeAttachmentManifest(text)
				if visible != "" {
					parts = append(parts, visible)
				}
			}
		}
	}
	return strings.Join(parts, "\n")
}

func isCodexHiddenUserContext(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "<environment_context>") && strings.Contains(trimmed, "</environment_context>") {
		return true
	}
	if strings.HasPrefix(trimmed, "# AGENTS.md instructions for ") && strings.Contains(trimmed, "<INSTRUCTIONS>") {
		return true
	}
	return false
}

func codexReasoningText(payload map[string]any) string {
	parts := []string{}
	for _, item := range arrayValue(payload["summary"]) {
		value := mapValue(item)
		if text := stringValue(value["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func isClaudeNativeAPIError(raw map[string]any) bool {
	return boolValue(raw["isApiErrorMessage"]) || raw["apiErrorStatus"] != nil
}

func claudeNativeAPIErrorMessage(raw map[string]any) string {
	if text := claudeMessageText(raw); text != "" {
		return text
	}
	if status := stringValue(raw["apiErrorStatus"]); status != "" {
		return "Claude API error: " + status
	}
	return "Claude API error"
}

func claudeMessageText(raw map[string]any) string {
	message := mapValue(raw["message"])
	content := message["content"]
	if text := stringValue(content); text != "" {
		return text
	}
	parts := []string{}
	for _, item := range arrayValue(content) {
		value := mapValue(item)
		if stringValue(value["type"]) == "text" {
			if text := stringValue(value["text"]); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func claudeUserMessageText(raw map[string]any) string {
	if boolValue(raw["isMeta"]) {
		return ""
	}
	message := mapValue(raw["message"])
	content := message["content"]
	if text := stringValue(content); text != "" {
		return claudeVisibleUserText(text)
	}
	parts := []string{}
	for _, item := range arrayValue(content) {
		value := mapValue(item)
		if stringValue(value["type"]) != "text" {
			continue
		}
		if text := claudeVisibleUserText(stringValue(value["text"])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func claudeVisibleUserText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if command, ok := claudeVisibleCommandText(trimmed); ok {
		return command
	}
	if isClaudeHiddenUserContext(trimmed) {
		return ""
	}
	return text
}

func claudeVisibleCommandText(text string) (string, bool) {
	if !strings.Contains(text, "<command-name>") && !strings.Contains(text, "<command-message>") && !strings.Contains(text, "<command-args>") {
		return "", false
	}
	args := xmlTagValue(text, "command-args")
	if args == "" {
		return "", true
	}
	name := xmlTagValue(text, "command-name")
	if name != "" {
		return strings.TrimSpace(name + " " + args), true
	}
	return args, true
}

func isClaudeHiddenUserContext(text string) bool {
	hiddenTags := []string{
		"local-command-caveat",
		"local-command-stdout",
		"local-command-stderr",
		"environment_context",
		"system-reminder",
	}
	for _, tag := range hiddenTags {
		if strings.HasPrefix(text, "<"+tag+">") && strings.Contains(text, "</"+tag+">") {
			return true
		}
	}
	if strings.HasPrefix(text, "# AGENTS.md instructions for ") && strings.Contains(text, "<INSTRUCTIONS>") {
		return true
	}
	return false
}

func xmlTagValue(text string, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(text, open)
	if start < 0 {
		return ""
	}
	rest := text[start+len(open):]
	end := strings.Index(rest, close)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func nativeLineTimestamp(line []byte) string {
	var raw map[string]any
	if json.Unmarshal(line, &raw) != nil {
		return ""
	}
	return nativeTimeString(raw["timestamp"])
}

func nativeTimeString(value any) string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return ""
		}
		if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano)
		}
		return typed
	case float64:
		if typed <= 0 {
			return ""
		}
		return time.Unix(int64(typed), 0).UTC().Format(time.RFC3339Nano)
	case int64:
		if typed <= 0 {
			return ""
		}
		return time.Unix(typed, 0).UTC().Format(time.RFC3339Nano)
	case int:
		if typed <= 0 {
			return ""
		}
		return time.Unix(int64(typed), 0).UTC().Format(time.RFC3339Nano)
	default:
		return ""
	}
}

func fileModTime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339Nano)
}

func nativeTranscriptFingerprint(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return path + ":" + strconv.FormatInt(info.Size(), 10) + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

func cleanLocalPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if expanded, err := filepath.Abs(path); err == nil {
		path = expanded
	}
	if cleaned, err := filepath.EvalSymlinks(path); err == nil {
		path = cleaned
	}
	return filepath.Clean(path)
}

func encodeClaudeProjectPath(path string) string {
	path = filepath.Clean(path)
	return strings.ReplaceAll(path, string(filepath.Separator), "-")
}

func stableNativeSessionID(agent AgentKind, cwd, nativeID string) string {
	sum := sha1.Sum([]byte(string(agent) + "\x00" + cwd + "\x00" + nativeID))
	return "native_" + string(agent) + "_" + hex.EncodeToString(sum[:])[:16]
}
