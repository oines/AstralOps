package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func claudeRemoteNativeDisallowedTools() []string {
	return []string{"Read", "Write", "Edit", "MultiEdit", "Glob", "Grep", "Bash", "NotebookEdit", "LS"}
}

func claudeRemoteMCPAllowedTools() []string {
	return []string{
		"mcp__astralops_remote__read",
		"mcp__astralops_remote__write",
		"mcp__astralops_remote__edit",
		"mcp__astralops_remote__multiedit",
		"mcp__astralops_remote__glob",
		"mcp__astralops_remote__grep",
		"mcp__astralops_remote__bash",
	}
}

func (a *app) writeClaudeRemoteMCPConfig(ws Workspace) (string, error) {
	helper, err := claudeRemoteHelperExecutable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.store.dataDir, "runtime", "claude-remote", ws.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	config := map[string]any{
		"mcpServers": map[string]any{
			"astralops_remote": map[string]any{
				"command": helper,
				"args":    []string{"claude-remote-mcp"},
				"env": map[string]string{
					"ASTRALOPS_DAEMON":       "http://" + a.addr,
					"ASTRALOPS_TOKEN":        a.token,
					"ASTRALOPS_WORKSPACE_ID": ws.ID,
				},
				"timeout": 600000,
			},
		},
	}
	path := filepath.Join(dir, "mcp.json")
	body, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (a *app) handleClaudeRemoteTool(w http.ResponseWriter, r *http.Request, ws Workspace) {
	if ws.Target != "ssh" || ws.SSH == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claude remote tools require ssh workspace"})
		return
	}
	var req struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := decodeJSON(r.Body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	output, err := a.claudeRemoteToolOutput(ctx, ws, req.Tool, req.Arguments)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	isError := strings.TrimSpace(req.Tool) == "bash" && int(numberValue(output["exitCode"])) != 0
	writeJSON(w, http.StatusOK, map[string]any{"output": output, "is_error": isError})
}

func (a *app) claudeRemoteToolOutput(ctx context.Context, ws Workspace, tool string, args map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(tool) {
	case "read":
		return a.claudeRemoteToolRead(ctx, ws, args)
	case "write":
		return a.claudeRemoteToolWrite(ctx, ws, args)
	case "edit":
		return a.claudeRemoteToolEdit(ctx, ws, args)
	case "multiedit":
		return a.claudeRemoteToolMultiEdit(ctx, ws, args)
	case "glob":
		return a.claudeRemoteToolGlob(ctx, ws, args)
	case "grep":
		return a.claudeRemoteToolGrep(ctx, ws, args)
	case "bash":
		return a.claudeRemoteToolBash(ctx, ws, args)
	default:
		return nil, fmt.Errorf("unsupported AstralOps remote tool %q", tool)
	}
}

func (a *app) claudeRemoteToolRead(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	remote := claudeRemoteToolPath(ws, firstString(args["file_path"], args["path"]))
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "read", map[string]any{"path": remote}, &out); err != nil {
		return nil, err
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return nil, err
	}
	content := string(body)
	lines := splitLinesKeepEnd(content)
	totalLines := len(lines)
	startLine := int(numberValue(firstNonNil(args["offset"], args["start_line"])))
	if startLine <= 0 {
		startLine = 1
	}
	startIndex := startLine - 1
	if startIndex > len(lines) {
		startIndex = len(lines)
	}
	endIndex := len(lines)
	if limit := int(numberValue(args["limit"])); limit > 0 && startIndex+limit < endIndex {
		endIndex = startIndex + limit
	}
	selected := strings.Join(lines[startIndex:endIndex], "")
	output := map[string]any{
		"type": "text",
		"file": map[string]any{
			"filePath":   remote,
			"content":    selected,
			"numLines":   len(lines[startIndex:endIndex]),
			"startLine":  startLine,
			"totalLines": totalLines,
		},
	}
	return output, validateClaudeNativeToolOutput("Read", output)
}

