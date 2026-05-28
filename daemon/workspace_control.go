package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	workspaceFileDefaultMaxBytes = 10 * 1024 * 1024
	workspaceFileHardMaxBytes    = 25 * 1024 * 1024
	workspaceExecDefaultTimeout  = 60 * time.Second
	workspaceExecMaxTimeout      = 120 * time.Second
)

type workspaceFilesReadParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Mode        string `json:"mode"`
	MaxBytes    int64  `json:"max_bytes"`
}

type workspaceFilesWriteParams struct {
	WorkspaceID   string `json:"workspace_id"`
	Path          string `json:"path"`
	Content       string `json:"content"`
	ContentBase64 string `json:"content_base64"`
	CreateParents *bool  `json:"create_parents,omitempty"`
}

type workspaceFilesApplyPatchParams struct {
	WorkspaceID string                  `json:"workspace_id"`
	Path        string                  `json:"path"`
	Edits       []workspaceFileTextEdit `json:"edits"`
}

type workspaceFileTextEdit struct {
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type workspaceExecParams struct {
	WorkspaceID string `json:"workspace_id"`
	Command     string `json:"command"`
	CWD         string `json:"cwd"`
	TimeoutMS   int    `json:"timeout_ms"`
}

type workspaceFileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type workspaceFilesReadResult struct {
	WorkspaceID   string               `json:"workspace_id"`
	Target        string               `json:"target"`
	Path          string               `json:"path"`
	Kind          string               `json:"kind"`
	Name          string               `json:"name,omitempty"`
	Size          int64                `json:"size,omitempty"`
	ModTime       string               `json:"mod_time,omitempty"`
	MIMEType      string               `json:"mime_type,omitempty"`
	ContentBase64 string               `json:"content_base64,omitempty"`
	Entries       []workspaceFileEntry `json:"entries,omitempty"`
	Truncated     bool                 `json:"truncated,omitempty"`
}

type workspaceFilesWriteResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
}

type workspaceFilesApplyPatchResult struct {
	WorkspaceID     string           `json:"workspace_id"`
	Target          string           `json:"target"`
	Path            string           `json:"path"`
	Kind            string           `json:"kind"`
	Size            int64            `json:"size"`
	AppliedEdits    int              `json:"applied_edits"`
	StructuredPatch []map[string]any `json:"structured_patch,omitempty"`
}

type workspaceExecResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Command     string `json:"command"`
	CWD         string `json:"cwd"`
	ExitCode    int    `json:"exit_code"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Output      string `json:"output,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	Failure     string `json:"failure,omitempty"`
}

func (a *app) controlWorkspace(workspaceID string) (Workspace, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return Workspace{}, newActionError(http.StatusBadRequest, "workspace_id_required", "workspace_id required")
	}
	ws, ok := a.store.getWorkspace(workspaceID)
	if !ok {
		return Workspace{}, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
	}
	return ws, nil
}

func (a *app) readControlWorkspaceFiles(ctx context.Context, params workspaceFilesReadParams) (workspaceFilesReadResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFilesReadResult{}, err
	}
	if ws.Target == "ssh" {
		return a.readRemoteControlWorkspaceFiles(ctx, ws, params)
	}
	return a.readLocalControlWorkspaceFiles(ws, params)
}

func (a *app) writeControlWorkspaceFile(ctx context.Context, params workspaceFilesWriteParams) (workspaceFilesWriteResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFilesWriteResult{}, err
	}
	if strings.TrimSpace(params.Path) == "" {
		return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_file_path_required", "path required")
	}
	body, err := workspaceWriteBody(params)
	if err != nil {
		return workspaceFilesWriteResult{}, err
	}
	if ws.Target == "ssh" {
		return a.writeRemoteControlWorkspaceFile(ctx, ws, params, body)
	}
	return a.writeLocalControlWorkspaceFile(ws, params, body)
}

func (a *app) applyControlWorkspacePatch(ctx context.Context, params workspaceFilesApplyPatchParams) (workspaceFilesApplyPatchResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, err
	}
	if strings.TrimSpace(params.Path) == "" {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_file_path_required", "path required")
	}
	if len(params.Edits) == 0 {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_patch_required", "edits required")
	}
	if ws.Target == "ssh" {
		return a.applyRemoteControlWorkspacePatch(ctx, ws, params)
	}
	return a.applyLocalControlWorkspacePatch(ws, params)
}

