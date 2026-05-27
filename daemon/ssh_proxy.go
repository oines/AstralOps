package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	connectionDisconnected = "disconnected"
	connectionConnecting   = "connecting"
	connectionConnected    = "connected"
	connectionReconnecting = "reconnecting"
	connectionDegraded     = "degraded"
	connectionFailed       = "failed"
	sshProxyMaxAttempts    = 5
)

type WorkspaceConnection struct {
	WorkspaceID  string         `json:"workspace_id"`
	Target       string         `json:"target"`
	Status       string         `json:"status"`
	Endpoint     string         `json:"endpoint,omitempty"`
	Port         int            `json:"port,omitempty"`
	RemoteCWD    string         `json:"remote_cwd,omitempty"`
	RemoteUser   string         `json:"remote_user,omitempty"`
	RemoteHost   string         `json:"remote_host,omitempty"`
	RemoteOS     string         `json:"remote_os,omitempty"`
	RemoteArch   string         `json:"remote_arch,omitempty"`
	RemoteShell  string         `json:"remote_shell,omitempty"`
	DisplayCWD   string         `json:"display_cwd,omitempty"`
	HelperPath   string         `json:"helper_path,omitempty"`
	HelperStatus string         `json:"helper_status,omitempty"`
	Capabilities map[string]any `json:"capabilities,omitempty"`
	Message      string         `json:"message,omitempty"`
	RetryAttempt int            `json:"retry_attempt,omitempty"`
	RetryMax     int            `json:"retry_max,omitempty"`
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

func (m *sshManager) restorePersistedConnections(ctx context.Context) {
	for _, ws := range m.app.store.listWorkspaces() {
		if ws.Target != "ssh" {
			continue
		}
		last, ok := m.app.store.latestWorkspaceConnection(ws.ID)
		if !ok {
			continue
		}
		state := mergeSSHConnectionDefaults(ws, last)
		if shouldRestoreSSHConnection(state.Status) {
			state.Status = connectionReconnecting
			state.Message = "restoring previous ssh connection"
			state.RetryAttempt = 0
			state.RetryMax = sshProxyMaxAttempts
			m.setState(ws, state)
			go func(workspace Workspace) {
				connectCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
				defer cancel()
				_, _ = m.connect(connectCtx, workspace)
			}(ws)
			continue
		}
		m.seedState(ws, state)
	}
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

func (m *sshManager) remoteWorkspaceRuntimeDir(ws Workspace) string {
	if ws.Target != "ssh" {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target := m.by[ws.ID]
	if target == nil {
		return ""
	}
	if value := stringValue(mapValue(target.state.Raw)["runtime_dir"]); value != "" {
		return remotePathClean(value)
	}
	if helperPath := strings.TrimSpace(target.state.HelperPath); helperPath != "" {
		return remotePathDir(helperPath)
	}
	return ""
}

func (m *sshManager) connect(ctx context.Context, ws Workspace) (WorkspaceConnection, error) {
	return m.connectInternal(ctx, ws, true)
}

func (m *sshManager) connectInternal(ctx context.Context, ws Workspace, emitProgress bool) (WorkspaceConnection, error) {
	if ws.Target != "ssh" {
		return m.getConnection(ws), nil
	}
	if ws.SSH == nil {
		return WorkspaceConnection{}, errors.New("ssh workspace is missing ssh config")
	}
	if emitProgress {
		m.setState(ws, initialSSHConnection(ws, connectionConnecting))
	}

	probe, err := m.probe(ctx, ws)
	if err != nil {
		state := initialSSHConnection(ws, connectionFailed)
		state.Message = err.Error()
		if emitProgress {
			m.setState(ws, state)
		}
		return state, err
	}
	var helper helperUpload
	var proxy *proxyClient
	var hello map[string]any
	localHelper, err := m.localHelperBinary(ctx, probe)
	if err != nil {
		state := connectionFailedState(ws, probe, err)
		if emitProgress {
			m.setState(ws, state)
		}
		return state, err
	}
	localSum, err := fileSHA256(localHelper)
	if err != nil {
		state := connectionFailedState(ws, probe, err)
		if emitProgress {
			m.setState(ws, state)
		}
		return state, err
	}
	candidates := remoteHelperCandidates(ws, probe)
	attempts := []remoteHelperAttempt{}
	for _, candidate := range candidates {
		helper, proxy, hello, err = m.tryRemoteHelperCandidate(ctx, ws, probe, candidate, localHelper, localSum, emitProgress)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			err = ctx.Err()
			break
		}
		attempts = append(attempts, remoteHelperAttempt{Candidate: candidate, Err: err})
		var noFallback remoteHelperNoFallbackError
		if errors.As(err, &noFallback) {
			break
		}
	}
	if err != nil {
		if ctx.Err() == nil {
			err = remoteHelperAttemptsError(attempts)
		}
		state := connectionFailedState(ws, probe, err)
		if len(attempts) > 0 {
			state.HelperPath = remotePathJoin(attempts[len(attempts)-1].Candidate.RemoteDir, "astral-proxy-agent")
			state.HelperStatus = "failed"
		}
		if emitProgress {
			m.setState(ws, state)
		}
		return state, err
	}
	state := initialSSHConnection(ws, connectionConnected)
	state.RemoteOS = probe.OS
	state.RemoteArch = probe.Arch
	state.RemoteShell = firstString(probe.Shell, stringValue(hello["shell"]))
	state.RemoteUser = firstString(probe.User, stringValue(hello["user"]))
	state.RemoteHost = firstString(probe.Host, stringValue(hello["hostname"]))
	state.DisplayCWD = remoteDisplayCWD(state.RemoteUser, state.RemoteHost, state.RemoteCWD, state.Endpoint)
	state.HelperPath = helper.RemotePath
	state.HelperStatus = "running"
	state.Capabilities = probeCapabilities(probe)
	if caps := mapValue(hello["capabilities"]); len(caps) > 0 {
		state.Capabilities = caps
	}
	hello["helper_uploaded"] = helper.Uploaded
	hello["runtime_dir"] = helper.RemoteDir
	state.Raw = hello

	m.mu.Lock()
	m.by[ws.ID] = &sshTarget{workspace: ws, proxy: proxy, state: state}
	m.mu.Unlock()
	m.emitConnection(ws, state)
	go m.watchProxy(ws, proxy)
	return state, nil
}

func connectionFailedState(ws Workspace, probe sshProbe, err error) WorkspaceConnection {
	state := initialSSHConnection(ws, connectionFailed)
	state.RemoteOS = probe.OS
	state.RemoteArch = probe.Arch
	state.RemoteShell = probe.Shell
	state.RemoteUser = probe.User
	state.RemoteHost = probe.Host
	state.Capabilities = probeCapabilities(probe)
	if err != nil {
		state.Message = err.Error()
	}
	return state
}

func validateProxyHello(hello map[string]any) error {
	capabilities := mapValue(hello["capabilities"])
	methods := map[string]bool{}
	switch values := capabilities["methods"].(type) {
	case []any:
		for _, value := range values {
			if method := stringValue(value); method != "" {
				methods[method] = true
			}
		}
	case []string:
		for _, method := range values {
			if method != "" {
				methods[method] = true
			}
		}
	}
	required := []string{"hello", "read", "write", "list", "stat", "exec_start", "exec_kill", "pty_start", "pty_kill"}
	missing := []string{}
	for _, method := range required {
		if !methods[method] {
			missing = append(missing, method)
		}
	}
	if len(missing) > 0 {
		version := stringValue(hello["version"])
		if version == "" {
			version = "unknown"
		}
		return fmt.Errorf("ssh proxy helper is incompatible: version %s missing methods %s", version, strings.Join(missing, ", "))
	}
	return nil
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
	return m.proxyForWithProgress(ctx, ws, true)
}

func (m *sshManager) proxyForWithProgress(ctx context.Context, ws Workspace, emitConnectProgress bool) (*proxyClient, WorkspaceConnection, error) {
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
	state, err := m.connectInternal(ctx, ws, emitConnectProgress)
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

func (m *sshManager) call(ctx context.Context, ws Workspace, method string, params any, out any) error {
	var lastErr error
	for attempt := 1; attempt <= sshProxyMaxAttempts; attempt++ {
		proxy, _, err := m.proxyForWithProgress(ctx, ws, attempt == 1)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
		} else {
			err = proxy.call(ctx, method, params, out)
			if err == nil {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			if !isProxyTransportError(err) {
				return err
			}
			m.dropProxy(ws, proxy)
		}
		m.setReconnecting(ws, attempt, sshProxyMaxAttempts, lastErr)
		if attempt < sshProxyMaxAttempts {
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("ssh operation failed")
	}
	message := fmt.Sprintf("ssh operation failed after %d attempts: %s", sshProxyMaxAttempts, lastErr.Error())
	m.markDegraded(ws, message)
	if m.app != nil {
		m.app.stopWorkspaceSessions(ws.ID, message)
	}
	return errors.New(message)
}

func (m *sshManager) startPTY(ctx context.Context, ws Workspace, id string, params map[string]any) (*proxyClient, <-chan proxyEvent, func(), map[string]any, error) {
	return m.startEventProcess(ctx, ws, id, "pty_start", params)
}

func (m *sshManager) startExec(ctx context.Context, ws Workspace, id string, params map[string]any) (*proxyClient, <-chan proxyEvent, func(), map[string]any, error) {
	return m.startEventProcess(ctx, ws, id, "exec_start", params)
}

func (m *sshManager) startEventProcess(ctx context.Context, ws Workspace, id, method string, params map[string]any) (*proxyClient, <-chan proxyEvent, func(), map[string]any, error) {
	var lastErr error
	for attempt := 1; attempt <= sshProxyMaxAttempts; attempt++ {
		proxy, _, err := m.proxyForWithProgress(ctx, ws, attempt == 1)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, nil, ctx.Err()
			}
			lastErr = err
		} else {
			events, unsubscribe := proxy.subscribe(id)
			callParams := map[string]any{}
			for key, value := range params {
				callParams[key] = value
			}
			callParams["id"] = id
			var started map[string]any
			err = proxy.call(ctx, method, callParams, &started)
			if err == nil {
				return proxy, events, unsubscribe, started, nil
			}
			unsubscribe()
			if ctx.Err() != nil {
				return nil, nil, nil, nil, ctx.Err()
			}
			lastErr = err
			if !isProxyTransportError(err) {
				return nil, nil, nil, nil, err
			}
			m.dropProxy(ws, proxy)
		}
		m.setReconnecting(ws, attempt, sshProxyMaxAttempts, lastErr)
		if attempt < sshProxyMaxAttempts {
			time.Sleep(time.Duration(attempt) * 200 * time.Millisecond)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("ssh operation failed")
	}
	message := fmt.Sprintf("ssh operation failed after %d attempts: %s", sshProxyMaxAttempts, lastErr.Error())
	m.markDegraded(ws, message)
	if m.app != nil {
		m.app.stopWorkspaceSessions(ws.ID, message)
	}
	return nil, nil, nil, nil, errors.New(message)
}

func (m *sshManager) dropProxy(ws Workspace, proxy *proxyClient) {
	m.mu.Lock()
	target := m.by[ws.ID]
	if target != nil && target.proxy == proxy {
		target.proxy = nil
		target.state.Status = connectionDegraded
		target.state.HelperStatus = "exited"
		target.state.Message = proxy.exitMessage()
		target.state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	m.mu.Unlock()
	_ = proxy.close()
}

func (m *sshManager) markDegraded(ws Workspace, message string) {
	m.mu.Lock()
	target := m.by[ws.ID]
	if target == nil {
		target = &sshTarget{workspace: ws, state: initialSSHConnection(ws, connectionDegraded)}
		m.by[ws.ID] = target
	}
	state := target.state
	if state.WorkspaceID == "" {
		state = initialSSHConnection(ws, connectionDegraded)
	}
	state.Status = connectionDegraded
	state.HelperStatus = "exited"
	state.Message = message
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	target.state = state
	m.mu.Unlock()
	m.emitConnection(ws, state)
}

func (m *sshManager) setReconnecting(ws Workspace, attempt int, max int, err error) {
	state := m.connectionStateForUpdate(ws)
	state.Status = connectionReconnecting
	state.HelperStatus = "reconnecting"
	state.RetryAttempt = attempt
	state.RetryMax = max
	if err != nil {
		state.Message = err.Error()
	}
	m.setState(ws, state)
}

func (m *sshManager) connectionStateForUpdate(ws Workspace) WorkspaceConnection {
	m.mu.Lock()
	defer m.mu.Unlock()
	if target := m.by[ws.ID]; target != nil && target.state.WorkspaceID != "" {
		return target.state
	}
	return initialSSHConnection(ws, connectionDisconnected)
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

func (m *sshManager) seedState(ws Workspace, state WorkspaceConnection) {
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	m.mu.Lock()
	target := m.by[ws.ID]
	if target == nil {
		target = &sshTarget{workspace: ws}
		m.by[ws.ID] = target
	}
	target.state = state
	m.mu.Unlock()
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
	OS            string
	Arch          string
	Shell         string
	CWD           string
	User          string
	Host          string
	UID           string
	XDGRuntimeDir string
	TMPDir        string
	Home          string
	RGPath        string
	RGVersion     string
}

func (m *sshManager) probe(ctx context.Context, ws Workspace) (sshProbe, error) {
	remoteCWD := strings.TrimSpace(ws.SSH.RemoteCWD)
	script := remoteProbeScript(remoteCWD)
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
	if len(lines) > 4 {
		probe.User = strings.TrimSpace(lines[4])
	}
	if len(lines) > 5 {
		probe.Host = strings.TrimSpace(lines[5])
	}
	if len(lines) > 6 {
		probe.UID = strings.TrimSpace(lines[6])
	}
	if len(lines) > 7 {
		probe.XDGRuntimeDir = strings.TrimSpace(lines[7])
	}
	if len(lines) > 8 {
		probe.TMPDir = strings.TrimSpace(lines[8])
	}
	if len(lines) > 9 {
		probe.Home = strings.TrimSpace(lines[9])
	}
	if len(lines) > 10 {
		probe.RGPath = strings.TrimSpace(lines[10])
	}
	if len(lines) > 11 {
		probe.RGVersion = strings.TrimSpace(lines[11])
	}
	if probe.OS == "" || probe.Arch == "" {
		return probe, fmt.Errorf("unsupported remote platform %q/%q", probe.OS, probe.Arch)
	}
	return probe, nil
}

func probeCapabilities(probe sshProbe) map[string]any {
	return map[string]any{
		"rg": map[string]any{
			"available": probe.RGPath != "",
			"path":      probe.RGPath,
			"version":   probe.RGVersion,
		},
	}
}

type helperUpload struct {
	LocalPath  string
	RemoteDir  string
	RemotePath string
	Uploaded   bool
}

type remoteHelperCandidate struct {
	Label     string
	BaseDir   string
	RemoteDir string
}

type remoteHelperAttempt struct {
	Candidate remoteHelperCandidate
	Err       error
}

func (m *sshManager) tryRemoteHelperCandidate(ctx context.Context, ws Workspace, probe sshProbe, candidate remoteHelperCandidate, localHelper, localSum string, emitProgress bool) (helperUpload, *proxyClient, map[string]any, error) {
	helper, proxy, hello, err := m.tryRemoteHelperCandidateOnce(ctx, ws, candidate, localHelper, localSum, false)
	if err == nil {
		return helper, proxy, hello, nil
	}
	var validationErr remoteHelperValidationError
	if helper.RemotePath == "" || helper.Uploaded || !errors.As(err, &validationErr) {
		return helper, proxy, hello, err
	}
	if emitProgress {
		state := initialSSHConnection(ws, connectionReconnecting)
		state.RemoteOS = probe.OS
		state.RemoteArch = probe.Arch
		state.RemoteShell = probe.Shell
		state.RemoteUser = probe.User
		state.RemoteHost = probe.Host
		state.Capabilities = probeCapabilities(probe)
		state.HelperPath = helper.RemotePath
		state.HelperStatus = "upgrading"
		state.Message = err.Error()
		state.RetryAttempt = 1
		state.RetryMax = 2
		m.setState(ws, state)
	}
	helper, proxy, hello, err = m.tryRemoteHelperCandidateOnce(ctx, ws, candidate, localHelper, localSum, true)
	if err != nil {
		var validationErr remoteHelperValidationError
		if errors.As(err, &validationErr) {
			err = remoteHelperNoFallbackError{err: err}
		}
	}
	return helper, proxy, hello, err
}

func (m *sshManager) tryRemoteHelperCandidateOnce(ctx context.Context, ws Workspace, candidate remoteHelperCandidate, localHelper, localSum string, forceUpload bool) (helperUpload, *proxyClient, map[string]any, error) {
	helper, err := m.ensureHelperAt(ctx, ws, candidate, localHelper, localSum, forceUpload)
	if err != nil {
		return helper, nil, nil, err
	}
	proxy, err := startProxyClient(ws, helper)
	if err != nil {
		return helper, nil, nil, remoteHelperNoFallbackError{err: err}
	}
	hello, err := proxy.hello(ctx)
	if err != nil {
		_ = proxy.close()
		return helper, nil, nil, remoteHelperNoFallbackError{err: err}
	}
	if err = validateProxyHello(hello); err != nil {
		_ = proxy.close()
		return helper, nil, nil, remoteHelperValidationError{err: err}
	}
	return helper, proxy, hello, nil
}

type remoteHelperValidationError struct {
	err error
}

func (e remoteHelperValidationError) Error() string {
	if e.err == nil {
		return "remote helper validation failed"
	}
	return e.err.Error()
}

func (e remoteHelperValidationError) Unwrap() error {
	return e.err
}

type remoteHelperNoFallbackError struct {
	err error
}

func (e remoteHelperNoFallbackError) Error() string {
	if e.err == nil {
		return "remote helper failed"
	}
	return e.err.Error()
}

func (e remoteHelperNoFallbackError) Unwrap() error {
	return e.err
}

func (m *sshManager) ensureHelperAt(ctx context.Context, ws Workspace, candidate remoteHelperCandidate, local, localSum string, forceUpload bool) (helperUpload, error) {
	remoteDir := candidate.RemoteDir
	remotePath := remoteDir + "/astral-proxy-agent"
	helper := helperUpload{LocalPath: local, RemoteDir: remoteDir, RemotePath: remotePath}
	mkdir := "mkdir -p " + shellQuote(candidate.BaseDir) + " " + shellQuote(remoteDir) + " && chmod 700 " + shellQuote(candidate.BaseDir) + " " + shellQuote(remoteDir)
	if out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, mkdir)...).CombinedOutput(); err != nil {
		return helper, fmt.Errorf("create remote helper dir failed: %s%s", err.Error(), stderrSuffix(string(out)))
	}
	uploaded := false
	remoteSum := m.remoteHelperSHA256(ctx, ws, remotePath)
	if forceUpload || remoteSum != localSum {
		remoteTemp := remotePath + ".upload-" + randomID(8)
		if err := uploadRemoteFile(ctx, ws, local, remoteTemp); err != nil {
			return helper, fmt.Errorf("helper upload failed: %w", err)
		}
		uploaded = true
		install := "chmod 700 " + shellQuote(remoteTemp) + " && mv -f " + shellQuote(remoteTemp) + " " + shellQuote(remotePath)
		if out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, install)...).CombinedOutput(); err != nil {
			cleanup := "rm -f " + shellQuote(remoteTemp)
			_, _ = exec.CommandContext(context.Background(), "ssh", append(sshArgs(ws), ws.SSH.Endpoint, cleanup)...).CombinedOutput()
			return helper, fmt.Errorf("install remote helper failed: %s%s", err.Error(), stderrSuffix(string(out)))
		}
	}
	helper.Uploaded = uploaded
	verify := "exec " + shellQuote(remotePath) + " --self-test"
	if out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, verify)...).CombinedOutput(); err != nil {
		return helper, fmt.Errorf("verify remote helper failed: %s%s", err.Error(), stderrSuffix(string(out)))
	}
	return helper, nil
}

