package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func runClaudeRemoteHookHelper(args []string) bool {
	if len(args) == 0 || args[0] != "claude-remote-hook" {
		return false
	}
	if err := runClaudeRemoteHookHelperMain(args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

func runClaudeRemoteHookHelperMain(args []string) error {
	base := os.Getenv("ASTRALOPS_DAEMON")
	token := os.Getenv("ASTRALOPS_TOKEN")
	workspace := os.Getenv("ASTRALOPS_WORKSPACE_ID")
	if base == "" || token == "" || workspace == "" {
		return fmt.Errorf("missing AstralOps hook environment")
	}
	if len(args) > 0 && args[0] == "exec" {
		if len(args) < 2 {
			return fmt.Errorf("missing encoded command")
		}
		command, err := base64.StdEncoding.DecodeString(args[1])
		if err != nil {
			return err
		}
		var result map[string]any
		if err := postClaudeRemoteHelper(base, token, "/v1/workspaces/"+workspace+"/exec", map[string]any{"command": string(command)}, &result); err != nil {
			return err
		}
		_, _ = io.WriteString(os.Stdout, firstString(result["stdout"], result["output"]))
		_, _ = io.WriteString(os.Stderr, stringValue(result["stderr"]))
		os.Exit(int(numberValue(result["exit_code"])))
	}

	var payload map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&payload); err != nil && err != io.EOF {
		return err
	}
	var result map[string]any
	if err := postClaudeRemoteHelper(base, token, "/v1/workspaces/"+workspace+"/claude-hook", payload, &result); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func postClaudeRemoteHelper(base, token, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		text, _ := io.ReadAll(res.Body)
		return fmt.Errorf("AstralOps hook request failed: %s: %s", res.Status, string(text))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}
