package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	connectionDisconnected = "disconnected"
	connectionConnecting   = "connecting"
	connectionConnected    = "connected"
	connectionDegraded     = "degraded"
	connectionFailed       = "failed"
)

type WorkspaceConnection struct {
	WorkspaceID  string         `json:"workspace_id"`
	Target       string         `json:"target"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint,omitempty"`
	Port         int            `json:"port,omitempty"`
	RemoteCWD    string         `json:"remote_cwd,omitempty"`
	RemoteOS     string         `json:"remote_os,omitempty"`
	RemoteArch   string         `json:"remote_arch,omitempty"`
	RemoteShell  string         `json:"remote_shell,omitempty"`
	HelperPath   string         `json:"helper_path,omitempty"`
	HelperStatus string         `json:"helper_status,omitempty"`
	Message      string         `json:"message,omitempty"`
	UpdatedAt    string         `json:"updated_at"`
	Raw          map[string]any `json:"raw,omitempty"`
}

type sshManager struct {
	app *app
	mu  sync.Mutex
	by  map[string]*sshTarget
}

type sshTarget struct {
	workspace Workspace
	proxy     *proxyClient
	state     WorkspaceConnection
}

func newSSHManager(a *app) *sshManager {
	return &sshManager{app: a, by: map[string]*sshTarget{}}
}