func (a *app) claudeRemoteToolWrite(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	remote := claudeRemoteToolPath(ws, firstString(args["file_path"], args["path"]))
	content := firstString(args["content"], args["text"])
	original, existed, err := a.remoteReadStringIfExists(ctx, ws, remote)
	if err != nil {
		return nil, err
	}
	if err := a.ssh.call(ctx, ws, "write", remoteWriteParams(remote, []byte(content)), nil); err != nil {
		return nil, err
	}
	if !existed {
		return map[string]any{
			"type":            "create",
			"filePath":        remote,
			"content":         content,
			"structuredPatch": []any{},
			"originalFile":    nil,
			"userModified":    false,
		}, nil
	}
	return map[string]any{
		"type":            "update",
		"filePath":        remote,
		"content":         content,
		"structuredPatch": simpleStructuredPatch(original, content),
		"originalFile":    original,
		"userModified":    false,
	}, nil
}

func (a *app) claudeRemoteToolEdit(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	remote := claudeRemoteToolPath(ws, firstString(args["file_path"], args["path"]))
	oldString := stringValue(args["old_string"])
	newString := stringValue(args["new_string"])
	replaceAll := boolValue(firstNonNil(args["replace_all"], args["replaceAll"]))
	if oldString == "" {
		return nil, errors.New("old_string is required")
	}
	original, _, err := a.remoteReadStringIfExists(ctx, ws, remote)
	if err != nil {
		return nil, err
	}
	next := ""
	if replaceAll {
		if !strings.Contains(original, oldString) {
			return nil, fmt.Errorf("old_string not found in %s", remote)
		}
		next = strings.ReplaceAll(original, oldString, newString)
	} else {
		index := strings.Index(original, oldString)
		if index < 0 {
			return nil, fmt.Errorf("old_string not found in %s", remote)
		}
		next = original[:index] + newString + original[index+len(oldString):]
	}
	if err := a.ssh.call(ctx, ws, "write", remoteWriteParams(remote, []byte(next)), nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"filePath":        remote,
		"oldString":       oldString,
		"newString":       newString,
		"originalFile":    original,
		"structuredPatch": simpleStructuredPatch(original, next),
		"userModified":    false,
		"replaceAll":      replaceAll,
	}, nil
}

func (a *app) claudeRemoteToolMultiEdit(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	remote := claudeRemoteToolPath(ws, firstString(args["file_path"], args["path"]))
	original, _, err := a.remoteReadStringIfExists(ctx, ws, remote)
	if err != nil {
		return nil, err
	}
	next := original
	applied := []map[string]any{}
	for _, item := range arrayValue(args["edits"]) {
		edit := mapValue(item)
		oldString := stringValue(edit["old_string"])
		newString := stringValue(edit["new_string"])
		replaceAll := boolValue(firstNonNil(edit["replace_all"], edit["replaceAll"]))
		if oldString == "" {
			return nil, errors.New("edits[].old_string is required")
		}
		if replaceAll {
			if !strings.Contains(next, oldString) {
				return nil, fmt.Errorf("old_string not found in %s", remote)
			}
			next = strings.ReplaceAll(next, oldString, newString)
		} else {
			index := strings.Index(next, oldString)
			if index < 0 {
				return nil, fmt.Errorf("old_string not found in %s", remote)
			}
			next = next[:index] + newString + next[index+len(oldString):]
		}
		applied = append(applied, map[string]any{"oldString": oldString, "newString": newString, "replaceAll": replaceAll})
	}
	if len(applied) == 0 {
		return nil, errors.New("edits must contain at least one edit")
	}
	if err := a.ssh.call(ctx, ws, "write", remoteWriteParams(remote, []byte(next)), nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"filePath":        remote,
		"edits":           applied,
		"originalFile":    original,
		"structuredPatch": simpleStructuredPatch(original, next),
		"userModified":    false,
	}, nil
}

func (a *app) claudeRemoteToolGlob(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	cwd := claudeRemoteToolPath(ws, firstString(args["path"], args["cwd"]))
	start := time.Now()
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "glob", map[string]any{"cwd": cwd, "pattern": firstString(args["pattern"], "*")}, &out); err != nil {
		return nil, err
	}
	matches := []string{}
	for _, value := range arrayValue(out["matches"]) {
		if path := stringValue(value); path != "" {
			matches = append(matches, remotePathClean(path))
		}
	}
	sort.Strings(matches)
	output := map[string]any{
		"filenames":  matches,
		"durationMs": int(time.Since(start).Milliseconds()),
		"numFiles":   len(matches),
		"truncated":  false,
	}
	return output, validateClaudeNativeToolOutput("Glob", output)
}