func (a *app) executeControlWorkspaceCommand(ctx context.Context, params workspaceExecParams) (workspaceExecResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceExecResult{}, err
	}
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return workspaceExecResult{}, newActionError(http.StatusBadRequest, "command_required", "command required")
	}
	timeout := workspaceExecTimeout(params.TimeoutMS)
	if ws.Target == "ssh" {
		return a.executeRemoteControlWorkspaceCommand(ctx, ws, command, params.CWD, timeout)
	}
	return executeLocalControlWorkspaceCommand(ctx, ws, command, params.CWD, timeout)
}

func (a *app) readLocalControlWorkspaceFiles(ws Workspace, params workspaceFilesReadParams) (workspaceFilesReadResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	info, err := os.Stat(target)
	if err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", "workspace file not found")
	}
	mode := strings.TrimSpace(params.Mode)
	if mode == "" {
		mode = "auto"
	}
	if info.IsDir() {
		if mode == "file" {
			return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_directory", "workspace path is a directory")
		}
		entries, truncated, err := localWorkspaceEntries(target, rel)
		if err != nil {
			return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_list_failed", err.Error())
		}
		return workspaceFilesReadResult{
			WorkspaceID: ws.ID,
			Target:      ws.Target,
			Path:        rel,
			Kind:        "dir",
			Name:        filepath.Base(target),
			ModTime:     info.ModTime().UTC().Format(time.RFC3339),
			Entries:     entries,
			Truncated:   truncated,
		}, nil
	}
	if mode == "list" {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_file", "workspace path is a file")
	}
	body, err := readWorkspaceFileBytes(target, info, params.MaxBytes)
	if err != nil {
		return workspaceFilesReadResult{}, err
	}
	return workspaceFilesReadResult{
		WorkspaceID:   ws.ID,
		Target:        ws.Target,
		Path:          rel,
		Kind:          "file",
		Name:          filepath.Base(target),
		Size:          info.Size(),
		ModTime:       info.ModTime().UTC().Format(time.RFC3339),
		MIMEType:      workspaceFileMIMEType(target, body),
		ContentBase64: base64.StdEncoding.EncodeToString(body),
	}, nil
}

func (a *app) readRemoteControlWorkspaceFiles(ctx context.Context, ws Workspace, params workspaceFilesReadParams) (workspaceFilesReadResult, error) {
	if a.ssh == nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	var stat map[string]any
	if err := a.ssh.call(ctx, ws, "stat", map[string]any{"path": target}, &stat); err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", err.Error())
	}
	mode := strings.TrimSpace(params.Mode)
	if mode == "" {
		mode = "auto"
	}
	if boolValue(stat["is_dir"]) {
		if mode == "file" {
			return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_directory", "workspace path is a directory")
		}
		entries, truncated, err := a.remoteWorkspaceEntries(ctx, ws, root, target)
		if err != nil {
			return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_list_failed", err.Error())
		}
		return workspaceFilesReadResult{
			WorkspaceID: ws.ID,
			Target:      ws.Target,
			Path:        rel,
			Kind:        "dir",
			Name:        firstString(stat["name"], remotePathBase(target)),
			Size:        int64(numberValue(stat["size"])),
			ModTime:     stringValue(stat["modified"]),
			Entries:     entries,
			Truncated:   truncated,
		}, nil
	}
	if mode == "list" {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_file", "workspace path is a file")
	}
	maxBytes := workspaceReadMaxBytes(params.MaxBytes)
	size := int64(numberValue(stat["size"]))
	if size > maxBytes {
		return workspaceFilesReadResult{}, newActionError(http.StatusRequestEntityTooLarge, "workspace_file_too_large", "workspace file is too large for workspace.files.read")
	}
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "read", map[string]any{"path": target}, &out); err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_file_read_failed", err.Error())
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return workspaceFilesReadResult{}, newActionError(http.StatusBadRequest, "workspace_file_read_failed", err.Error())
	}
	if int64(len(body)) > maxBytes {
		return workspaceFilesReadResult{}, newActionError(http.StatusRequestEntityTooLarge, "workspace_file_too_large", "workspace file is too large for workspace.files.read")
	}
	return workspaceFilesReadResult{
		WorkspaceID:   ws.ID,
		Target:        ws.Target,
		Path:          rel,
		Kind:          "file",
		Name:          firstString(stat["name"], remotePathBase(target)),
		Size:          size,
		ModTime:       stringValue(stat["modified"]),
		MIMEType:      workspaceFileMIMEType(target, body),
		ContentBase64: base64.StdEncoding.EncodeToString(body),
	}, nil
}

func (a *app) writeLocalControlWorkspaceFile(ws Workspace, params workspaceFilesWriteParams, body []byte) (workspaceFilesWriteResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if allowCreateParents(params.CreateParents) {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_file_write_failed", err.Error())
		}
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_file_write_failed", err.Error())
	}
	return workspaceFilesWriteResult{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Path:        rel,
		Kind:        "file",
		Size:        int64(len(body)),
	}, nil
}

