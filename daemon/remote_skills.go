package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const remoteSkillsSyncFileLimit = 1000
const remoteSkillsSyncByteLimit = 20 * 1024 * 1024

func (a *app) syncRemoteSkillTree(ctx context.Context, ws Workspace, remoteRel string, localDest string) error {
	if ws.Target != "ssh" || ws.SSH == nil {
		return nil
	}
	remoteRoot := remotePathClean(remotePathJoin(ws.SSH.RemoteCWD, remoteRel))
	if strings.TrimSpace(localDest) == "" {
		return nil
	}
	if err := clearLocalSkillTree(localDest); err != nil {
		return err
	}

	var stat map[string]any
	if err := a.ssh.call(ctx, ws, "stat", map[string]any{"path": remoteRoot}, &stat); err != nil {
		if isRemoteSkillsMissing(err) {
			return nil
		}
		return err
	}
	if !boolValue(stat["is_dir"]) {
		return nil
	}

	var listing map[string]any
	if err := a.ssh.call(ctx, ws, "dirs", map[string]any{"path": remoteRoot, "limit": remoteSkillsSyncFileLimit + 1}, &listing); err != nil {
		if isRemoteSkillsMissing(err) {
			return nil
		}
		return err
	}
	files := remoteSkillsStringSlice(listing["files"])
	if len(files) > remoteSkillsSyncFileLimit || boolValue(listing["truncated"]) {
		return errors.New("remote skills tree is too large")
	}
	totalBytes := 0
	for _, remoteFile := range files {
		remoteFile = remotePathClean(remoteFile)
		rel, err := remotePathRel(remoteRoot, remoteFile)
		if err != nil || pathEscapesRoot(rel) || rel == "." {
			continue
		}
		var out map[string]any
		if err := a.ssh.call(ctx, ws, "read", map[string]any{"path": remoteFile}, &out); err != nil {
			return err
		}
		body, err := remoteReadBytes(out)
		if err != nil {
			return err
		}
		totalBytes += len(body)
		if totalBytes > remoteSkillsSyncByteLimit {
			return errors.New("remote skills tree is too large")
		}
		localPath := filepath.Join(localDest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(localPath, body, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func clearLocalSkillTree(localDest string) error {
	entries, err := os.ReadDir(localDest)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".system" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(localDest, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func isRemoteSkillsMissing(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such file") ||
		strings.Contains(text, "not found") ||
		strings.Contains(text, "does not exist")
}

func remoteSkillsStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}
