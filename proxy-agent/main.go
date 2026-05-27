package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

var version = "dev"

var supportedMethods = []string{
	"hello",
	"stat",
	"list",
	"read",
	"dirs",
	"write",
	"mkdir",
	"remove",
	"copy",
	"glob",
	"grep",
	"exec",
	"exec_start",
	"exec_kill",
	"apply_patch",
	"pty_start",
	"pty_write",
	"pty_resize",
	"pty_kill",
}

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Event  string `json:"event,omitempty"`
}

var (
	rootCWD string
	encMu   sync.Mutex
	ptyMu   sync.Mutex
	ptys    = map[string]*ptySession{}
	execMu  sync.Mutex
	execs   = map[string]*execSession{}
	encoder *json.Encoder

	proxyAgentShutdownOnce sync.Once
	proxyAgentExecutable   = os.Executable
	removeProxyAgentFile   = os.Remove
)

type execSession struct {
	id     string
	cmd    *exec.Cmd
	cancel context.CancelFunc
}

type ptySession struct {
	id       string
	cmd      *exec.Cmd
	file     *os.File
	once     sync.Once
	exitCode int
}

func main() {
	flag.StringVar(&rootCWD, "cwd", "", "remote cwd for relative paths")
	flag.Parse()
	if rootCWD == "" {
		rootCWD, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(rootCWD); err == nil {
		rootCWD = filepath.Clean(abs)
	}
	defer shutdownProxyAgent()
	installShutdownSignalHandler(shutdownProxyAgent)
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	encoder = json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			writeLine(response{Error: err.Error()})
			continue
		}
		result, err := dispatch(req)
		res := response{ID: req.ID, Result: result}
		if err != nil {
			res.Error = err.Error()
			res.Result = nil
		}
		writeLine(res)
	}
}

func shutdownProxyAgent() {
	proxyAgentShutdownOnce.Do(func() {
		cleanupManagedProcesses()
		removeSelfExecutable()
	})
}

func cleanupManagedProcesses() {
	cleanupManagedPTYs()
	cleanupManagedExecs()
}

func cleanupManagedPTYs() {
	ptyMu.Lock()
	sessions := make([]*ptySession, 0, len(ptys))
	for _, session := range ptys {
		sessions = append(sessions, session)
	}
	ptyMu.Unlock()
	for _, session := range sessions {
		finishPTY(session, true)
	}
}

