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
	"strings"
	"time"
)

func (a *app) ensureClaudeHookScript() (string, error) {
	dir := filepath.Join(a.store.dataDir, "runtime", "claude-remote")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "hook_bridge.py")
	body := `#!/usr/bin/env python3
import base64, json, os, sys, urllib.request

base = os.environ["ASTRALOPS_DAEMON"]
token = os.environ["ASTRALOPS_TOKEN"]
workspace = os.environ["ASTRALOPS_WORKSPACE_ID"]

def post(path, payload):
    req = urllib.request.Request(base + path, data=json.dumps(payload).encode(), headers={
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
    })
    with urllib.request.urlopen(req, timeout=120) as res:
        return json.loads(res.read().decode())

if len(sys.argv) > 1 and sys.argv[1] == "exec":
    command = base64.b64decode(sys.argv[2]).decode()
    result = post(f"/v1/workspaces/{workspace}/exec", {"command": command})
    sys.stdout.write(result.get("stdout") or result.get("output") or "")
    sys.stderr.write(result.get("stderr") or "")
    sys.exit(int(result.get("exit_code") or 0))

payload = json.loads(sys.stdin.read() or "{}")
result = post(f"/v1/workspaces/{workspace}/claude-hook", payload)
sys.stdout.write(json.dumps(result))
`
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		return "", err
	}
	return path, nil
}

func (a *app) writeClaudeRemoteSettings(ws Workspace) (string, error) {
	hook, err := a.ensureClaudeHookScript()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.store.dataDir, "runtime", "claude-remote", ws.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	command := strings.Join([]string{
		"ASTRALOPS_DAEMON=" + shellQuote("http://"+a.addr),
		"ASTRALOPS_TOKEN=" + shellQuote(a.token),
		"ASTRALOPS_WORKSPACE_ID=" + shellQuote(ws.ID),
		"python3",
		shellQuote(hook),
	}, " ")
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Read|LS|Glob|Grep|Bash|Write|Edit|MultiEdit",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": command,
					"timeout": 60,
				}},
			}},
			"PostToolUse": []map[string]any{{
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
		writeJSON(w, http.StatusOK, map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName":     firstString(payload["hook_event_name"], payload["hookEventName"], "PreToolUse"),
				"additionalContext": "AstralOps remote hook failed: " + err.Error(),
			},
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *app) processClaudeRemoteHook(ctx context.Context, ws Workspace, payload map[string]any) (map[string]any, error) {
	event := firstString(payload["hook_event_name"], payload["hookEventName"])
	tool := firstString(payload["tool_name"], payload["toolName"])
	input := mapValue(firstNonNil(payload["tool_input"], payload["toolInput"]))
	if event == "" {
		event = "PreToolUse"
	}
	switch event {
	case "PreToolUse":
		updated, contextText, err := a.preClaudeRemoteTool(ctx, ws, tool, input)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"hookEventName": "PreToolUse"}
		if len(updated) > 0 {
			out["updatedInput"] = updated
		}
		if contextText != "" {
			out["additionalContext"] = contextText
		}
		return map[string]any{"hookSpecificOutput": out}, nil
	case "PostToolUse":
		if err := a.postClaudeRemoteTool(ctx, ws, tool, input); err != nil {
			return nil, err
		}
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse"}}, nil
	default:
		return map[string]any{}, nil
	}
}

func (a *app) preClaudeRemoteTool(ctx context.Context, ws Workspace, tool string, input map[string]any) (map[string]any, string, error) {
	updated := copyStringAny(input)
	switch tool {
	case "Bash":
		command := stringValue(input["command"])
		if strings.TrimSpace(command) == "" {
			return nil, "", nil
		}
		hook, err := a.ensureClaudeHookScript()
		if err != nil {
			return nil, "", err
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(command))
		updated["command"] = strings.Join([]string{
			"ASTRALOPS_DAEMON=" + shellQuote("http://"+a.addr),
			"ASTRALOPS_TOKEN=" + shellQuote(a.token),
			"ASTRALOPS_WORKSPACE_ID=" + shellQuote(ws.ID),
			"python3",
			shellQuote(hook),
			"exec",
			shellQuote(encoded),
		}, " ")
		return updated, "AstralOps will execute this Bash command in remote cwd " + ws.SSH.RemoteCWD + ".", nil
	case "Read":
		local, remote, err := a.hydrateClaudePath(ctx, ws, stringValue(input["file_path"]), false)
		if err != nil {
			return nil, "", err
		}
		updated["file_path"] = local
		return updated, "AstralOps hydrated " + remote + " into sparse projection.", nil
	case "LS":
		local, remote, err := a.hydrateClaudePath(ctx, ws, stringValue(input["path"]), true)
		if err != nil {
			return nil, "", err
		}
		updated["path"] = local
		return updated, "AstralOps listed remote directory " + remote + " through sparse projection.", nil
	case "Glob":
		if err := a.hydrateGlobMatches(ctx, ws, input); err != nil {
			return nil, "", err
		}
		updated["path"] = ws.LocalProjectionRoot
		return updated, "AstralOps created projection placeholders for remote glob matches.", nil
	case "Grep":
		if err := a.hydrateGrepMatches(ctx, ws, input); err != nil {
			return nil, "", err
		}
		updated["path"] = ws.LocalProjectionRoot
		return updated, "AstralOps hydrated remote grep matches into sparse projection.", nil
	case "Write", "Edit", "MultiEdit":
		key := "file_path"
		if stringValue(input[key]) == "" {
			return nil, "", nil
		}
		local, remote, err := a.hydrateClaudePath(ctx, ws, stringValue(input[key]), false)
		if err != nil && tool != "Write" {
			return nil, "", err
		}
		if local == "" {
			local, remote, err = a.projectedLocalPath(ws, stringValue(input[key]))
			if err != nil {
				return nil, "", err
			}
			if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
				return nil, "", err
			}
		}
		updated[key] = local
		return updated, "AstralOps mapped write target " + remote + " into sparse projection.", nil
	default:
		return nil, "", nil
	}
}