func (m *sshManager) getConnection(ws Workspace) WorkspaceConnection {
	if ws.Target != "ssh" {
		return WorkspaceConnection{
			WorkspaceID: ws.ID,
			Target:      ws.Target,
			Status:      connectionConnected,
			RemoteCWD:   ws.LocalCWD,
			UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if target := m.by[ws.ID]; target != nil {
		return target.state
	}
	return initialSSHConnection(ws, connectionDisconnected)
}

func (m *sshManager) connect(ctx context.Context, ws Workspace) (WorkspaceConnection, error) {
	if ws.Target != "ssh" {
		return m.getConnection(ws), nil
	}
	if ws.SSH == nil {
		return WorkspaceConnection{}, errors.New("ssh workspace is missing ssh config")
	}
	m.setState(ws, initialSSHConnection(ws, connectionConnecting))

	probe, err := m.probe(ctx, ws)
	if err != nil {
		state := initialSSHConnection(ws, connectionFailed)
		state.Message = err.Error()
		m.setState(ws, state)
		return state, err
	}
	helper, err := m.ensureHelper(ctx, ws, probe)
	if err != nil {
		state := initialSSHConnection(ws, connectionFailed)
		state.RemoteOS = probe.OS
		state.RemoteArch = probe.Arch
		state.RemoteShell = probe.Shell
		state.Message = err.Error()
		m.setState(ws, state)
		return state, err
	}
	proxy, err := startProxyClient(ws, helper)
	if err != nil {
		state := initialSSHConnection(ws, connectionFailed)
		state.RemoteOS = probe.OS
		state.RemoteArch = probe.Arch
		state.RemoteShell = probe.Shell
		state.HelperPath = helper.RemotePath
		state.HelperStatus = "uploaded"
		state.Message = err.Error()
		m.setState(ws, state)
		return state, err
	}
	hello, err := proxy.hello(ctx)
	if err != nil {
		_ = proxy.close()
		state := initialSSHConnection(ws, connectionFailed)
		state.Message = err.Error()
		m.setState(ws, state)
		return state, err
	}
	state := initialSSHConnection(ws, connectionConnected)
	state.RemoteOS = probe.OS
	state.RemoteArch = probe.Arch
	state.RemoteShell = firstString(probe.Shell, stringValue(hello["shell"]))
	state.HelperPath = helper.RemotePath
	state.HelperStatus = "running"
	state.Raw = hello

	m.mu.Lock()
	m.by[ws.ID] = &sshTarget{workspace: ws, proxy: proxy, state: state}
	m.mu.Unlock()
	m.emitConnection(ws, state)
	go m.watchProxy(ws, proxy)
	return state, nil
}

func (m *sshManager) disconnect(ws Workspace) WorkspaceConnection {
	m.mu.Lock()
	target := m.by[ws.ID]
	delete(m.by, ws.ID)
	m.mu.Unlock()
	if target != nil && target.proxy != nil {
		_ = target.proxy.close()
	}
	state := initialSSHConnection(ws, connectionDisconnected)
	m.setState(ws, state)
	return state
}

func (m *sshManager) proxyFor(ctx context.Context, ws Workspace) (*proxyClient, WorkspaceConnection, error) {
	if ws.Target != "ssh" {
		return nil, m.getConnection(ws), errors.New("workspace is not ssh")
	}
	m.mu.Lock()
	target := m.by[ws.ID]
	if target != nil && target.proxy != nil && target.proxy.isAlive() && target.state.Status == connectionConnected {
		proxy := target.proxy
		state := target.state
		m.mu.Unlock()
		return proxy, state, nil
	}
	m.mu.Unlock()
	state, err := m.connect(ctx, ws)
	if err != nil {
		return nil, state, err
	}
	m.mu.Lock()
	target = m.by[ws.ID]
	m.mu.Unlock()
	if target == nil || target.proxy == nil {
		return nil, state, errors.New("ssh proxy did not start")
	}
	return target.proxy, state, nil
}

func (m *sshManager) setState(ws Workspace, state WorkspaceConnection) {
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	m.mu.Lock()
	target := m.by[ws.ID]
	if target == nil {
		target = &sshTarget{workspace: ws}
		m.by[ws.ID] = target
	}
	target.state = state
	m.mu.Unlock()
	m.emitConnection(ws, state)
}

func (m *sshManager) emitConnection(ws Workspace, state WorkspaceConnection) {
	m.app.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.connection", Normalized: state})
}

func (m *sshManager) watchProxy(ws Workspace, proxy *proxyClient) {
	<-proxy.done
	m.mu.Lock()
	target := m.by[ws.ID]
	if target == nil || target.proxy != proxy {
		m.mu.Unlock()
		return
	}
	state := target.state
	state.Status = connectionDegraded
	state.HelperStatus = "exited"
	state.Message = proxy.exitMessage()
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	target.state = state
	m.mu.Unlock()
	m.emitConnection(ws, state)
}

type sshProbe struct {
	OS    string
	Arch  string
	Shell string
	CWD   string
}

func (m *sshManager) probe(ctx context.Context, ws Workspace) (sshProbe, error) {
	remoteCWD := strings.TrimSpace(ws.SSH.RemoteCWD)
	script := "printf '%s\\n' \"$(uname -s)\" \"$(uname -m)\" \"${SHELL:-/bin/sh}\" \"$(pwd)\"; test -d " + shellQuote(remoteCWD) + "; test -r " + shellQuote(remoteCWD) + "; test -x " + shellQuote(remoteCWD)
	cmd := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, script)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return sshProbe{}, fmt.Errorf("ssh probe failed: %s%s", err.Error(), stderrSuffix(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	probe := sshProbe{}
	if len(lines) > 0 {
		probe.OS = normalizeRemoteOS(lines[0])
	}
	if len(lines) > 1 {
		probe.Arch = normalizeRemoteArch(lines[1])
	}
	if len(lines) > 2 {
		probe.Shell = strings.TrimSpace(lines[2])
	}
	if len(lines) > 3 {
		probe.CWD = strings.TrimSpace(lines[3])
	}
	if probe.OS == "" || probe.Arch == "" {
		return probe, fmt.Errorf("unsupported remote platform %q/%q", probe.OS, probe.Arch)
	}
	return probe, nil
}

type helperUpload struct {
	LocalPath  string
	RemoteDir  string
	RemotePath string
}

func (m *sshManager) ensureHelper(ctx context.Context, ws Workspace, probe sshProbe) (helperUpload, error) {
	local, err := m.localHelperBinary(ctx, probe)
	if err != nil {
		return helperUpload{}, err
	}
	remoteDir := fmt.Sprintf("/tmp/.astralops/%s", ws.ID)
	remotePath := remoteDir + "/astral-proxy-agent"
	mkdir := "mkdir -p " + shellQuote(remoteDir) + " && chmod 700 " + shellQuote(remoteDir)
	if out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, mkdir)...).CombinedOutput(); err != nil {
		return helperUpload{}, fmt.Errorf("create remote helper dir failed: %s%s", err.Error(), stderrSuffix(string(out)))
	}
	scpArgs := []string{"-P", strconv.Itoa(sshPort(ws))}
	scpArgs = append(scpArgs, local, ws.SSH.Endpoint+":"+remotePath)
	if out, err := exec.CommandContext(ctx, "scp", scpArgs...).CombinedOutput(); err != nil {
		return helperUpload{}, fmt.Errorf("helper upload failed: %s%s", err.Error(), stderrSuffix(string(out)))
	}
	chmod := "chmod 700 " + shellQuote(remotePath)
	if out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, chmod)...).CombinedOutput(); err != nil {
		return helperUpload{}, fmt.Errorf("chmod remote helper failed: %s%s", err.Error(), stderrSuffix(string(out)))
	}
	return helperUpload{LocalPath: local, RemoteDir: remoteDir, RemotePath: remotePath}, nil
}

