package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type mcpMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func runClaudeRemoteMCPHelper(args []string) bool {
	if len(args) == 0 || args[0] != "claude-remote-mcp" {
		return false
	}
	if err := runClaudeRemoteMCPHelperMain(os.Stdin, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

func runClaudeRemoteMCPHelperMain(stdin io.Reader, stdout io.Writer) error {
	server := &claudeRemoteMCPServer{
		base:      os.Getenv("ASTRALOPS_DAEMON"),
		token:     os.Getenv("ASTRALOPS_TOKEN"),
		workspace: os.Getenv("ASTRALOPS_WORKSPACE_ID"),
		out:       stdout,
	}
	if server.base == "" || server.token == "" || server.workspace == "" {
		return fmt.Errorf("missing AstralOps MCP environment")
	}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := server.handleLine(line); err != nil {
			return err
		}
	}
	return scanner.Err()
}

type claudeRemoteMCPServer struct {
	base      string
	token     string
	workspace string
	out       io.Writer
}

func (s *claudeRemoteMCPServer) handleLine(line []byte) error {
	var msg mcpMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return err
	}
	if msg.ID == nil {
		return nil
	}
	switch msg.Method {
	case "initialize":
		return s.writeResult(msg.ID, map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "astralops_remote", "version": version},
		})
	case "tools/list":
		return s.writeResult(msg.ID, map[string]any{"tools": claudeRemoteMCPTools()})
	case "tools/call":
		return s.callTool(msg.ID, msg.Params)
	case "ping":
		return s.writeResult(msg.ID, map[string]any{})
	default:
		return s.writeError(msg.ID, -32601, "method not found")
	}
}

func (s *claudeRemoteMCPServer) callTool(id any, raw json.RawMessage) error {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return s.writeError(id, -32602, err.Error())
	}
	var result map[string]any
	err := postClaudeRemoteHelper(s.base, s.token, "/v1/workspaces/"+s.workspace+"/claude-remote-tool", map[string]any{
		"tool":      params.Name,
		"arguments": params.Arguments,
	}, &result)
	if err != nil {
		return s.writeResult(id, map[string]any{
			"isError": true,
			"content": []map[string]any{{
				"type": "text",
				"text": err.Error(),
			}},
		})
	}
	output := mapValue(result["output"])
	text, _ := json.Marshal(output)
	mcpResult := map[string]any{
		"content": []map[string]any{{
			"type": "text",
			"text": string(text),
		}},
		"structuredContent": output,
	}
	if boolValue(firstNonNil(result["is_error"], result["isError"])) {
		mcpResult["isError"] = true
	}
	return s.writeResult(id, mcpResult)
}

func (s *claudeRemoteMCPServer) writeResult(id any, result any) error {
	return json.NewEncoder(s.out).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	})
}

func (s *claudeRemoteMCPServer) writeError(id any, code int, message string) error {
	return json.NewEncoder(s.out).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func claudeRemoteMCPTools() []map[string]any {
	return []map[string]any{
		remoteMCPTool("read", "Read a file from the SSH remote workspace. Returns the Claude Code native Read JSON shape.", map[string]any{
			"file_path": stringSchema("Remote path to read. Relative paths resolve from the SSH cwd."),
			"offset":    numberSchema("Optional 1-based start line."),
			"limit":     numberSchema("Optional maximum number of lines."),
		}, []string{"file_path"}),
		remoteMCPTool("write", "Write a file in the SSH remote workspace. Returns the Claude Code native Write JSON shape.", map[string]any{
			"file_path": stringSchema("Remote path to write. Relative paths resolve from the SSH cwd."),
			"content":   stringSchema("Full file content."),
		}, []string{"file_path", "content"}),
		remoteMCPTool("edit", "Replace text in a remote file. Returns the Claude Code native Edit JSON shape.", map[string]any{
			"file_path":   stringSchema("Remote path to edit. Relative paths resolve from the SSH cwd."),
			"old_string":  stringSchema("Exact text to replace."),
			"new_string":  stringSchema("Replacement text."),
			"replace_all": boolSchema("Replace all occurrences instead of the first occurrence."),
		}, []string{"file_path", "old_string", "new_string"}),
		remoteMCPTool("multiedit", "Apply several exact replacements to one remote file.", map[string]any{
			"file_path": stringSchema("Remote path to edit. Relative paths resolve from the SSH cwd."),
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"old_string":  stringSchema("Exact text to replace."),
						"new_string":  stringSchema("Replacement text."),
						"replace_all": boolSchema("Replace all occurrences instead of the first occurrence."),
					},
					"required": []string{"old_string", "new_string"},
				},
			},
		}, []string{"file_path", "edits"}),
		remoteMCPTool("glob", "Find files on the SSH remote workspace. Returns the Claude Code native Glob JSON shape.", map[string]any{
			"pattern": stringSchema("Glob pattern."),
			"path":    stringSchema("Optional remote directory. Relative paths resolve from the SSH cwd."),
		}, []string{"pattern"}),
		remoteMCPTool("grep", "Search file contents on the SSH remote workspace. Returns the Claude Code native Grep JSON shape.", map[string]any{
			"pattern":      stringSchema("Regular expression pattern."),
			"path":         stringSchema("Optional remote directory. Relative paths resolve from the SSH cwd."),
			"glob":         stringSchema("Optional file glob filter."),
			"output_mode":  stringEnumSchema("Output mode.", []string{"files_with_matches", "content"}),
			"line_numbers": boolSchema("Include line numbers in content mode."),
		}, []string{"pattern"}),
		remoteMCPTool("bash", "Run a shell command on the SSH remote workspace. Returns the Claude Code native Bash JSON shape.", map[string]any{
			"command":    stringSchema("Command to execute remotely."),
			"cwd":        stringSchema("Optional remote cwd. Relative paths resolve from the SSH cwd."),
			"timeout_ms": numberSchema("Optional timeout in milliseconds."),
		}, []string{"command"}),
	}
}

func remoteMCPTool(name, description string, properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             required,
			"additionalProperties": false,
		},
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func numberSchema(description string) map[string]any {
	return map[string]any{"type": "number", "description": description}
}

func boolSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func stringEnumSchema(description string, values []string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}