func cleanupManagedExecs() {
	execMu.Lock()
	sessions := make([]*execSession, 0, len(execs))
	for _, session := range execs {
		sessions = append(sessions, session)
	}
	execMu.Unlock()
	for _, session := range sessions {
		session.cancel()
		if session.cmd != nil && session.cmd.Process != nil {
			_ = killCommandProcessGroup(session.cmd)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(sessions) > 0 && time.Now().Before(deadline) {
		execMu.Lock()
		remaining := len(execs)
		execMu.Unlock()
		if remaining == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func removeSelfExecutable() {
	path, err := proxyAgentExecutable()
	if err != nil || strings.TrimSpace(path) == "" {
		return
	}
	_ = removeProxyAgentFile(path)
}

func dispatch(req request) (any, error) {
	switch req.Method {
	case "hello":
		host, _ := os.Hostname()
		user := os.Getenv("USER")
		if user == "" {
			user = os.Getenv("LOGNAME")
		}
		rg := rgCapability()
		return map[string]any{
			"ok":           true,
			"version":      version,
			"hostname":     host,
			"user":         user,
			"os":           runtimeOS(),
			"cwd":          rootCWD,
			"shell":        os.Getenv("SHELL"),
			"capabilities": map[string]any{"rg": rg, "methods": supportedMethods},
		}, nil
	case "stat":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		return fileInfo(path, info), nil
	case "list":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		out := []any{}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, fileInfo(filepath.Join(path, e.Name()), info))
		}
		return out, nil
	case "read":
		var p pathParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "content": string(body), "dataBase64": base64.StdEncoding.EncodeToString(body)}, nil
	case "dirs":
		var p dirsParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return listDirs(p)
	case "write":
		var p writeParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		body := []byte(p.Content)
		if p.DataBase64 != "" {
			decoded, err := base64.StdEncoding.DecodeString(p.DataBase64)
			if err != nil {
				return nil, err
			}
			body = decoded
		}
		return map[string]any{"path": path}, os.WriteFile(path, body, 0o644)
	case "mkdir":
		var p mkdirParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		if p.Recursive == nil || *p.Recursive {
			return map[string]any{"path": path}, os.MkdirAll(path, 0o755)
		}
		return map[string]any{"path": path}, os.Mkdir(path, 0o755)
	case "remove":
		var p removeParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		path, err := resolveRemotePath(p.Path)
		if err != nil {
			return nil, err
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) && (p.Force == nil || *p.Force) {
				return map[string]any{"path": path}, nil
			}
			return nil, statErr
		}
		if info.IsDir() && (p.Recursive == nil || *p.Recursive) {
			return map[string]any{"path": path}, os.RemoveAll(path)
		}
		if info.IsDir() {
			return map[string]any{"path": path}, os.Remove(path)
		}
		return map[string]any{"path": path}, os.Remove(path)
	case "copy":
		var p copyParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return copyPath(p)
	case "glob":
		var p globParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return glob(p)
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
	case "exec_start":
		var p execParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return startExec(p)
	case "exec_kill":
		var p ptyKillParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return killExec(p.ID), nil
	case "apply_patch":
		var p applyPatchParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return applyPatch(p)
	case "pty_start":
		var p ptyStartParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return startPTY(req.ID, p)
	case "pty_write":
		var p ptyWriteParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, writePTY(p)
	case "pty_resize":
		var p ptyResizeParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, resizePTY(p)
	case "pty_kill":
		var p ptyKillParams
		if err := parse(req.Params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, killPTY(p.ID)
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

type dirsParams struct {
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

type mkdirParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive"`
}

type removeParams struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive"`
	Force     *bool  `json:"force"`
}

type writeParams struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	DataBase64 string `json:"dataBase64"`
}

type copyParams struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Recursive   bool   `json:"recursive"`
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
	ID      string            `json:"id"`
	CWD     string            `json:"cwd"`
	Command string            `json:"command"`
	Argv    []string          `json:"argv"`
	Arg0    string            `json:"arg0"`
	Env     map[string]string `json:"env"`
	Timeout int               `json:"timeout_ms"`
}

type cwdParams struct {
	CWD string `json:"cwd"`
}

type applyPatchParams struct {
	CWD   string `json:"cwd"`
	Patch string `json:"patch"`
}

type ptyStartParams struct {
	ID    string            `json:"id"`
	CWD   string            `json:"cwd"`
	Shell string            `json:"shell"`
	Argv  []string          `json:"argv"`
	Env   map[string]string `json:"env"`
	Arg0  string            `json:"arg0"`
	Cols  uint16            `json:"cols"`
	Rows  uint16            `json:"rows"`
}

type ptyWriteParams struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