func remoteHelperCandidates(ws Workspace, probe sshProbe) []remoteHelperCandidate {
	type candidateInput struct {
		label string
		base  string
	}
	tmpDir := firstString(probe.TMPDir, "/tmp")
	tmpName := ".astralops"
	if probe.UID != "" {
		tmpName += "-" + probe.UID
	}
	inputs := []candidateInput{}
	if probe.XDGRuntimeDir != "" {
		inputs = append(inputs, candidateInput{"xdg-runtime", remotePathJoin(probe.XDGRuntimeDir, "astralops")})
	}
	inputs = append(inputs, candidateInput{"tmp", remotePathJoin(tmpDir, tmpName)})
	if probe.Home != "" {
		inputs = append(inputs, candidateInput{"home-cache", remotePathJoin(probe.Home, ".cache/astralops")})
	}
	if ws.SSH != nil && strings.TrimSpace(ws.SSH.RemoteCWD) != "" {
		inputs = append(inputs, candidateInput{"workspace", remotePathJoin(ws.SSH.RemoteCWD, ".astralops")})
	}
	seen := map[string]bool{}
	out := []remoteHelperCandidate{}
	for _, input := range inputs {
		base := remotePathClean(input.base)
		if base == "" || !remotePathIsAbs(base) || seen[base] {
			continue
		}
		seen[base] = true
		out = append(out, remoteHelperCandidate{
			Label:     input.label,
			BaseDir:   base,
			RemoteDir: remotePathJoin(base, ws.ID),
		})
	}
	if len(out) == 0 {
		base := "/tmp/.astralops"
		out = append(out, remoteHelperCandidate{Label: "tmp", BaseDir: base, RemoteDir: remotePathJoin(base, ws.ID)})
	}
	return out
}

