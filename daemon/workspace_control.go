package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
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
	workspaceFileDefaultMaxBytes     = 10 * 1024 * 1024
	workspaceFileHardMaxBytes        = 25 * 1024 * 1024
	workspaceExecDefaultTimeout      = 60 * time.Second
	workspaceExecMaxTimeout          = 120 * time.Second
	workspaceFileStreamFrameChunk    = "workspace_file.chunk"
	workspaceFileStreamFrameComplete = "workspace_file.completed"
	workspaceFileStreamFrameError    = "workspace_file.error"
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

type workspaceFilesDeleteParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Recursive   bool   `json:"recursive,omitempty"`
	Force       bool   `json:"force,omitempty"`
}

type workspaceFilesMoveParams struct {
	WorkspaceID     string `json:"workspace_id"`
	Path            string `json:"path"`
	DestinationPath string `json:"destination_path"`
	Overwrite       bool   `json:"overwrite,omitempty"`
	CreateParents   *bool  `json:"create_parents,omitempty"`
}

type workspaceFilesStreamParams struct {
	WorkspaceID string `json:"workspace_id"`
	Path        string `json:"path"`
	Offset      int64  `json:"offset,omitempty"`
	ChunkSize   int    `json:"chunk_size,omitempty"`
}

type workspaceFileStreamCancelParams struct {
	StreamID string `json:"stream_id"`
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

type workspaceFilesDeleteResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Removed     bool   `json:"removed"`
}

type workspaceFilesMoveResult struct {
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	FromPath    string `json:"from_path"`
	ToPath      string `json:"to_path"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size,omitempty"`
}