type ptyResizeParams struct {
	ID   string `json:"id"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type ptyKillParams struct {
	ID string `json:"id"`
}

func parse(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	return json.Unmarshal(raw, out)
}

func writeLine(value any) {
	encMu.Lock()
	defer encMu.Unlock()
	_ = encoder.Encode(value)
}

func resolveRemotePath(path string) (string, error) {
	if rootCWD == "" {
		if filepath.IsAbs(path) {
			rootCWD = string(filepath.Separator)
		} else if cwd, err := os.Getwd(); err == nil {
			rootCWD = cwd
		}
	}
	if strings.TrimSpace(path) == "" || path == "." {
		path = rootCWD
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(rootCWD, path)
	}
	return filepath.Clean(path), nil
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

func listDirs(p dirsParams) (any, error) {
	root, err := resolveRemotePath(p.Path)
	if err != nil {
		return nil, err
	}
	if p.Limit <= 0 {
		p.Limit = 5000
	}
	dirs := []string{}
	files := []string{}
	truncated := false
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		resolved, err := resolveRemotePath(path)
		if err != nil {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, resolved)
		} else {
			files = append(files, resolved)
		}
		if len(dirs)+len(files) >= p.Limit {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	sort.Strings(files)
	return map[string]any{"dirs": dirs, "files": files, "truncated": truncated}, nil
}

func rgCapability() map[string]any {
	path, err := exec.LookPath("rg")
	if err != nil {
		return map[string]any{"available": false}
	}
	version := ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err == nil {
		version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	}
	return map[string]any{"available": true, "path": path, "version": version}
}

func glob(p globParams) (any, error) {
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, err
	}
	if matches, err := globRG(cwd, p.Pattern); err == nil {
		return map[string]any{"matches": matches, "backend": "rg"}, nil
	}
	matches, err := globGo(cwd, p.Pattern)
	if err != nil {
		return nil, err
	}
	return map[string]any{"matches": matches, "backend": "go"}, nil
}

func globGo(cwd, pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "*"
	}
	seen := map[string]bool{}
	matches := []string{}
	for _, expanded := range expandSimpleBraceGlob(pattern) {
		items, err := filepath.Glob(filepath.Join(cwd, expanded))
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			if !seen[item] {
				seen[item] = true
				matches = append(matches, item)
			}
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func globRG(cwd, pattern string) ([]string, error) {
	if strings.TrimSpace(pattern) == "" {
		pattern = "*"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rg", "--files", "-g", pattern)
	cmd.Dir = cwd
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("rg glob failed: %w%s", err, stderrSuffix(stderr.String()))
	}
	matches := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		path, err := resolveRemotePath(filepath.Join(cwd, line))
		if err != nil {
			continue
		}
		matches = append(matches, path)
	}
	sort.Strings(matches)
	return matches, nil
}

func grep(p grepParams) (any, error) {
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, err
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}
	if matches, err := grepRG(cwd, p); err == nil {
		return map[string]any{"matches": matches, "backend": "rg"}, nil
	}
	return grepGo(cwd, p)
}

func grepRG(cwd string, p grepParams) ([]map[string]any, error) {
	args := []string{"--json", "--line-number", "--color", "never"}
	if strings.TrimSpace(p.Glob) != "" {
		args = append(args, "-g", p.Glob)
	}
	args = append(args, p.Pattern)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	matches := []map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var item map[string]any
		if json.Unmarshal(scanner.Bytes(), &item) != nil || stringValue(item["type"]) != "match" {
			continue
		}
		data := mapValue(item["data"])
		pathText := stringValue(mapValue(data["path"])["text"])
		lineText := stringValue(mapValue(data["lines"])["text"])
		path, err := resolveRemotePath(filepath.Join(cwd, pathText))
		if err != nil {
			continue
		}
		matches = append(matches, map[string]any{
			"path": path,
			"line": int(numberValue(data["line_number"])),
			"text": strings.TrimRight(lineText, "\r\n"),
		})
		if len(matches) >= p.Limit {
			return matches, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return matches, nil
}

func grepGo(cwd string, p grepParams) (any, error) {
	re, err := regexp.Compile(p.Pattern)
	if err != nil {
		return nil, err
	}
	globs := expandSimpleBraceGlob(p.Glob)
	out := []map[string]any{}
	err = filepath.WalkDir(cwd, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.TrimSpace(p.Glob) != "" {
			if !matchesAnyGrepGlob(cwd, path, globs) {
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
	return map[string]any{"matches": out, "backend": "go"}, err
}

func expandSimpleBraceGlob(pattern string) []string {
	if !strings.Contains(pattern, "{") {
		return []string{pattern}
	}
	open := strings.IndexByte(pattern, '{')
	close := -1
	depth := 0
	for i := open; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				close = i
				break
			}
		}
	}
	if open < 0 || close <= open {
		return []string{pattern}
	}
	body := pattern[open+1 : close]
	if body == "" || strings.ContainsAny(body, "{}") {
		return []string{pattern}
	}
	parts := strings.Split(body, ",")
	out := make([]string, 0, len(parts))
	prefix := pattern[:open]
	suffix := pattern[close+1:]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return []string{pattern}
		}
		out = append(out, prefix+part+suffix)
	}
	return out
}

func matchesAnyGrepGlob(cwd, path string, globs []string) bool {
	base := filepath.Base(path)
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		rel = base
	}
	rel = filepath.ToSlash(rel)
	for _, pattern := range globs {
		pattern = filepath.ToSlash(pattern)
		ok, _ := filepath.Match(pattern, base)
		if ok {
			return true
		}
		if strings.Contains(pattern, "/") {
			ok, _ = filepath.Match(pattern, rel)
			if ok {
				return true
			}
		}
	}
	return false
}

func runExec(p execParams) (any, error) {
	result, _, err := runExecWithSession(p)
	return result, err
}

func startExec(p execParams) (any, error) {
	if strings.TrimSpace(p.ID) == "" {
		return nil, errors.New("id is required")
	}
	if len(p.Argv) == 0 && strings.TrimSpace(p.Command) == "" {
		return nil, errors.New("command or argv required")
	}
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, err
	}
	if existing := lookupExec(p.ID); existing != nil {
		return nil, fmt.Errorf("exec %s already exists", p.ID)
	}
	p.CWD = cwd
	go func() {
		result, _, err := runExecWithSession(p)
		if err != nil {
			writeLine(response{ID: p.ID, Event: "exit", Result: map[string]any{"exit_code": 1, "failure": err.Error()}})
			return
		}
		writeLine(response{ID: p.ID, Event: "exit", Result: result})
	}()
	return map[string]any{"id": p.ID}, nil
}

func runExecWithSession(p execParams) (any, *execSession, error) {
	if len(p.Argv) == 0 && strings.TrimSpace(p.Command) == "" {
		return nil, nil, errors.New("command or argv required")
	}
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, nil, err
	}
	start := time.Now()
	timeout := time.Duration(p.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cmd *exec.Cmd
	command := p.Command
	if len(p.Argv) > 0 {
		cmd = exec.CommandContext(ctx, p.Argv[0], p.Argv[1:]...)
		command = strings.Join(p.Argv, "\x00")
		if p.Arg0 != "" {
			cmd.Args[0] = p.Arg0
		}
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-lc", p.Command)
	}
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range p.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	configureCommandProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	session := &execSession{id: p.ID, cmd: cmd, cancel: cancel}
	if p.ID != "" {
		registerExec(session)
		defer unregisterExec(p.ID, session)
	}
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			exitCode = 124
		}
	}
	return map[string]any{
		"command":     command,
		"cwd":         cwd,
		"exit_code":   exitCode,
		"stdout":      stdout.String(),
		"stderr":      stderr.String(),
		"output":      stdout.String() + stderr.String(),
		"duration_ms": time.Since(start).Milliseconds(),
	}, session, nil
}

func registerExec(session *execSession) {
	execMu.Lock()
	execs[session.id] = session
	execMu.Unlock()
}

func lookupExec(id string) *execSession {
	execMu.Lock()
	defer execMu.Unlock()
	return execs[id]
}

func unregisterExec(id string, session *execSession) {
	execMu.Lock()
	if execs[id] == session {
		delete(execs, id)
	}
	execMu.Unlock()
}

func killExec(id string) map[string]any {
	session := lookupExec(id)
	if session == nil {
		return map[string]any{"running": false}
	}
	session.cancel()
	if session.cmd != nil && session.cmd.Process != nil {
		_ = killCommandProcessGroup(session.cmd)
	}
	return map[string]any{"running": true}
}

func runtimeOS() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func mapValue(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}

func numberValue(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		n, _ := t.Float64()
		return n
	default:
		return 0
	}
}

func stderrSuffix(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return ": " + text
}

func copyPath(p copyParams) (any, error) {
	src, err := resolveRemotePath(p.Source)
	if err != nil {
		return nil, err
	}
	dst, err := resolveRemotePath(p.Destination)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(src)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		if !p.Recursive {
			return nil, errors.New("copying a directory requires recursive=true")
		}
		if destinationIsSameOrDescendant(src, dst) {
			return nil, errors.New("cannot copy a directory to itself or a descendant")
		}
		if err := copyDirRecursive(src, dst); err != nil {
			return nil, err
		}
		return map[string]any{"source": src, "destination": dst}, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		_ = os.Remove(dst)
		return map[string]any{"source": src, "destination": dst}, os.Symlink(target, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	in, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return nil, err
	}
	return map[string]any{"source": src, "destination": dst}, nil
}

func copyDirRecursive(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(src, entry.Name())
		targetPath := filepath.Join(dst, entry.Name())
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyDirRecursive(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sourcePath, targetPath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func destinationIsSameOrDescendant(src, dst string) bool {
	rel, err := filepath.Rel(src, dst)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func applyPatch(p applyPatchParams) (any, error) {
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Patch) == "" {
		return nil, errors.New("patch is required")
	}
	cmd := exec.Command("/bin/sh", "-lc", "git apply --whitespace=nowarn -")
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader(p.Patch)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		}
	}
	return map[string]any{"cwd": cwd, "exit_code": exitCode, "stdout": stdout.String(), "stderr": stderr.String()}, err
}

func startPTY(requestID string, p ptyStartParams) (any, error) {
	cwd, err := resolveRemotePath(p.CWD)
	if err != nil {
		return nil, err
	}
	if p.ID == "" {
		p.ID = requestID
	}
	shell := strings.TrimSpace(p.Shell)
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	if p.Cols == 0 {
		p.Cols = 100
	}
	if p.Rows == 0 {
		p.Rows = 28
	}
	var cmd *exec.Cmd
	if len(p.Argv) > 0 {
		cmd = exec.Command(p.Argv[0], p.Argv[1:]...)
	} else {
		cmd = exec.Command(shell, "-l")
	}
	cmd.Dir = cwd
	cmd.Env = terminalEnv(os.Environ())
	for key, value := range p.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if p.Arg0 != "" {
		cmd.Args[0] = p.Arg0
	}
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: p.Rows, Cols: p.Cols})
	if err != nil {
		return nil, err
	}
	session := &ptySession{id: p.ID, cmd: cmd, file: file}
	ptyMu.Lock()
	ptys[p.ID] = session
	ptyMu.Unlock()
	go pumpPTY(session)
	return map[string]any{"id": p.ID, "cwd": cwd, "shell": filepath.Base(shell)}, nil
}

func pumpPTY(session *ptySession) {
	buf := make([]byte, 4096)
	for {
		n, err := session.file.Read(buf)
		if n > 0 {
			writeLine(response{ID: session.id, Event: "output", Result: map[string]any{"data": string(buf[:n])}})
		}
		if err != nil {
			exitCode := finishPTY(session, false)
			writeLine(response{ID: session.id, Event: "exit", Result: map[string]any{"exit_code": exitCode}})
			return
		}
	}
}

func lookupPTY(id string) (*ptySession, error) {
	ptyMu.Lock()
	defer ptyMu.Unlock()
	session := ptys[id]
	if session == nil {
		return nil, fmt.Errorf("unknown pty %s", id)
	}
	return session, nil
}

func writePTY(p ptyWriteParams) error {
	session, err := lookupPTY(p.ID)
	if err != nil {
		return err
	}
	_, err = session.file.Write([]byte(p.Data))
	return err
}

func resizePTY(p ptyResizeParams) error {
	session, err := lookupPTY(p.ID)
	if err != nil {
		return err
	}
	if p.Cols == 0 || p.Rows == 0 {
		return nil
	}
	return pty.Setsize(session.file, &pty.Winsize{Rows: p.Rows, Cols: p.Cols})
}

func killPTY(id string) error {
	ptyMu.Lock()
	session := ptys[id]
	ptyMu.Unlock()
	if session == nil {
		return nil
	}
	finishPTY(session, true)
	return nil
}

func finishPTY(session *ptySession, kill bool) int {
	session.once.Do(func() {
		ptyMu.Lock()
		delete(ptys, session.id)
		ptyMu.Unlock()
		if kill && session.cmd.Process != nil {
			_ = killCommandProcessGroup(session.cmd)
		}
		_ = session.file.Close()
		if session.cmd.Process == nil {
			session.exitCode = -1
			return
		}
		err := session.cmd.Wait()
		if err == nil {
			session.exitCode = 0
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			session.exitCode = exitErr.ExitCode()
			return
		}
		session.exitCode = -1
	})
	return session.exitCode
}