func (a *app) postClaudeRemoteTool(ctx context.Context, ws Workspace, tool string, input map[string]any) error {
	if tool != "Write" && tool != "Edit" && tool != "MultiEdit" {
		return nil
	}
	path := stringValue(input["file_path"])
	if path == "" {
		return nil
	}
	remote, err := a.remotePathFromProjected(ws, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := proxy.call(callCtx, "write", map[string]any{"path": remote, "content": string(body)}, nil); err != nil {
		return err
	}
	a.recordProjectionFile(ws, remote, path, false, false)
	return nil
}

func (a *app) hydrateClaudePath(ctx context.Context, ws Workspace, requested string, dir bool) (string, string, error) {
	local, remote, err := a.projectedLocalPath(ws, requested)
	if err != nil {
		return "", "", err
	}
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if dir {
		var entries []map[string]any
		if err := proxy.call(callCtx, "list", map[string]any{"path": remote}, &entries); err != nil {
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
			} else if _, err := os.Stat(entryLocal); os.IsNotExist(err) {
				_ = os.MkdirAll(filepath.Dir(entryLocal), 0o700)
				_ = os.WriteFile(entryLocal, nil, 0o600)
			}
		}
		a.recordProjectionFile(ws, remote, local, false, true)
		return local, remote, nil
	}
	var out map[string]any
	if err := proxy.call(callCtx, "read", map[string]any{"path": remote}, &out); err != nil {
		return "", remote, err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(local, []byte(stringValue(out["content"])), 0o600); err != nil {
		return "", "", err
	}
	a.recordProjectionFile(ws, remote, local, false, true)
	return local, remote, nil
}

func (a *app) hydrateGlobMatches(ctx context.Context, ws Workspace, input map[string]any) error {
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		return err
	}
	cwd := firstString(input["path"], ws.SSH.RemoteCWD)
	var out map[string]any
	if err := proxy.call(ctx, "glob", map[string]any{"cwd": cwd, "pattern": stringValue(input["pattern"])}, &out); err != nil {
		return err
	}
	for _, value := range arrayValue(out["matches"]) {
		local, _, err := a.projectedLocalPath(ws, stringValue(value))
		if err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(local), 0o700)
		if _, err := os.Stat(local); os.IsNotExist(err) {
			_ = os.WriteFile(local, nil, 0o600)
		}
	}
	return nil
}

func (a *app) hydrateGrepMatches(ctx context.Context, ws Workspace, input map[string]any) error {
	proxy, _, err := a.ssh.proxyFor(ctx, ws)
	if err != nil {
		return err
	}
	cwd := firstString(input["path"], ws.SSH.RemoteCWD)
	var out map[string]any
	if err := proxy.call(ctx, "grep", map[string]any{"cwd": cwd, "pattern": stringValue(input["pattern"]), "glob": stringValue(input["glob"]), "limit": 200}, &out); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, value := range arrayValue(out["matches"]) {
		path := stringValue(mapValue(value)["path"])
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		_, _, _ = a.hydrateClaudePath(ctx, ws, path, false)
	}
	return nil
}

func (a *app) projectedLocalPath(ws Workspace, requested string) (string, string, error) {
	remoteRoot := filepath.Clean(ws.SSH.RemoteCWD)
	remote := strings.TrimSpace(requested)
	if remote == "" {
		remote = remoteRoot
	}
	if !filepath.IsAbs(remote) {
		remote = filepath.Join(remoteRoot, remote)
	}
	remote = filepath.Clean(remote)
	rel, err := filepath.Rel(remoteRoot, remote)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		rel = ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("path %q escapes remote cwd %q", remote, remoteRoot)
	}
	return filepath.Join(ws.LocalProjectionRoot, rel), remote, nil
}

func (a *app) remotePathFromProjected(ws Workspace, local string) (string, error) {
	local = filepath.Clean(local)
	rel, err := filepath.Rel(filepath.Clean(ws.LocalProjectionRoot), local)
	if err != nil {
		return "", err
	}
	if rel == "." {
		rel = ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("local path %q escapes projection", local)
	}
	return filepath.Clean(filepath.Join(ws.SSH.RemoteCWD, rel)), nil
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
