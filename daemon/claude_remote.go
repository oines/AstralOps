package main

import (
	"context"
	"encoding/base64"
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

const maxClaudeHydrateBytes int64 = 2 * 1024 * 1024
const maxClaudeRemoteGrepMatches = 200
const claudeRemoteAbsProjectionDir = ".astralops/remote-abs"

var claudeRemoteHookExecutable = os.Executable

type claudeRemoteToolResult struct {
	WorkspaceID string
	Tool        string
	RemoteInput map[string]any
	Output      map[string]any
	CreatedAt   time.Time
}

func (a *app) writeClaudeRemoteSettings(ws Workspace) (string, error) {
	helper, err := claudeRemoteHookExecutable()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.store.dataDir, "runtime", "claude-remote", ws.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	command := claudeRemoteHookCommand(helper, "http://"+a.addr, a.token, ws.ID)
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Read|Glob|Grep|Bash|Write|Edit|MultiEdit",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": command,
					"timeout": 60,
				}},
			}},
			"PostToolUse": []map[string]any{{
				"matcher": "Read|Glob|Grep|Write|Edit|MultiEdit",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": command,
					"timeout": 60,
				}},
			}},
			"PostToolUseFailure": []map[string]any{{
				"matcher": "Write|Edit|MultiEdit",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": command,
					"timeout": 60,
				}},
			}},
		},
	}
	path := filepath.Join(dir, "settings.json")
	body, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (a *app) handleClaudeRemoteHook(w http.ResponseWriter, r *http.Request, ws Workspace) {
	if ws.Target != "ssh" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claude remote hooks require ssh workspace"})
		return
	}
	var payload map[string]any
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := a.processClaudeRemoteHook(r.Context(), ws, payload)
	if err != nil {
		writeJSON(w, http.StatusOK, claudeRemoteHookErrorOutput(payload, err))
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func claudeRemoteHookErrorOutput(payload map[string]any, err error) map[string]any {
	event := firstString(payload["hook_event_name"], payload["hookEventName"], "PreToolUse")
	out := map[string]any{"hookEventName": event}
	message := "Remote operation failed: " + err.Error()
	if event == "PreToolUse" {
		out["permissionDecision"] = "deny"
		out["permissionDecisionReason"] = message
	} else {
		out["decision"] = "block"
		out["reason"] = message
	}
	return map[string]any{"hookSpecificOutput": out}
}

func (a *app) processClaudeRemoteHook(ctx context.Context, ws Workspace, payload map[string]any) (map[string]any, error) {
	event := firstString(payload["hook_event_name"], payload["hookEventName"])
	tool := firstString(payload["tool_name"], payload["toolName"])
	toolUseID := firstString(payload["tool_use_id"], payload["toolUseID"])
	input := mapValue(firstNonNil(payload["tool_input"], payload["toolInput"]))
	if event == "" {
		event = "PreToolUse"
	}
	switch event {
	case "PreToolUse":
		updated, err := a.preClaudeRemoteTool(ctx, ws, tool, toolUseID, input)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"hookEventName": "PreToolUse"}
		if len(updated) > 0 {
			out["updatedInput"] = updated
		}
		if tool == "Bash" && a.shouldAllowClaudeRemoteBash(ws.ID, stringValue(input["command"])) {
			out["permissionDecision"] = "allow"
			out["permissionDecisionReason"] = "read-only or previously approved command"
		}
		return map[string]any{"hookSpecificOutput": out}, nil
	case "PostToolUse":
		updatedOutput, err := a.postClaudeRemoteTool(ctx, ws, tool, toolUseID, input, payload)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"hookEventName": "PostToolUse"}
		if updatedOutput != nil {
			out["updatedToolOutput"] = updatedOutput
		}
		return map[string]any{"hookSpecificOutput": out}, nil
	case "PostToolUseFailure":
		a.deleteClaudeRemoteToolResult(ws.ID, toolUseID)
		if err := a.rollbackClaudeRemoteTool(ctx, ws, tool, input); err != nil {
			return nil, err
		}
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUseFailure"}}, nil
	default:
		return map[string]any{}, nil
	}
}

func (a *app) shouldAllowClaudeRemoteBash(workspaceID, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	return isClaudeRemoteReadOnlyBash(command) || a.consumeClaudeRemoteApprovedCommand(workspaceID, command)
}

