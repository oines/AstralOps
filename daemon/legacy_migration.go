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
	"time"
)

type nativeHistoryMigrationMarker struct {
	Version    int    `json:"version"`
	MigratedAt string `json:"migrated_at"`
}

func (s *store) migrateLegacyNativeHistory() error {
	s.bumpNextSeqFromState()
	markerPath := filepath.Join(s.dataDir, "migrations", "native-history-v1.json")
	if _, err := os.Stat(markerPath); err == nil {
		return nil
	}
	reader := legacyMigrationReader{dir: filepath.Join(s.dataDir, "events")}
	events, err := reader.Read()
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	migratedSessions := map[string]bool{}
	for _, ev := range events {
		if ev.Seq > s.nextSeq {
			s.nextSeq = ev.Seq
		}
		switch ev.Kind {
		case "session.started", "session.native", "session.deleted":
			s.mu.Lock()
			s.applySessionEventLocked(ev)
			s.mu.Unlock()
			if ev.SessionID != "" {
				migratedSessions[ev.SessionID] = true
			}
		}
		if isOverlayEventKind(string(ev.Kind)) {
			if err := s.persistOverlayEvent(ev); err != nil {
				return err
			}
		}
		if isControlStateEventKind(string(ev.Kind)) {
			if err := s.persistControlStateEvent(ev); err != nil {
				return err
			}
		}
	}
	s.markMissingLegacyNativeHistory(migratedSessions)
	if err := s.persistLoadedSessions(); err != nil {
		return err
	}
	return writeJSONFile(markerPath, nativeHistoryMigrationMarker{Version: 1, MigratedAt: time.Now().UTC().Format(time.RFC3339Nano)}, 0o600)
}

type legacyMigrationReader struct {
	dir string
}

func (s *store) bumpNextSeqFromState() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.overlays {
		for _, ev := range state.Events {
			if ev.Seq > s.nextSeq {
				s.nextSeq = ev.Seq
			}
		}
	}
	for _, ev := range s.controlState.Events {
		if ev.Seq > s.nextSeq {
			s.nextSeq = ev.Seq
		}
	}
}

func (r legacyMigrationReader) Read() ([]AstralEvent, error) {
	files, _ := filepath.Glob(filepath.Join(r.dir, "*.jsonl"))
	out := []AstralEvent{}
	for _, path := range files {
		events, err := readLegacyEventFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq == out[j].Seq {
			return out[i].TS < out[j].TS
		}
		return out[i].Seq < out[j].Seq
	})
	return out, nil
}

func readLegacyEventFile(path string) ([]AstralEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []AstralEvent{}
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var ev AstralEvent
			if decodeErr := json.Unmarshal(line, &ev); decodeErr == nil {
				out = append(out, ev)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *store) markMissingLegacyNativeHistory(sessionIDs map[string]bool) {
	for sessionID := range sessionIDs {
		ss, ok := s.getSession(sessionID)
		if !ok {
			continue
		}
		if ss.NativeRef != nil && ss.NativeRef.LocalPath != "" {
			continue
		}
		if ss.NativeSessionID == "" && ss.NativeThreadID == "" {
			continue
		}
		ss.Source = SessionSourceLegacyUnlinked
		ss.ManagedByAstralOps = false
		s.mu.Lock()
		current, ok := s.sessions[sessionID]
		if ok {
			current.Source = ss.Source
			current.ManagedByAstralOps = ss.ManagedByAstralOps
			s.sessions[sessionID] = current
		}
		s.mu.Unlock()
	}
}