func (m *sshManager) localHelperBinary(ctx context.Context, probe sshProbe) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("ASTRALOPS_PROXY_AGENT")); configured != "" {
		return configured, nil
	}
	if bundled := findBundledProxyAgent(probe); bundled != "" {
		return bundled, nil
	}
	out := filepath.Join(m.app.store.dataDir, "helpers", probe.OS+"-"+probe.Arch, "astral-proxy-agent")
	if st, err := os.Stat(out); err == nil && st.Mode()&0o111 != 0 {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	env := os.Environ()
	env = append(env, "GOOS="+probe.OS, "GOARCH="+probe.Arch, "CGO_ENABLED=0")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./proxy-agent")
	cmd.Env = env
	cmd.Dir = repoRootGuess()
	if body, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build proxy helper for %s/%s failed: %s%s", probe.OS, probe.Arch, err.Error(), stderrSuffix(string(body)))
	}
	return out, nil
}

func findBundledProxyAgent(probe sshProbe) string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "astral-proxy-agent"),
			filepath.Join(dir, "proxy-agent"),
			filepath.Join(dir, "helpers", probe.OS+"-"+probe.Arch, "astral-proxy-agent"),
		)
	}
	root := repoRootGuess()
	candidates = append(candidates,
		filepath.Join(root, "bin", "proxy-agent", probe.OS+"-"+probe.Arch, "astral-proxy-agent"),
		filepath.Join(root, "astral-proxy-agent"),
	)
	for _, candidate := range candidates {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func initialSSHConnection(ws Workspace, status string) WorkspaceConnection {
	port := 22
	endpoint := ""
	remoteCWD := ""
	if ws.SSH != nil {
		endpoint = ws.SSH.Endpoint
		port = sshPort(ws)
		remoteCWD = ws.SSH.RemoteCWD
	}
	return WorkspaceConnection{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Status:      status,
		Endpoint:    endpoint,
		Port:        port,
		RemoteCWD:   remoteCWD,
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func startProxyClient(ws Workspace, helper helperUpload) (*proxyClient, error) {
	command := "exec " + shellQuote(helper.RemotePath) + " --cwd " + shellQuote(ws.SSH.RemoteCWD)
	cmd := exec.Command("ssh", append(sshArgs(ws), ws.SSH.Endpoint, command)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	client := newProxyClient(ws, cmd, stdin, stdout, stderr)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client.start()
	return client, nil
}

type proxyClient struct {
	workspace Workspace
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.Reader
	stderr    io.Reader

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan proxyResponse
	events  map[string][]chan proxyEvent
	alive   bool
	errText string
	done    chan struct{}
}

type proxyResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Event  string          `json:"event,omitempty"`
}

type proxyEvent struct {
	ID     string         `json:"id"`
	Event  string         `json:"event"`
	Result map[string]any `json:"result,omitempty"`
}

func newProxyClient(ws Workspace, cmd *exec.Cmd, stdin io.WriteCloser, stdout io.Reader, stderr io.Reader) *proxyClient {
	return &proxyClient{
		workspace: ws,
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderr:    stderr,
		pending:   map[string]chan proxyResponse{},
		events:    map[string][]chan proxyEvent{},
		alive:     true,
		done:      make(chan struct{}),
	}
}

func (p *proxyClient) start() {
	go p.scanStdout()
	go p.scanStderr()
	go p.wait()
}

func (p *proxyClient) isAlive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alive
}

func (p *proxyClient) close() error {
	p.mu.Lock()
	alive := p.alive
	p.mu.Unlock()
	if !alive {
		return nil
	}
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return nil
}

func (p *proxyClient) exitMessage() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errText
}

func (p *proxyClient) scanStdout() {
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var res proxyResponse
		if err := json.Unmarshal(scanner.Bytes(), &res); err != nil {
			p.setErr("invalid proxy json: " + err.Error())
			continue
		}
		if res.Event != "" {
			p.deliverEvent(res)
			continue
		}
		p.deliverResponse(res)
	}
	if err := scanner.Err(); err != nil {
		p.setErr(err.Error())
	}
}