func isClaudeRemoteReadOnlyBash(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "\n\r;&|><`$(){}\"'") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	name := filepath.Base(fields[0])
	if name == "." || name == string(os.PathSeparator) || strings.Contains(name, "=") {
		return false
	}
	if !claudeBashArgsLookLiteral(fields[1:]) {
		return false
	}
	switch name {
	case "pwd", "ls", "cat", "head", "tail", "wc", "df", "du", "uname", "whoami", "hostname", "date", "id", "which":
		return true
	case "command":
		return len(fields) >= 3 && fields[1] == "-v"
	default:
		return false
	}
}

func claudeBashArgsLookLiteral(args []string) bool {
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, "\n\r;&|><`$(){}\"'") {
			return false
		}
	}
	return true
}

func (a *app) allowClaudeRemoteApprovedCommand(origin AstralEvent) {
	value := mapValue(origin.Normalized)
	if stringValue(value["kind"]) != "permission" || firstString(value["tool_name"], "Bash") != "Bash" {
		return
	}
	command := strings.TrimSpace(stringValue(value["command"]))
	if command == "" {
		return
	}
	ws, ok := a.store.getWorkspace(origin.WorkspaceID)
	if !ok || ws.Target != "ssh" {
		return
	}
	a.claudeRemoteAllowMu.Lock()
	defer a.claudeRemoteAllowMu.Unlock()
	if a.claudeRemoteAllow == nil {
		a.claudeRemoteAllow = map[string]map[string]bool{}
	}
	if a.claudeRemoteAllow[origin.WorkspaceID] == nil {
		a.claudeRemoteAllow[origin.WorkspaceID] = map[string]bool{}
	}
	a.claudeRemoteAllow[origin.WorkspaceID][command] = true
}

func (a *app) consumeClaudeRemoteApprovedCommand(workspaceID, command string) bool {
	a.claudeRemoteAllowMu.Lock()
	defer a.claudeRemoteAllowMu.Unlock()
	commands := a.claudeRemoteAllow[workspaceID]
	if commands == nil || !commands[command] {
		return false
	}
	delete(commands, command)
	if len(commands) == 0 {
		delete(a.claudeRemoteAllow, workspaceID)
	}
	return true
}

func (a *app) preClaudeRemoteTool(ctx context.Context, ws Workspace, tool, toolUseID string, input map[string]any) (map[string]any, error) {
	updated := copyStringAny(input)
	switch tool {
	case "Bash":
		command := stringValue(input["command"])
		if strings.TrimSpace(command) == "" {
			return nil, nil
		}
		command = remapClaudeRemoteBashCommand(ws, command)
		helper, err := claudeRemoteHookExecutable()
		if err != nil {
			return nil, err
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(command))
		updated["command"] = claudeRemoteHookCommand(helper, "http://"+a.addr, a.token, ws.ID, "exec", encoded)
		return updated, nil
	case "Read":
		local, _, err := a.hydrateClaudePath(ctx, ws, stringValue(input["file_path"]), false)
		if err != nil {
			return nil, err
		}
		updated["file_path"] = claudeLocalToolPath(ws, local)
		return updated, nil
	case "LS":
		local, _, err := a.hydrateClaudePath(ctx, ws, stringValue(input["path"]), true)
		if err != nil {
			return nil, err
		}
		updated["path"] = local
		return updated, nil
	case "Glob":
		output, err := a.remoteClaudeGlobOutput(ctx, ws, input)
		if err != nil {
			return nil, err
		}
		if err := validateClaudeNativeToolOutput("Glob", output); err != nil {
			return nil, err
		}
		a.putClaudeRemoteToolResult(ws.ID, toolUseID, claudeRemoteToolResult{WorkspaceID: ws.ID, Tool: tool, RemoteInput: copyStringAny(input), Output: output, CreatedAt: time.Now()})
		stub, err := a.claudeRemoteSearchStubDir(ws)
		if err != nil {
			return nil, err
		}
		updated["path"] = stub
		return updated, nil
	case "Grep":
		output, err := a.remoteClaudeGrepOutput(ctx, ws, input)
		if err != nil {
			return nil, err
		}
		if err := validateClaudeNativeToolOutput("Grep", output); err != nil {
			return nil, err
		}
		a.putClaudeRemoteToolResult(ws.ID, toolUseID, claudeRemoteToolResult{WorkspaceID: ws.ID, Tool: tool, RemoteInput: copyStringAny(input), Output: output, CreatedAt: time.Now()})
		stub, err := a.claudeRemoteSearchStubDir(ws)
		if err != nil {
			return nil, err
		}
		updated["path"] = stub
		return updated, nil
	case "Write", "Edit", "MultiEdit":
		key := "file_path"
		if stringValue(input[key]) == "" {
			return nil, nil
		}
		local, remote, err := a.hydrateClaudePath(ctx, ws, stringValue(input[key]), false)
		if err != nil && tool != "Write" {
			return nil, err
		}
		if local == "" {
			local, remote, err = a.projectedLocalPath(ws, stringValue(input[key]))
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
				return nil, err
			}
		}
		updated[key] = claudeLocalToolPath(ws, local)
		a.recordProjectionFile(ws, remote, local, true, false)
		return updated, nil
	default:
		return nil, nil
	}
}