func (a *app) claudeRemoteToolGrep(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	cwd := claudeRemoteToolPath(ws, firstString(args["path"], args["cwd"]))
	limit := int(numberValue(args["limit"]))
	if limit <= 0 || limit > maxClaudeRemoteGrepMatches {
		limit = maxClaudeRemoteGrepMatches
	}
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "grep", map[string]any{
		"cwd":     cwd,
		"pattern": stringValue(args["pattern"]),
		"glob":    stringValue(args["glob"]),
		"limit":   limit,
	}, &out); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	filenames := []string{}
	contentLines := []string{}
	includeLine := boolValue(args["-n"]) || boolValue(args["line_numbers"])
	for _, value := range arrayValue(out["matches"]) {
		match := mapValue(value)
		path := remotePathClean(stringValue(match["path"]))
		if path == "" {
			continue
		}
		if !seen[path] {
			seen[path] = true
			filenames = append(filenames, path)
		}
		if includeLine {
			contentLines = append(contentLines, fmt.Sprintf("%s:%d:%s", path, int(numberValue(match["line"])), stringValue(match["text"])))
		} else {
			contentLines = append(contentLines, fmt.Sprintf("%s:%s", path, stringValue(match["text"])))
		}
	}
	sort.Strings(filenames)
	if stringValue(args["output_mode"]) == "content" {
		output := map[string]any{
			"mode":      "content",
			"numFiles":  0,
			"filenames": []string{},
			"content":   strings.Join(contentLines, "\n"),
			"numLines":  len(contentLines),
		}
		return output, validateClaudeNativeToolOutput("Grep", output)
	}
	output := map[string]any{
		"mode":      "files_with_matches",
		"filenames": filenames,
		"numFiles":  len(filenames),
	}
	return output, validateClaudeNativeToolOutput("Grep", output)
}

func (a *app) claudeRemoteToolBash(ctx context.Context, ws Workspace, args map[string]any) (map[string]any, error) {
	command := strings.TrimSpace(stringValue(args["command"]))
	if command == "" {
		return nil, errors.New("command is required")
	}
	cwd := claudeRemoteToolPath(ws, firstString(args["cwd"], args["path"]))
	timeoutMs := int(numberValue(args["timeout_ms"]))
	if timeoutMs <= 0 {
		timeoutMs = 60000
	}
	out, err := a.runRemoteWorkspaceExecAt(ctx, ws, command, cwd, timeoutMs)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"stdout":           firstString(out["stdout"], out["output"]),
		"stderr":           stringValue(out["stderr"]),
		"interrupted":      false,
		"isImage":          false,
		"noOutputExpected": false,
		"exitCode":         int(numberValue(out["exit_code"])),
	}, nil
}

func (a *app) remoteReadStringIfExists(ctx context.Context, ws Workspace, remote string) (string, bool, error) {
	var out map[string]any
	err := a.ssh.call(ctx, ws, "read", map[string]any{"path": remote}, &out)
	if err != nil {
		return "", false, nil
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return "", false, err
	}
	return string(body), true, nil
}

func claudeRemoteToolPath(ws Workspace, requested string) string {
	root := remotePathClean(ws.SSH.RemoteCWD)
	path := strings.TrimSpace(requested)
	if path == "" {
		return root
	}
	if !remotePathIsAbs(path) {
		path = remotePathJoin(root, path)
	}
	return remotePathClean(path)
}

func splitLinesKeepEnd(content string) []string {
	if content == "" {
		return []string{}
	}
	raw := strings.SplitAfter(content, "\n")
	if raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	return raw
}

func simpleStructuredPatch(oldContent, newContent string) []map[string]any {
	oldLines := strings.Split(strings.TrimRight(oldContent, "\n"), "\n")
	newLines := strings.Split(strings.TrimRight(newContent, "\n"), "\n")
	if oldContent == "" {
		oldLines = []string{}
	}
	if newContent == "" {
		newLines = []string{}
	}
	lines := make([]string, 0, len(oldLines)+len(newLines))
	for _, line := range oldLines {
		lines = append(lines, "-"+line)
	}
	for _, line := range newLines {
		lines = append(lines, "+"+line)
	}
	return []map[string]any{{
		"oldStart": 1,
		"oldLines": len(oldLines),
		"newStart": 1,
		"newLines": len(newLines),
		"lines":    lines,
	}}
}