func (p *proxyClient) scanStderr() {
	scanner := bufio.NewScanner(p.stderr)
	scanner.Buffer(make([]byte, 0, 16*1024), 8*1024*1024)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			p.setErr(text)
		}
	}
}

func (p *proxyClient) wait() {
	err := p.cmd.Wait()
	if err != nil {
		p.setErr(err.Error())
	}
	p.mu.Lock()
	p.alive = false
	for id, ch := range p.pending {
		ch <- proxyResponse{ID: id, Error: "ssh proxy exited"}
		delete(p.pending, id)
	}
	p.mu.Unlock()
	close(p.done)
}

func (p *proxyClient) setErr(text string) {
	p.mu.Lock()
	if p.errText == "" {
		p.errText = text
	} else if !strings.Contains(p.errText, text) {
		p.errText += "\n" + text
	}
	p.mu.Unlock()
}

func (p *proxyClient) deliverResponse(res proxyResponse) {
	p.mu.Lock()
	ch := p.pending[res.ID]
	delete(p.pending, res.ID)
	p.mu.Unlock()
	if ch != nil {
		ch <- res
	}
}

func (p *proxyClient) deliverEvent(res proxyResponse) {
	event := proxyEvent{ID: res.ID, Event: res.Event}
	if len(res.Result) > 0 {
		_ = json.Unmarshal(res.Result, &event.Result)
	}
	p.mu.Lock()
	listeners := append([]chan proxyEvent(nil), p.events[res.ID]...)
	p.mu.Unlock()
	for _, ch := range listeners {
		select {
		case ch <- event:
		default:
		}
	}
}

func (p *proxyClient) subscribe(id string) (<-chan proxyEvent, func()) {
	ch := make(chan proxyEvent, 128)
	p.mu.Lock()
	p.events[id] = append(p.events[id], ch)
	p.mu.Unlock()
	cancel := func() {
		p.mu.Lock()
		list := p.events[id]
		filtered := list[:0]
		for _, existing := range list {
			if existing != ch {
				filtered = append(filtered, existing)
			}
		}
		if len(filtered) == 0 {
			delete(p.events, id)
		} else {
			p.events[id] = filtered
		}
		p.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

func (p *proxyClient) call(ctx context.Context, method string, params any, out any) error {
	id := strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatInt(p.next(), 36)
	req := map[string]any{"id": id, "method": method, "params": params}
	body, _ := json.Marshal(req)
	ch := make(chan proxyResponse, 1)
	p.mu.Lock()
	if !p.alive {
		p.mu.Unlock()
		return errors.New("ssh proxy is not running")
	}
	p.pending[id] = ch
	_, err := p.stdin.Write(append(body, '\n'))
	p.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case res := <-ch:
		if res.Error != "" {
			return errors.New(res.Error)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(res.Result, out)
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return ctx.Err()
	}
}

func (p *proxyClient) next() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	return p.nextID
}

func (p *proxyClient) hello(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	err := p.call(ctx, "hello", map[string]any{}, &out)
	return out, err
}

func sshPort(ws Workspace) int {
	if ws.SSH != nil && ws.SSH.Port > 0 {
		return ws.SSH.Port
	}
	return 22
}

func sshArgs(ws Workspace) []string {
	return []string{"-p", strconv.Itoa(sshPort(ws)), "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func stderrSuffix(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return ": " + text
}

func normalizeRemoteOS(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "linux":
		return "linux"
	case "darwin":
		return "darwin"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeRemoteArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func repoRootGuess() string {
	wd, err := os.Getwd()
	if err == nil {
		for dir := wd; dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
		}
		return wd
	}
	return "."
}

func localTCPHostPort(addr string) string {
	if strings.HasPrefix(addr, "127.0.0.1:") {
		return addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil && (host == "" || host == "::" || host == "[::]") {
		return "127.0.0.1:" + port
	}
	return addr
}