type workspaceFileStreamResult struct {
	StreamID    string `json:"stream_id"`
	WorkspaceID string `json:"workspace_id"`
	Target      string `json:"target"`
	Path        string `json:"path"`
	Kind        string `json:"kind"`
	Name        string `json:"name,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Offset      int64  `json:"offset"`
	ChunkSize   int    `json:"chunk_size"`
}

type workspaceFileStreamCancelResult struct {
	StreamID  string `json:"stream_id"`
	Cancelled bool   `json:"cancelled"`
}

type workspaceFileStreamFrame struct {
	StreamID     string `json:"stream_id"`
	RequestID    string `json:"request_id,omitempty"`
	WorkspaceID  string `json:"workspace_id"`
	Target       string `json:"target"`
	Path         string `json:"path"`
	Kind         string `json:"kind,omitempty"`
	Name         string `json:"name,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Size         int64  `json:"size,omitempty"`
	Seq          int64  `json:"seq"`
	Offset       int64  `json:"offset"`
	DataBase64   string `json:"data_base64,omitempty"`
	Final        bool   `json:"final,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type workspaceExecResult struct {
	WorkspaceID    string `json:"workspace_id"`
	Target         string `json:"target"`
	Command        string `json:"command"`
	CWD            string `json:"cwd"`
	ApprovalPolicy string `json:"approval_policy"`
	ExitCode       int    `json:"exit_code"`
	Stdout         string `json:"stdout"`
	Stderr         string `json:"stderr"`
	Output         string `json:"output,omitempty"`
	DurationMS     int64  `json:"duration_ms"`
	Failure        string `json:"failure,omitempty"`
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

func (a *app) deleteControlWorkspacePath(ctx context.Context, params workspaceFilesDeleteParams) (workspaceFilesDeleteResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFilesDeleteResult{}, err
	}
	if strings.TrimSpace(params.Path) == "" {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_file_path_required", "path required")
	}
	if ws.Target == "ssh" {
		return a.deleteRemoteControlWorkspacePath(ctx, ws, params)
	}
	return a.deleteLocalControlWorkspacePath(ws, params)
}

func (a *app) moveControlWorkspacePath(ctx context.Context, params workspaceFilesMoveParams) (workspaceFilesMoveResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFilesMoveResult{}, err
	}
	if strings.TrimSpace(params.Path) == "" {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_file_path_required", "path required")
	}
	if strings.TrimSpace(params.DestinationPath) == "" {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_destination_path_required", "destination_path required")
	}
	if ws.Target == "ssh" {
		return a.moveRemoteControlWorkspacePath(ctx, ws, params)
	}
	return a.moveLocalControlWorkspacePath(ws, params)
}

func (a *app) prepareControlWorkspaceFileStream(ctx context.Context, params workspaceFilesStreamParams) (workspaceFileStreamResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceFileStreamResult{}, err
	}
	if strings.TrimSpace(params.Path) == "" {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_file_path_required", "path required")
	}
	if params.Offset < 0 {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_file_stream_offset_invalid", "workspace file stream offset is invalid")
	}
	if ws.Target == "ssh" {
		return a.prepareRemoteControlWorkspaceFileStream(ctx, ws, params)
	}
	return a.prepareLocalControlWorkspaceFileStream(ws, params)
}

func (a *app) executeControlWorkspaceCommand(ctx context.Context, params workspaceExecParams, grant TrustGrant) (workspaceExecResult, error) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		return workspaceExecResult{}, err
	}
	command := strings.TrimSpace(params.Command)
	if command == "" {
		return workspaceExecResult{}, newActionError(http.StatusBadRequest, "command_required", "command required")
	}
	policy := normalizeWorkspaceExecPolicy(grant.WorkspaceExecPolicy)
	if policy == "" {
		policy = WorkspaceExecPolicyTrusted
	}
	if err := enforceWorkspaceExecPolicy(policy, command, params.CWD); err != nil {
		return workspaceExecResult{}, err
	}
	timeout := workspaceExecTimeout(params.TimeoutMS)
	if ws.Target == "ssh" {
		return a.executeRemoteControlWorkspaceCommand(ctx, ws, command, params.CWD, timeout, policy)
	}
	return executeLocalControlWorkspaceCommand(ctx, ws, command, params.CWD, timeout, policy)
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

func (a *app) deleteLocalControlWorkspacePath(ws Workspace, params workspaceFilesDeleteParams) (workspaceFilesDeleteResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if rel == "" {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_root_path_forbidden", "workspace root cannot be deleted")
	}
	info, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) && params.Force {
			return workspaceFilesDeleteResult{WorkspaceID: ws.ID, Target: ws.Target, Path: rel, Kind: "missing", Removed: false}, nil
		}
		return workspaceFilesDeleteResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", "workspace file not found")
	}
	kind := workspacePathKind(info)
	if info.IsDir() && !params.Recursive {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_delete_recursive_required", "recursive=true required to delete a directory")
	}
	if err := removeWorkspacePath(target, info); err != nil {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_file_delete_failed", err.Error())
	}
	return workspaceFilesDeleteResult{WorkspaceID: ws.ID, Target: ws.Target, Path: rel, Kind: kind, Removed: true}, nil
}

func (a *app) deleteRemoteControlWorkspacePath(ctx context.Context, ws Workspace, params workspaceFilesDeleteParams) (workspaceFilesDeleteResult, error) {
	if a.ssh == nil {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if rel == "" {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_root_path_forbidden", "workspace root cannot be deleted")
	}
	var stat map[string]any
	if err := a.ssh.call(ctx, ws, "stat", map[string]any{"path": target}, &stat); err != nil {
		if params.Force {
			if removeErr := a.ssh.call(ctx, ws, "remove", map[string]any{"path": target, "recursive": params.Recursive, "force": true}, nil); removeErr != nil {
				return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_file_delete_failed", removeErr.Error())
			}
			return workspaceFilesDeleteResult{WorkspaceID: ws.ID, Target: ws.Target, Path: rel, Kind: "missing", Removed: false}, nil
		}
		return workspaceFilesDeleteResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", err.Error())
	}
	kind := remoteWorkspaceKind(stat)
	if kind == "dir" && !params.Recursive {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_delete_recursive_required", "recursive=true required to delete a directory")
	}
	if err := a.ssh.call(ctx, ws, "remove", map[string]any{"path": target, "recursive": params.Recursive, "force": params.Force}, nil); err != nil {
		return workspaceFilesDeleteResult{}, newActionError(http.StatusBadRequest, "workspace_file_delete_failed", err.Error())
	}
	return workspaceFilesDeleteResult{WorkspaceID: ws.ID, Target: ws.Target, Path: rel, Kind: kind, Removed: true}, nil
}

func (a *app) moveLocalControlWorkspacePath(ws Workspace, params workspaceFilesMoveParams) (workspaceFilesMoveResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	source, fromRel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	destination, toRel, err := resolveWorkspacePath(root, params.DestinationPath)
	if err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if fromRel == "" || toRel == "" {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_root_path_forbidden", "workspace root cannot be moved")
	}
	if source == destination {
		return workspaceFilesMoveResult{}, newActionError(http.StatusConflict, "workspace_move_same_path", "source and destination are the same path")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", "workspace file not found")
	}
	if info.IsDir() && localPathIsSameOrDescendant(source, destination) {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_move_descendant_forbidden", "cannot move a directory into itself or a descendant")
	}
	if allowCreateParents(params.CreateParents) {
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_file_move_failed", err.Error())
		}
	}
	if err := prepareWorkspaceMoveDestination(destination, params.Overwrite); err != nil {
		return workspaceFilesMoveResult{}, err
	}
	if err := os.Rename(source, destination); err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_file_move_failed", err.Error())
	}
	return workspaceFilesMoveResult{WorkspaceID: ws.ID, Target: ws.Target, FromPath: fromRel, ToPath: toRel, Kind: workspacePathKind(info), Size: info.Size()}, nil
}

func (a *app) moveRemoteControlWorkspacePath(ctx context.Context, ws Workspace, params workspaceFilesMoveParams) (workspaceFilesMoveResult, error) {
	if a.ssh == nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	source, fromRel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	destination, toRel, err := resolveRemoteWorkspacePath(root, params.DestinationPath)
	if err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	if fromRel == "" || toRel == "" {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_root_path_forbidden", "workspace root cannot be moved")
	}
	if source == destination {
		return workspaceFilesMoveResult{}, newActionError(http.StatusConflict, "workspace_move_same_path", "source and destination are the same path")
	}
	var stat map[string]any
	if err := a.ssh.call(ctx, ws, "stat", map[string]any{"path": source}, &stat); err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", err.Error())
	}
	kind := remoteWorkspaceKind(stat)
	if kind == "dir" && remotePathIsSameOrDescendant(source, destination) {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_move_descendant_forbidden", "cannot move a directory into itself or a descendant")
	}
	if err := a.ssh.call(ctx, ws, "move", map[string]any{"source": source, "destination": destination, "overwrite": params.Overwrite, "create_parents": allowCreateParents(params.CreateParents)}, nil); err != nil {
		return workspaceFilesMoveResult{}, newActionError(http.StatusBadRequest, "workspace_file_move_failed", err.Error())
	}
	return workspaceFilesMoveResult{WorkspaceID: ws.ID, Target: ws.Target, FromPath: fromRel, ToPath: toRel, Kind: kind, Size: int64(numberValue(stat["size"]))}, nil
}

func (a *app) prepareLocalControlWorkspaceFileStream(ws Workspace, params workspaceFilesStreamParams) (workspaceFileStreamResult, error) {
	root := filepath.Clean(ws.LocalCWD)
	target, rel, err := resolveWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	info, err := os.Stat(target)
	if err != nil {
		return workspaceFileStreamResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", "workspace file not found")
	}
	if info.IsDir() {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_directory", "workspace path is a directory")
	}
	if params.Offset > info.Size() {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_file_stream_offset_invalid", "workspace file stream offset is invalid")
	}
	return workspaceFileStreamResult{
		StreamID:    "workspace_file_" + randomID(16),
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Path:        rel,
		Kind:        "file",
		Name:        filepath.Base(target),
		MIMEType:    workspaceFileMIMEType(target, nil),
		Size:        info.Size(),
		Offset:      params.Offset,
		ChunkSize:   workspaceFileStreamChunkSize(params.ChunkSize),
	}, nil
}

func (a *app) prepareRemoteControlWorkspaceFileStream(ctx context.Context, ws Workspace, params workspaceFilesStreamParams) (workspaceFileStreamResult, error) {
	if a.ssh == nil {
		return workspaceFileStreamResult{}, newActionError(http.StatusNotImplemented, "ssh_unavailable", "ssh manager unavailable")
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, rel, err := resolveRemoteWorkspacePath(root, params.Path)
	if err != nil {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_path_invalid", err.Error())
	}
	var stat map[string]any
	if err := a.ssh.call(ctx, ws, "stat", map[string]any{"path": target}, &stat); err != nil {
		return workspaceFileStreamResult{}, newActionError(http.StatusNotFound, "workspace_file_not_found", err.Error())
	}
	if boolValue(stat["is_dir"]) {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_path_is_directory", "workspace path is a directory")
	}
	size := int64(numberValue(stat["size"]))
	if params.Offset > size {
		return workspaceFileStreamResult{}, newActionError(http.StatusBadRequest, "workspace_file_stream_offset_invalid", "workspace file stream offset is invalid")
	}
	return workspaceFileStreamResult{
		StreamID:    "workspace_file_" + randomID(16),
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Path:        rel,
		Kind:        "file",
		Name:        firstString(stat["name"], remotePathBase(target)),
		MIMEType:    workspaceFileMIMEType(target, nil),
		Size:        size,
		Offset:      params.Offset,
		ChunkSize:   workspaceFileStreamChunkSize(params.ChunkSize),
	}, nil
}

func (a *app) streamControlWorkspaceFile(ctx context.Context, params workspaceFilesStreamParams, result workspaceFileStreamResult, conn *controlWSConn, requestID string) {
	ws, err := a.controlWorkspace(params.WorkspaceID)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_not_found", err.Error())})
		return
	}
	if ws.Target == "ssh" {
		a.streamRemoteControlWorkspaceFile(ctx, ws, result, conn, requestID)
		return
	}
	a.streamLocalControlWorkspaceFile(ctx, ws, result, conn, requestID)
}

func (a *app) streamLocalControlWorkspaceFile(ctx context.Context, ws Workspace, result workspaceFileStreamResult, conn *controlWSConn, requestID string) {
	root := filepath.Clean(ws.LocalCWD)
	target, _, err := resolveWorkspacePath(root, result.Path)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_path_invalid", err.Error())})
		return
	}
	file, err := os.Open(target)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_file_not_found", "workspace file not found")})
		return
	}
	defer file.Close()
	offset := result.Offset
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_file_stream_seek_failed", err.Error())})
			return
		}
	}
	buffer := make([]byte, result.ChunkSize)
	seq := int64(0)
	for {
		if controlStreamCancelled(ctx) {
			return
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			if controlStreamCancelled(ctx) {
				return
			}
			seq++
			conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameChunk, WorkspaceFile: workspaceFileChunkFrame(result, requestID, seq, offset, buffer[:n])})
			offset += int64(n)
		}
		if controlStreamCancelled(ctx) {
			return
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameComplete, WorkspaceFile: workspaceFileCompleteFrame(result, requestID, seq+1, offset)})
			return
		}
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_file_stream_read_failed", readErr.Error())})
		return
	}
}

func (a *app) streamRemoteControlWorkspaceFile(ctx context.Context, ws Workspace, result workspaceFileStreamResult, conn *controlWSConn, requestID string) {
	if a.ssh == nil {
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "ssh_unavailable", "ssh manager unavailable")})
		return
	}
	root := remotePathClean(ws.SSH.RemoteCWD)
	target, _, err := resolveRemoteWorkspacePath(root, result.Path)
	if err != nil {
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_path_invalid", err.Error())})
		return
	}
	offset := result.Offset
	seq := int64(0)
	for offset < result.Size {
		if controlStreamCancelled(ctx) {
			return
		}
		var out map[string]any
		if err := a.ssh.call(ctx, ws, "read_range", map[string]any{"path": target, "offset": offset, "length": result.ChunkSize}, &out); err != nil {
			if controlStreamCancelled(ctx) {
				return
			}
			conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_file_stream_read_failed", err.Error())})
			return
		}
		body, err := remoteReadBytes(out)
		if err != nil {
			conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameError, WorkspaceFile: workspaceFileStreamErrorFrame(result, requestID, "workspace_file_stream_read_failed", err.Error())})
			return
		}
		if len(body) == 0 {
			break
		}
		if controlStreamCancelled(ctx) {
			return
		}
		seq++
		conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameChunk, WorkspaceFile: workspaceFileChunkFrame(result, requestID, seq, offset, body)})
		offset += int64(len(body))
	}
	if controlStreamCancelled(ctx) {
		return
	}
	conn.writePlain(controlPlainFrame{Type: workspaceFileStreamFrameComplete, WorkspaceFile: workspaceFileCompleteFrame(result, requestID, seq+1, offset)})
}

func executeLocalControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration, approvalPolicy string) (workspaceExecResult, error) {
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
		WorkspaceID:    ws.ID,
		Target:         ws.Target,
		Command:        command,
		CWD:            rel,
		ApprovalPolicy: approvalPolicy,
		ExitCode:       exitCode,
		Stdout:         stdout.String(),
		Stderr:         stderr.String(),
		DurationMS:     time.Since(start).Milliseconds(),
	}, nil
}

func (a *app) executeRemoteControlWorkspaceCommand(parent context.Context, ws Workspace, command, requestedCWD string, timeout time.Duration, approvalPolicy string) (workspaceExecResult, error) {
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
		WorkspaceID:    ws.ID,
		Target:         ws.Target,
		Command:        firstString(out["command"], command),
		CWD:            rel,
		ApprovalPolicy: approvalPolicy,
		ExitCode:       int(numberValue(out["exit_code"])),
		Stdout:         stringValue(out["stdout"]),
		Stderr:         stringValue(out["stderr"]),
		Output:         stringValue(out["output"]),
		DurationMS:     int64(numberValue(out["duration_ms"])),
		Failure:        stringValue(out["failure"]),
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

func workspacePathKind(info os.FileInfo) string {
	if info.IsDir() {
		return "dir"
	}
	return "file"
}

func remoteWorkspaceKind(stat map[string]any) string {
	if boolValue(stat["is_dir"]) {
		return "dir"
	}
	return "file"
}

func removeWorkspacePath(target string, info os.FileInfo) error {
	if info.IsDir() {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

func prepareWorkspaceMoveDestination(destination string, overwrite bool) error {
	info, err := os.Lstat(destination)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return newActionError(http.StatusBadRequest, "workspace_file_move_failed", err.Error())
	}
	if !overwrite {
		return newActionError(http.StatusConflict, "workspace_destination_exists", "destination already exists")
	}
	if info.IsDir() {
		return newActionError(http.StatusConflict, "workspace_destination_exists", "destination already exists and is a directory")
	}
	if err := os.Remove(destination); err != nil {
		return newActionError(http.StatusBadRequest, "workspace_file_move_failed", err.Error())
	}
	return nil
}

func localPathIsSameOrDescendant(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func remotePathIsSameOrDescendant(root, target string) bool {
	rel, err := remotePathRel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, "../"))
}

func workspaceFileStreamChunkSize(requested int) int {
	return mediaStreamChunkSize(requested)
}

func workspaceFileChunkFrame(result workspaceFileStreamResult, requestID string, seq, offset int64, body []byte) *workspaceFileStreamFrame {
	frame := workspaceFileBaseStreamFrame(result, requestID)
	frame.Seq = seq
	frame.Offset = offset
	frame.DataBase64 = base64.StdEncoding.EncodeToString(body)
	return frame
}

func workspaceFileCompleteFrame(result workspaceFileStreamResult, requestID string, seq, offset int64) *workspaceFileStreamFrame {
	frame := workspaceFileBaseStreamFrame(result, requestID)
	frame.Seq = seq
	frame.Offset = offset
	frame.Final = true
	return frame
}

func workspaceFileStreamErrorFrame(result workspaceFileStreamResult, requestID, code, message string) *workspaceFileStreamFrame {
	frame := workspaceFileBaseStreamFrame(result, requestID)
	frame.Offset = result.Offset
	frame.ErrorCode = code
	frame.ErrorMessage = message
	return frame
}

func workspaceFileBaseStreamFrame(result workspaceFileStreamResult, requestID string) *workspaceFileStreamFrame {
	return &workspaceFileStreamFrame{
		StreamID:    result.StreamID,
		RequestID:   requestID,
		WorkspaceID: result.WorkspaceID,
		Target:      result.Target,
		Path:        result.Path,
		Kind:        result.Kind,
		Name:        result.Name,
		MIMEType:    result.MIMEType,
		Size:        result.Size,
	}
}

func enforceWorkspaceExecPolicy(policy string, command, requestedCWD string) error {
	switch policy {
	case WorkspaceExecPolicyTrusted:
		return nil
	case WorkspaceExecPolicyRequireApproval:
		return newActionError(http.StatusConflict, "workspace_exec_approval_required", workspaceExecPolicyMessage(command, requestedCWD, "workspace.exec requires Host approval before executing"))
	case WorkspaceExecPolicyDisabled:
		return newActionError(http.StatusForbidden, "workspace_exec_disabled", workspaceExecPolicyMessage(command, requestedCWD, "workspace.exec is disabled by Host policy"))
	default:
		return newActionError(http.StatusBadRequest, "workspace_exec_policy_invalid", "invalid workspace_exec_policy")
	}
}

func workspaceExecPolicyMessage(command, requestedCWD, prefix string) string {
	cwd := strings.TrimSpace(requestedCWD)
	if cwd == "" {
		cwd = "."
	}
	if cwd != "" {
		return prefix + ": " + command + " (cwd " + cwd + ")"
	}
	return prefix + ": " + command
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