func remapClaudeRemoteBashCommand(ws Workspace, command string) string {
	if ws.SSH == nil || strings.TrimSpace(ws.LocalProjectionRoot) == "" || strings.TrimSpace(ws.SSH.RemoteCWD) == "" {
		return command
	}
	remoteRoot := remotePathClean(ws.SSH.RemoteCWD)
	for _, localRoot := range claudeProjectionRootAliases(ws.LocalProjectionRoot) {
		command = strings.ReplaceAll(command, localRoot, remoteRoot)
	}
	return command
}

func claudeProjectionRootAliases(root string) []string {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || clean == "" {
		return nil
	}
	seen := map[string]bool{}
	var aliases []string
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || path == "" || seen[path] {
			return
		}
		seen[path] = true
		aliases = append(aliases, path)
	}
	add(clean)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		add(resolved)
	}
	sort.SliceStable(aliases, func(i, j int) bool {
		return len(aliases[i]) > len(aliases[j])
	})
	return aliases
}

func (a *app) postClaudeRemoteTool(ctx context.Context, ws Workspace, tool, toolUseID string, input map[string]any, payload map[string]any) (any, error) {
	if tool == "Read" {
		return nil, nil
	}
	if tool == "Glob" || tool == "Grep" {
		result, ok := a.takeClaudeRemoteToolResult(ws.ID, toolUseID)
		if !ok {
			return nil, fmt.Errorf("missing cached remote %s result for tool_use_id %q", tool, toolUseID)
		}
		if result.Tool != tool {
			return nil, fmt.Errorf("cached remote result tool mismatch: got %s, want %s", result.Tool, tool)
		}
		if err := validateClaudeNativeToolOutput(tool, result.Output); err != nil {
			return nil, err
		}
		return result.Output, nil
	}
	if tool != "Write" && tool != "Edit" && tool != "MultiEdit" {
		return nil, nil
	}
	path := stringValue(input["file_path"])
	if path == "" {
		return nil, nil
	}
	local, remote, err := a.projectionLocalPathFromToolInput(ws, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	body, err := os.ReadFile(local)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := a.ssh.call(callCtx, ws, "write", remoteWriteParams(remote, body), nil); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *app) rollbackClaudeRemoteTool(ctx context.Context, ws Workspace, tool string, input map[string]any) error {
	if tool != "Write" && tool != "Edit" && tool != "MultiEdit" {
		return nil
	}
	path := stringValue(input["file_path"])
	if path == "" {
		return nil
	}
	local, remote, err := a.projectionLocalPathFromToolInput(ws, path)
	if err != nil {
		return err
	}
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "read", map[string]any{"path": remote}, &out); err != nil {
		_ = os.Remove(local)
		a.recordProjectionFile(ws, remote, local, false, false)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return err
	}
	if err := os.WriteFile(local, body, 0o600); err != nil {
		return err
	}
	a.recordProjectionFile(ws, remote, local, false, true)
	return nil
}

func (a *app) putClaudeRemoteToolResult(workspaceID, toolUseID string, result claudeRemoteToolResult) {
	if strings.TrimSpace(toolUseID) == "" {
		return
	}
	a.claudeRemoteToolMu.Lock()
	defer a.claudeRemoteToolMu.Unlock()
	if a.claudeRemoteTool == nil {
		a.claudeRemoteTool = map[string]claudeRemoteToolResult{}
	}
	a.claudeRemoteTool[claudeRemoteToolKey(workspaceID, toolUseID)] = result
}

func (a *app) takeClaudeRemoteToolResult(workspaceID, toolUseID string) (claudeRemoteToolResult, bool) {
	if strings.TrimSpace(toolUseID) == "" {
		return claudeRemoteToolResult{}, false
	}
	a.claudeRemoteToolMu.Lock()
	defer a.claudeRemoteToolMu.Unlock()
	key := claudeRemoteToolKey(workspaceID, toolUseID)
	result, ok := a.claudeRemoteTool[key]
	if ok {
		delete(a.claudeRemoteTool, key)
	}
	return result, ok
}