func remoteHelperAttemptsError(attempts []remoteHelperAttempt) error {
	if len(attempts) == 0 {
		return errors.New("remote helper failed")
	}
	var b strings.Builder
	b.WriteString("remote helper failed")
	for _, attempt := range attempts {
		if attempt.Err == nil {
			continue
		}
		b.WriteString("\n")
		b.WriteString(attempt.Candidate.Label)
		b.WriteString(" ")
		b.WriteString(attempt.Candidate.RemoteDir)
		b.WriteString(": ")
		b.WriteString(attempt.Err.Error())
	}
	return errors.New(b.String())
}

func (m *sshManager) remoteHelperSHA256(ctx context.Context, ws Workspace, remotePath string) string {
	script := "if command -v sha256sum >/dev/null 2>&1; then sha256sum " + shellQuote(remotePath) + " 2>/dev/null | awk '{print $1}'; elif command -v shasum >/dev/null 2>&1; then shasum -a 256 " + shellQuote(remotePath) + " 2>/dev/null | awk '{print $1}'; fi"
	out, err := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, script)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func uploadRemoteFile(ctx context.Context, ws Workspace, localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	cmd := exec.CommandContext(ctx, "ssh", append(sshArgs(ws), ws.SSH.Endpoint, "cat > "+shellQuote(remotePath))...)
	cmd.Stdin = file
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s%s", err.Error(), stderrSuffix(stderr.String()))
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (m *sshManager) localHelperBinary(ctx context.Context, probe sshProbe) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("ASTRALOPS_PROXY_AGENT")); configured != "" {
		return configured, nil
	}
	if bundled := findBundledProxyAgent(probe); bundled != "" {
		return bundled, nil
	}
	out := filepath.Join(m.app.store.dataDir, "helpers", probe.OS+"-"+probe.Arch, "astral-proxy-agent")
	root := repoRootGuess()
	if _, err := os.Stat(filepath.Join(root, "proxy-agent", "main.go")); err == nil {
		return m.buildLocalHelperBinary(ctx, probe, out, root)
	}
	if st, err := os.Stat(out); err == nil && helperBinaryUsable(st) {
		return out, nil
	}
	return m.buildLocalHelperBinary(ctx, probe, out, root)
}