func (a *app) writeRemoteControlWorkspaceFile(ctx context.Context, ws Workspace, params workspaceFilesWriteParams, body []byte) (workspaceFilesWriteResult, error) {
	if a.ssh == nil {
		return workspaceFilesWriteResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if err := a.ssh.call(ctx, ws, "write", remoteWriteParams(target, body), nil); err != nil {
		return workspaceFilesWriteResult{}, newActionError(http.StatusBadRequest, "workspace_file_write_failed", err.Error())
	}
	return workspaceFilesWriteResult{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Path:        rel,
		Kind:        "file",
		Size:        int64(len(body)),
	}, nil
}

func (a *app) applyLocalControlWorkspacePatch(ws Workspace, params workspaceFilesApplyPatchParams) (workspaceFilesApplyPatchResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", "workspace file not found")
	}
	body, err := readWorkspaceFileBytes(target, info, 0)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, err
	}
	next, applied, err := applyWorkspaceTextEdits(string(body), params.Edits)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, err
	}
	if err := os.WriteFile(target, []byte(next), 0o600); err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_file_write_failed", err.Error())
	}
	return workspaceFilesApplyPatchResult{
		WorkspaceID:     ws.ID,
		Target:          ws.Target,
		Path:            rel,
		Kind:            "file",
		Size:            int64(len(next)),
		AppliedEdits:    applied,
		StructuredPatch: simpleStructuredPatch(string(body), next),
	}, nil
}

func (a *app) applyRemoteControlWorkspacePatch(ctx context.Context, ws Workspace, params workspaceFilesApplyPatchParams) (workspaceFilesApplyPatchResult, error) {
	if a.ssh == nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "read", map[string]any{"path": target}, &out); err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_file_read_failed", err.Error())
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_file_read_failed", err.Error())
	}
	if int64(len(body)) > workspaceFileHardMaxBytes {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusRequestEntityTooLarge, "workspace_file_too_large", "workspace file is too large for workspace.files.apply_patch")
	}
	next, applied, err := applyWorkspaceTextEdits(string(body), params.Edits)
	if err != nil {
		return workspaceFilesApplyPatchResult{}, err
	}
	if err := a.ssh.call(ctx, ws, "write", remoteWriteParams(target, []byte(next)), nil); err != nil {
		return workspaceFilesApplyPatchResult{}, newActionError(http.StatusBadRequest, "workspace_file_write_failed", err.Error())
	}
	return workspaceFilesApplyPatchResult{
		WorkspaceID:     ws.ID,
		Target:          ws.Target,
		Path:            rel,
		Kind:            "file",
		Size:            int64(len(next)),
		AppliedEdits:    applied,
		StructuredPatch: simpleStructuredPatch(string(body), next),
	}, nil
}

func executeLocalControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration) (workspaceExecResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	cwd, rel, err := resolveWorkspacePath(root, requestedCWD)
	if err != nil {
		return workspaceExecResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := localShellCommand(ctx, command)
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = 124
		}
	}
	return workspaceExecResult{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Command:     command,
		CWD:         rel,
		ExitCode:    exitCode,
		Stdout:      stdout.String(),
		Stderr:      stderr.String(),
		DurationMS:  time.Since(start).Milliseconds(),
	}, nil
}