func (a *app) deleteClaudeRemoteToolResult(workspaceID, toolUseID string) {
	if strings.TrimSpace(toolUseID) == "" {
		return
	}
	a.claudeRemoteToolMu.Lock()
	defer a.claudeRemoteToolMu.Unlock()
	delete(a.claudeRemoteTool, claudeRemoteToolKey(workspaceID, toolUseID))
}

func claudeRemoteToolKey(workspaceID, toolUseID string) string {
	return workspaceID + "\x00" + toolUseID
}

func (a *app) claudeRemoteSearchStubDir(ws Workspace) (string, error) {
	dir := filepath.Join(ws.LocalProjectionRoot, ".astralops", "claude-stubs", "search")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func (a *app) syncClaudeRemoteDirectorySkeleton(ctx context.Context, ws Workspace) error {
	if ws.Target != "ssh" || ws.SSH == nil {
		return nil
	}
	if strings.TrimSpace(ws.LocalProjectionRoot) == "" || strings.TrimSpace(ws.SSH.RemoteCWD) == "" {
		return nil
	}
	if err := os.MkdirAll(ws.LocalProjectionRoot, 0o700); err != nil {
		return err
	}
	var out map[string]any
	if err := a.ssh.call(ctx, ws, "dirs", map[string]any{"path": ws.SSH.RemoteCWD, "limit": 5000}, &out); err != nil {
		return err
	}
	for _, value := range arrayValue(out["dirs"]) {
		remote := stringValue(value)
		if remote == "" {
			continue
		}
		local, _, err := a.projectedLocalPath(ws, remote)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(local, 0o700); err != nil {
			return err
		}
	}
	for _, value := range arrayValue(out["files"]) {
		remote := stringValue(value)
		if remote == "" {
			continue
		}
		local, _, err := a.projectedLocalPath(ws, remote)
		if err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(local, os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		_ = file.Close()
		a.recordProjectionFile(ws, remote, local, false, false)
	}
	return nil
}

func (a *app) remoteClaudeGlobOutput(ctx context.Context, ws Workspace, input map[string]any) (map[string]any, error) {
	cwd := firstString(input["path"], ws.SSH.RemoteCWD)
	_, remoteCWD, err := a.projectedLocalPath(ws, cwd)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	start := time.Now()
	var out map[string]any
	if err := a.ssh.call(callCtx, ws, "glob", map[string]any{"cwd": remoteCWD, "pattern": stringValue(input["pattern"])}, &out); err != nil {
		return nil, err
	}
	matches := []string{}
	for _, value := range arrayValue(out["matches"]) {
		path := stringValue(value)
		if path != "" {
			local, _, err := a.projectedLocalPath(ws, path)
			if err != nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(local, os.O_CREATE, 0o600)
			if err != nil {
				return nil, err
			}
			_ = file.Close()
			matches = append(matches, claudeLocalToolPath(ws, local))
		}
	}
	sort.Strings(matches)
	return map[string]any{
		"filenames":  matches,
		"durationMs": int(time.Since(start).Milliseconds()),
		"numFiles":   len(matches),
		"truncated":  false,
	}, nil
}

func (a *app) remoteClaudeGrepOutput(ctx context.Context, ws Workspace, input map[string]any) (map[string]any, error) {
	cwd := firstString(input["path"], ws.SSH.RemoteCWD)
	_, remoteCWD, err := a.projectedLocalPath(ws, cwd)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var out map[string]any
	if err := a.ssh.call(callCtx, ws, "grep", map[string]any{
		"cwd":     remoteCWD,
		"pattern": stringValue(input["pattern"]),
		"glob":    stringValue(input["glob"]),
		"limit":   maxClaudeRemoteGrepMatches,
	}, &out); err != nil {
		return nil, err
	}
	matches := []map[string]any{}
	seen := map[string]bool{}
	filenames := []string{}
	for _, value := range arrayValue(out["matches"]) {
		match := mapValue(value)
		path := stringValue(match["path"])
		if path == "" {
			continue
		}
		local, _, err := a.hydrateClaudePath(ctx, ws, path, false)
		if err != nil {
			continue
		}
		display := claudeLocalToolPath(ws, local)
		matches = append(matches, map[string]any{"path": display, "line": int(numberValue(match["line"])), "text": stringValue(match["text"])})
		if !seen[display] {
			seen[display] = true
			filenames = append(filenames, display)
		}
	}
	sort.Strings(filenames)
	if stringValue(input["output_mode"]) == "content" {
		lines := make([]string, 0, len(matches))
		includeLine := boolValue(input["-n"]) || boolValue(input["line_numbers"])
		for _, match := range matches {
			if includeLine {
				lines = append(lines, fmt.Sprintf("%s:%d:%s", stringValue(match["path"]), int(numberValue(match["line"])), stringValue(match["text"])))
			} else {
				lines = append(lines, fmt.Sprintf("%s:%s", stringValue(match["path"]), stringValue(match["text"])))
			}
		}
		return map[string]any{
			"mode":      "content",
			"numFiles":  0,
			"filenames": []string{},
			"content":   strings.Join(lines, "\n"),
			"numLines":  len(lines),
		}, nil
	}
	return map[string]any{
		"mode":      "files_with_matches",
		"filenames": filenames,
		"numFiles":  len(filenames),
	}, nil
}

func claudeLocalToolPath(ws Workspace, local string) string {
	local = filepath.Clean(local)
	root := filepath.Clean(ws.LocalProjectionRoot)
	rel, err := filepath.Rel(root, local)
	if err != nil || pathEscapesRoot(rel) {
		return local
	}
	if rel == "." || rel == "" {
		return "."
	}
	return filepath.ToSlash(rel)
}

func validateClaudeNativeToolOutput(tool string, output map[string]any) error {
	switch tool {
	case "Read":
		file := mapValue(output["file"])
		if stringValue(output["type"]) != "text" || file == nil {
			return fmt.Errorf("invalid Read output shape")
		}
		if stringValue(file["filePath"]) == "" {
			return fmt.Errorf("invalid Read output shape: missing file.filePath")
		}
		if _, ok := file["content"].(string); !ok {
			return fmt.Errorf("invalid Read output shape: file.content must be string")
		}
		for _, key := range []string{"numLines", "startLine", "totalLines"} {
			if _, ok := intLikeValue(file[key]); !ok {
				return fmt.Errorf("invalid Read output shape: file.%s must be numeric", key)
			}
		}
	case "Glob":
		if _, ok := stringSliceValue(output["filenames"]); !ok {
			return fmt.Errorf("invalid Glob output shape: filenames must be []string")
		}
		for _, key := range []string{"durationMs", "numFiles"} {
			if _, ok := intLikeValue(output[key]); !ok {
				return fmt.Errorf("invalid Glob output shape: %s must be numeric", key)
			}
		}
		if _, ok := output["truncated"].(bool); !ok {
			return fmt.Errorf("invalid Glob output shape: truncated must be bool")
		}
	case "Grep":
		mode := stringValue(output["mode"])
		switch mode {
		case "files_with_matches":
			if _, ok := stringSliceValue(output["filenames"]); !ok {
				return fmt.Errorf("invalid Grep output shape: filenames must be []string")
			}
			if _, ok := intLikeValue(output["numFiles"]); !ok {
				return fmt.Errorf("invalid Grep output shape: numFiles must be numeric")
			}
		case "content":
			if _, ok := stringSliceValue(output["filenames"]); !ok {
				return fmt.Errorf("invalid Grep output shape: filenames must be []string")
			}
			if _, ok := output["content"].(string); !ok {
				return fmt.Errorf("invalid Grep output shape: content must be string")
			}
			for _, key := range []string{"numFiles", "numLines"} {
				if _, ok := intLikeValue(output[key]); !ok {
					return fmt.Errorf("invalid Grep output shape: %s must be numeric", key)
				}
			}
		default:
			return fmt.Errorf("unsupported Grep output mode %q", mode)
		}
	default:
		return fmt.Errorf("unsupported Claude native output tool %q", tool)
	}
	return nil
}

func (a *app) hydrateClaudePath(ctx context.Context, ws Workspace, requested string, dir bool) (string, string, error) {
	local, remote, err := a.projectedLocalPath(ws, requested)
	if err != nil {
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if dir {
		var entries []map[string]any
		if err := a.ssh.call(callCtx, ws, "list", map[string]any{"path": remote}, &entries); err != nil {
			return "", "", err
		}
		if err := os.MkdirAll(local, 0o700); err != nil {
			return "", "", err
		}
		for _, entry := range entries {
			entryLocal, _, err := a.projectedLocalPath(ws, stringValue(entry["path"]))
			if err != nil {
				continue
			}
			if boolValue(entry["is_dir"]) {
				_ = os.MkdirAll(entryLocal, 0o700)
				a.recordProjectionFile(ws, stringValue(entry["path"]), entryLocal, false, true)
			} else {
				a.recordProjectionFile(ws, stringValue(entry["path"]), entryLocal, false, false)
			}
		}
		a.recordProjectionFile(ws, remote, local, false, true)
		return local, remote, nil
	}
	var stat map[string]any
	if err := a.ssh.call(callCtx, ws, "stat", map[string]any{"path": remote}, &stat); err == nil {
		size := int64(numberValue(stat["size"]))
		if size > maxClaudeHydrateBytes {
			return "", remote, fmt.Errorf("file %s is too large to load automatically (%d bytes)", remote, size)
		}
	}
	var out map[string]any
	if err := a.ssh.call(callCtx, ws, "read", map[string]any{"path": remote}, &out); err != nil {
		return "", remote, err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return "", "", err
	}
	body, err := remoteReadBytes(out)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(local, body, 0o600); err != nil {
		return "", "", err
	}
	a.recordProjectionFile(ws, remote, local, false, true)
	return local, remote, nil
}

func (a *app) projectedLocalPath(ws Workspace, requested string) (string, string, error) {
	remoteRoot := remotePathClean(ws.SSH.RemoteCWD)
	remote := strings.TrimSpace(requested)
	if remote == "" {
		remote = remoteRoot
	}
	if strings.TrimSpace(ws.LocalProjectionRoot) != "" {
		if remapped, err := a.remotePathFromProjected(ws, remote); err == nil {
			remote = remapped
		}
	}
	if !remotePathIsAbs(remote) {
		remote = remotePathJoin(remoteRoot, remote)
	}
	remote = remotePathClean(remote)
	rel, err := remotePathRel(remoteRoot, remote)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		rel = ""
	}
	if !pathEscapesRoot(rel) {
		return filepath.Join(ws.LocalProjectionRoot, filepath.FromSlash(rel)), remote, nil
	}
	absRel := strings.TrimPrefix(remote, "/")
	if absRel == "" {
		absRel = "_root"
	}
	if pathEscapesRoot(absRel) {
		return "", "", fmt.Errorf("path %q cannot be projected safely", remote)
	}
	return filepath.Join(ws.LocalProjectionRoot, filepath.FromSlash(claudeRemoteAbsProjectionDir), filepath.FromSlash(absRel)), remote, nil
}

func (a *app) remotePathFromProjected(ws Workspace, local string) (string, error) {
	if !filepath.IsAbs(local) {
		local = filepath.Join(filepath.Clean(ws.LocalProjectionRoot), filepath.FromSlash(local))
	}
	local = filepath.Clean(local)
	absRoot := filepath.Join(filepath.Clean(ws.LocalProjectionRoot), filepath.FromSlash(claudeRemoteAbsProjectionDir))
	if rel, err := filepath.Rel(absRoot, local); err == nil {
		if rel == "." {
			return "/", nil
		}
		if !pathEscapesRoot(rel) {
			return remotePathClean("/" + filepath.ToSlash(rel)), nil
		}
	}
	rel, err := filepath.Rel(filepath.Clean(ws.LocalProjectionRoot), local)
	if err != nil {
		return "", err
	}
	if rel == "." {
		rel = ""
	}
	if pathEscapesRoot(rel) {
		return "", fmt.Errorf("local path %q escapes workspace cache", local)
	}
	return remotePathClean(remotePathJoin(ws.SSH.RemoteCWD, filepath.ToSlash(rel))), nil
}

func (a *app) projectionLocalPathFromToolInput(ws Workspace, inputPath string) (string, string, error) {
	if strings.TrimSpace(inputPath) == "" {
		return "", "", os.ErrNotExist
	}
	local := filepath.Clean(inputPath)
	if !filepath.IsAbs(local) {
		local = filepath.Join(filepath.Clean(ws.LocalProjectionRoot), filepath.FromSlash(inputPath))
	}
	remote, err := a.remotePathFromProjected(ws, local)
	if err != nil {
		return "", "", err
	}
	return local, remote, nil
}

func copyStringAny(input map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range input {
		out[k] = v
	}
	return out
}

func arrayValue(value any) []any {
	if value == nil {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	return nil
}

func stringSliceValue(value any) ([]string, bool) {
	switch items := value.(type) {
	case []string:
		return items, true
	case []any:
		out := make([]string, 0, len(items))
		for _, item := range items {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func intLikeValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}