func (m *sshManager) buildLocalHelperBinary(ctx context.Context, probe sshProbe, out string, root string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return "", err
	}
	env := os.Environ()
	env = append(env, "GOOS="+probe.OS, "GOARCH="+probe.Arch, "CGO_ENABLED=0")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./proxy-agent")
	cmd.Env = env
	cmd.Dir = root
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
		if st, err := os.Stat(candidate); err == nil && helperBinaryUsable(st) {
			return candidate
		}
	}
	return ""
}

func helperBinaryUsable(st os.FileInfo) bool {
	if st == nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
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
	user, host := userHostFromEndpoint(endpoint)
	return WorkspaceConnection{
		WorkspaceID: ws.ID,
		Target:      ws.Target,
		Status:      status,
		Endpoint:    endpoint,
		Port:        port,
		RemoteCWD:   remoteCWD,
		RemoteUser:  user,
		RemoteHost:  host,
		DisplayCWD:  remoteDisplayCWD(user, host, remoteCWD, endpoint),
		UpdatedAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func mergeSSHConnectionDefaults(ws Workspace, state WorkspaceConnection) WorkspaceConnection {
	base := initialSSHConnection(ws, state.Status)
	if state.WorkspaceID == "" {
		state.WorkspaceID = base.WorkspaceID
	}
	if state.Target == "" {
		state.Target = base.Target
	}
	if state.Endpoint == "" {
		state.Endpoint = base.Endpoint
	}
	if state.Port == 0 {
		state.Port = base.Port
	}
	if state.RemoteCWD == "" {
		state.RemoteCWD = base.RemoteCWD
	}
	if state.RemoteUser == "" {
		state.RemoteUser = base.RemoteUser
	}
	if state.RemoteHost == "" {
		state.RemoteHost = base.RemoteHost
	}
	if state.DisplayCWD == "" {
		state.DisplayCWD = remoteDisplayCWD(state.RemoteUser, state.RemoteHost, state.RemoteCWD, state.Endpoint)
	}
	if state.UpdatedAt == "" {
		state.UpdatedAt = base.UpdatedAt
	}
	return state
}

func shouldRestoreSSHConnection(status string) bool {
	switch status {
	case connectionConnected, connectionConnecting, connectionReconnecting, connectionDegraded:
		return true
	default:
		return false
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
	ID        string          `json:"id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Event     string          `json:"event,omitempty"`
	Transport bool            `json:"-"`
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
	exitText := p.errText
	eventListeners := map[string][]chan proxyEvent{}
	for id, listeners := range p.events {
		eventListeners[id] = append([]chan proxyEvent(nil), listeners...)
	}
	for id, ch := range p.pending {
		message := "ssh proxy exited"
		if exitText != "" {
			message += ": " + exitText
		}
		ch <- proxyResponse{ID: id, Error: message, Transport: true}
		delete(p.pending, id)
	}
	p.mu.Unlock()
	for id, listeners := range eventListeners {
		for _, ch := range listeners {
			select {
			case ch <- proxyEvent{ID: id, Event: "exit", Result: map[string]any{"error": p.exitMessage()}}:
			default:
			}
		}
	}
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
		return proxyTransportError{err: errors.New("ssh proxy is not running")}
	}
	p.pending[id] = ch
	_, err := p.stdin.Write(append(body, '\n'))
	p.mu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return proxyTransportError{err: err}
	}
	select {
	case res := <-ch:
		if res.Error != "" {
			if res.Transport {
				return proxyTransportError{err: errors.New(res.Error)}
			}
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
		return proxyTransportError{err: ctx.Err()}
	}
}

type proxyTransportError struct {
	err error
}

func (e proxyTransportError) Error() string {
	if e.err == nil {
		return "ssh proxy transport failed"
	}
	return e.err.Error()
}

func (e proxyTransportError) Unwrap() error {
	return e.err
}

func isProxyTransportError(err error) bool {
	var transport proxyTransportError
	return errors.As(err, &transport)
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
	args := []string{}
	if port := sshPort(ws); port != 22 {
		args = append(args, "-p", strconv.Itoa(port))
	}
	return append(args, "-o", "BatchMode=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3")
}

func remoteProbeScript(remoteCWD string) string {
	return strings.Join([]string{
		"cd " + shellQuote(remoteCWD),
		remoteIdentityProbeScript(),
		"if command -v rg >/dev/null 2>&1; then command -v rg; rg --version 2>/dev/null | { IFS= read -r line; printf '%s\\n' \"$line\"; }; else printf '%s\\n' '' ''; fi",
	}, " && ")
}

func remoteIdentityProbeScript() string {
	return strings.Join([]string{
		"remote_os=$(uname -s)",
		"remote_arch=$(uname -m)",
		"remote_user=$(whoami 2>/dev/null || id -un 2>/dev/null || printf '')",
		"remote_uid=$(id -u 2>/dev/null || printf '')",
		"remote_host=$(uname -n 2>/dev/null || cat /etc/hostname 2>/dev/null || printf '')",
		"if command -v hostname >/dev/null 2>&1; then remote_host=$(hostname -s 2>/dev/null || hostname 2>/dev/null || printf '%s' \"$remote_host\"); fi",
		"printf '%s\\n' \"$remote_os\" \"$remote_arch\" \"${SHELL:-/bin/sh}\" \"$(pwd)\" \"$remote_user\" \"$remote_host\" \"$remote_uid\" \"${XDG_RUNTIME_DIR:-}\" \"${TMPDIR:-}\" \"${HOME:-}\"",
	}, " && ")
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

func userHostFromEndpoint(endpoint string) (string, string) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", ""
	}
	if at := strings.LastIndex(endpoint, "@"); at >= 0 {
		return endpoint[:at], endpoint[at+1:]
	}
	return "", endpoint
}

func remoteDisplayCWD(user, host, cwd, endpoint string) string {
	if host == "" {
		_, host = userHostFromEndpoint(endpoint)
	}
	prefix := host
	if user != "" && host != "" {
		prefix = user + "@" + host
	}
	if prefix == "" {
		return cwd
	}
	if cwd == "" {
		return prefix
	}
	return prefix + ":" + cwd
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