func (a *app) executeRemoteControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration) (workspaceExecResult, error) {
	if a.ssh == nil {
		return workspaceExecResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	cwd, rel, err := resolveRemoteWorkspacePath(root, requestedCWD)
	if err != nil {
		return workspaceExecResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := a.runRemoteWorkspaceExecAt(ctx, ws, command, cwd, int(timeout.Milliseconds()))
	if err != nil {
		return workspaceExecResult{}, newActionError(http.StatusBadRequest, "workspace_exec_failed", err.Error())
	}
	return workspaceExecResult{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Command:     firstString(out["command"], command),
		CWD:         rel,
		ExitCode:    int(numberValue(out["exit_code"])),
		Stdout:      stringValue(out["stdout"]),
		Stderr:      stringValue(out["stderr"]),
		Output:      stringValue(out["output"]),
		DurationMS:  int64(numberValue(out["duration_ms"])),
		Failure:     stringValue(out["failure"]),
	}, nil
}

func localWorkspaceEntries(target, rel string) ([]workspaceFileEntry, bool, error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, false, err
	}
	out := make([]workspaceFileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "file"
		if entry.IsDir() {
			kind = "dir"
		}
		entryRel := entry.Name()
		if rel != "" {
			entryRel = filepath.ToSlash(filepath.Join(rel, entry.Name()))
		}
		out = append(out, workspaceFileEntry{
			Name:    entry.Name(),
			Path:    entryRel,
			Kind:    kind,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return sortAndLimitWorkspaceEntries(out)
}

func (a *app) remoteWorkspaceEntries(ctx context.Context, ws Workspace, root, target string) ([]workspaceFileEntry, bool, error) {
	var raw []map[string]any
	if err := a.ssh.call(ctx, ws, "list", map[string]any{"path": target}, &raw); err != nil {
		return nil, false, err
	}
	out := make([]workspaceFileEntry, 0, len(raw))
	for _, entry := range raw {
		name := stringValue(entry["name"])
		path := stringValue(entry["path"])
		entryRel, err := remotePathRel(root, path)
		if err != nil || pathEscapesRoot(entryRel) {
			entryRel = remotePathBase(path)
		}
		kind := "file"
		if boolValue(entry["is_dir"]) {
			kind = "dir"
		}
		out = append(out, workspaceFileEntry{
			Name:    name,
			Path:    entryRel,
			Kind:    kind,
			Size:    int64(numberValue(entry["size"])),
			ModTime: stringValue(entry["modified"]),
		})
	}
	return sortAndLimitWorkspaceEntries(out)
}

func sortAndLimitWorkspaceEntries(entries []workspaceFileEntry) ([]workspaceFileEntry, bool, error) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	if len(entries) > 300 {
		return entries[:300], true, nil
	}
	return entries, false, nil
}

func readWorkspaceFileBytes(path string, info os.FileInfo, maxBytes int64) ([]byte, error) {
	limit := workspaceReadMaxBytes(maxBytes)
	if info.Size() > limit {
		return nil, newActionError(http.StatusRequestEntityTooLarge, "workspace_file_too_large", "workspace file is too large for workspace.files.read")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, newActionError(http.StatusBadRequest, "workspace_file_read_failed", err.Error())
	}
	if int64(len(body)) > limit {
		return nil, newActionError(http.StatusRequestEntityTooLarge, "workspace_file_too_large", "workspace file is too large for workspace.files.read")
	}
	return body, nil
}

func workspaceReadMaxBytes(requested int64) int64 {
	if requested <= 0 {
		return workspaceFileDefaultMaxBytes
	}
	if requested > workspaceFileHardMaxBytes {
		return workspaceFileHardMaxBytes
	}
	return requested
}

func workspaceWriteBody(params workspaceFilesWriteParams) ([]byte, error) {
	if params.ContentBase64 != "" {
		body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(params.ContentBase64))
		if err != nil {
			return nil, newActionError(http.StatusBadRequest, "workspace_file_content_invalid", "content_base64 is invalid")
		}
		return body, nil
	}
	return []byte(params.Content), nil
}

func applyWorkspaceTextEdits(content string, edits []workspaceFileTextEdit) (string, int, error) {
	if strings.ContainsRune(content, 0) {
		return "", 0, newActionError(http.StatusBadRequest, "workspace_patch_binary_file", "workspace.files.apply_patch only supports text files")
	}
	next := content
	applied := 0
	for _, edit := range edits {
		oldString := edit.OldString
		if oldString == "" {
			return "", 0, newActionError(http.StatusBadRequest, "workspace_patch_old_string_required", "edits[].old_string required")
		}
		count := strings.Count(next, oldString)
		if count == 0 {
			return "", 0, newActionError(http.StatusConflict, "workspace_patch_old_string_not_found", "old_string not found")
		}
		if !edit.ReplaceAll && count != 1 {
			return "", 0, newActionError(http.StatusConflict, "workspace_patch_old_string_ambiguous", "old_string is not unique")
		}
		if edit.ReplaceAll {
			next = strings.ReplaceAll(next, oldString, edit.NewString)
			applied += count
		} else {
			next = strings.Replace(next, oldString, edit.NewString, 1)
			applied++
		}
		if strings.ContainsRune(next, 0) {
			return "", 0, newActionError(http.StatusBadRequest, "workspace_patch_binary_result", "workspace.files.apply_patch produced binary content")
		}
	}
	return next, applied, nil
}

func allowCreateParents(value *bool) bool {
	return value == nil || *value
}

func workspaceExecTimeout(timeoutMS int) time.Duration {
	if timeoutMS <= 0 {
		return workspaceExecDefaultTimeout
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout > workspaceExecMaxTimeout {
		return workspaceExecMaxTimeout
	}
	return timeout
}

func workspaceFileMIMEType(path string, body []byte) string {
	if byExt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); byExt != "" {
		return byExt
	}
	if len(body) > 0 {
		return http.DetectContentType(body)
	}
	return "application/octet-stream"
}
