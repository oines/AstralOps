package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const version = "0.1.0"

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			_ = enc.Encode(response{Error: err.Error()})
			continue
		}
		result, err := dispatch(req)
		res := response{ID: req.ID, Result: result}
		if err != nil {
			res.Error = err.Error()
			res.Result = nil
		}
		_ = enc.Encode(res)
	}
}

func dispatch(req request) (any, error) {
	switch req.Method {
	case "hello":
		host, _ := os.Hostname()
		return map[string]any{"ok": true, "version": version, "hostname": host, "os": runtimeOS()}, nil
	case "stat":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		info, err := os.Stat(p.Path)
		if err != nil {
			return nil, err
		}
		return fileInfo(p.Path, info), nil
	case "list":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(p.Path)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fileInfo(filepath.Join(p.Path, e.Name()), info))
		}
		return out, nil
	case "read":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		body, err := os.ReadFile(p.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": p.Path, "content": string(body)}, nil
	case "write":
		var p writeParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
			return nil, err
		}
		return map[string]any{"path": p.Path}, os.WriteFile(p.Path, []byte(p.Content), 0o644)
	case "mkdir":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"path": p.Path}, os.MkdirAll(p.Path, 0o755)
	case "remove":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"path": p.Path}, os.RemoveAll(p.Path)
	case "glob":
		var p globParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		matches, err := filepath.Glob(filepath.Join(p.CWD, p.Pattern))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		return map[string]any{"matches": matches}, nil
	case "grep":
		var p grepParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return grep(p)
	case "exec":
		var p execParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return runExec(p)
	case "apply_patch":
		return nil, errors.New("apply_patch rpc is reserved for the projection layer and is not implemented in the raw proxy")
	case "pty_start", "pty_write", "pty_resize", "pty_kill":
		return nil, errors.New("pty rpc is not implemented in this milestone")
	case "git_status":
		var p cwdParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return runExec(execParams{CWD: p.CWD, Command: "git status --short"})
	case "git_diff":
		var p cwdParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return runExec(execParams{CWD: p.CWD, Command: "git diff --"})
	default:
		return nil, fmt.Errorf("unknown method %q", req.Method)
	}
}

type pathParams struct {
	Path string `json:"path"`
}

type writeParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type globParams struct {
	CWD     string `json:"cwd"`
	Pattern string `json:"pattern"`
}

type grepParams struct {
	CWD     string `json:"cwd"`
	Pattern string `json:"pattern"`
	Glob    string `json:"glob"`
	Limit   int    `json:"limit"`
}

type execParams struct {
	CWD     string            `json:"cwd"`
	Command string            `json:"command"`
	Env     map[string]string `json:"env"`
	Timeout int               `json:"timeout_ms"`
}

type cwdParams struct {
	CWD string `json:"cwd"`
}

func parse(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func fileInfo(path string, info fs.FileInfo) map[string]any {
	return map[string]any{
		"path":     path,
		"name":     info.Name(),
		"size":     info.Size(),
		"mode":     info.Mode().String(),
		"is_dir":   info.IsDir(),
		"modified": info.ModTime().UTC().Format(time.RFC3339Nano),
	}
}

func grep(p grepParams) (any, error) {
	if p.CWD == "" {
		p.CWD = "."
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	err = filepath.WalkDir(p.CWD, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if p.Glob != "" {
			ok, _ := filepath.Match(p.Glob, filepath.Base(path))
			if !ok {
				return nil
			}
		}
		body, err := os.ReadFile(path)
		if err != nil || strings.ContainsRune(string(body), '\x00') {
			return nil
		}
		for i, line := range strings.Split(string(body), "\n") {
			if re.MatchString(line) {
				out = append(out, map[string]any{"path": path, "line": i + 1, "text": line})
				if len(out) >= p.Limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return map[string]any{"matches": out}, err
}

func runExec(p execParams) (any, error) {
	if strings.TrimSpace(p.Command) == "" {
		return nil, errors.New("command required")
	}
	if p.CWD == "" {
		p.CWD = "."
	}
	start := time.Now()
	cmd := exec.Command("/bin/sh", "-lc", p.Command)
	cmd.Dir = p.CWD
	cmd.Env = os.Environ()
	for k, v := range p.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	return map[string]any{
		"command":     p.Command,
		"cwd":         p.CWD,
		"exit_code":   exitCode,
		"output":      string(out),
		"duration_ms": time.Since(start).Milliseconds(),
	}, nil
}

func runtimeOS() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
