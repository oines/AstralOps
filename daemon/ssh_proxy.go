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
	sshBrowseSessionTTL    = 5 * time.Minute
)

type sshManager struct {
	deps   sshDeps
	mu     sync.Mutex
	by     map[string]*sshTarget
	browse map[string]*sshBrowseSession
}

type sshDeps struct {
	currentSettings       func() AppSettings
	listWorkspaces        func() []Workspace
	latestConnection      func(string) (WorkspaceConnection, bool)
	stopWorkspaceSessions func(string, string)
	emit                  func(AstralEvent)
	dataDir               string
}

type sshTarget struct {
	workspace Workspace
	proxy     *proxyClient
	state     WorkspaceConnection
}

type sshBrowseSession struct {
	workspace Workspace
	proxy     *proxyClient
	expiresAt time.Time
}

func newSSHManager(a *app) *sshManager {
	return &sshManager{deps: sshDepsFromApp(a), by: map[string]*sshTarget{}, browse: map[string]*sshBrowseSession{}}
}

func sshDepsFromApp(a *app) sshDeps {
	deps := sshDeps{}
	if a != nil {
		deps.currentSettings = a.currentSettings
		deps.stopWorkspaceSessions = a.stopWorkspaceSessions
		deps.emit = a.emit
		if a.store != nil {
			deps.listWorkspaces = a.store.listWorkspaces
			deps.latestConnection = a.store.latestWorkspaceConnection
			deps.dataDir = a.store.dataDir
		}
	}
	return deps
}

func (m *sshManager) restorePersistedConnections(ctx context.Context) {
	if m == nil || m.deps.currentSettings == nil || m.deps.listWorkspaces == nil || m.deps.latestConnection == nil {
		return
	}
	autoReconnect := m.deps.currentSettings().Workspace.SSHAutoReconnect
	for _, ws := range m.deps.listWorkspaces() {
		if ws.Target != "ssh" {
			continue
		}
		if !autoReconnect {
			m.seedState(ws, initialSSHConnection(ws, connectionDisconnected))
			continue
		}
		last, ok := m.deps.latestConnection(ws.ID)
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
	required := []string{"hello", "read", "read_range", "write", "remove", "move", "list", "stat", "exec_start", "exec_kill", "pty_start", "pty_kill"}
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
	if m.deps.stopWorkspaceSessions != nil {
		m.deps.stopWorkspaceSessions(ws.ID, message)
	}
	return errors.New(message)
}

func (m *sshManager) callEphemeral(ctx context.Context, ws Workspace, method string, params any, out any) error {
	proxy, err := m.openEphemeralProxy(ctx, ws)
	if err != nil {
		return err
	}
	defer proxy.close()
	return proxy.call(ctx, method, params, out)
}

func (m *sshManager) callBrowse(ctx context.Context, ws Workspace, method string, params any, out any) error {
	key := sshBrowseSessionKey(ws)
	if key == "" {
		return m.callEphemeral(ctx, ws, method, params, out)
	}
	proxy := m.cachedBrowseProxy(key)
	if proxy != nil {
		err := proxy.call(ctx, method, params, out)
		if err == nil {
			m.extendBrowseSession(key)
			return nil
		}
		if !isProxyTransportError(err) {
			return err
		}
		m.closeBrowseSession(key, proxy)
	}
	proxy, err := m.openEphemeralProxy(ctx, ws)
	if err != nil {
		return err
	}
	if err := proxy.call(ctx, method, params, out); err != nil {
		if !isProxyTransportError(err) {
			m.storeBrowseProxy(key, ws, proxy)
			return err
		}
		_ = proxy.close()
		return err
	}
	m.storeBrowseProxy(key, ws, proxy)
	return nil
}

func (m *sshManager) openEphemeralProxy(ctx context.Context, ws Workspace) (*proxyClient, error) {
	if ws.Target != "ssh" {
		return nil, errors.New("workspace is not ssh")
	}
	if ws.SSH == nil {
		return nil, errors.New("ssh workspace is missing ssh config")
	}
	probe, err := m.probe(ctx, ws)
	if err != nil {
		return nil, err
	}
	localHelper, err := m.localHelperBinary(ctx, probe)
	if err != nil {
		return nil, err
	}
	localSum, err := fileSHA256(localHelper)
	if err != nil {
		return nil, err
	}
	attempts := []remoteHelperAttempt{}
	for _, candidate := range remoteHelperCandidates(ws, probe) {
		_, proxy, _, err := m.tryRemoteHelperCandidate(ctx, ws, probe, candidate, localHelper, localSum, false)
		if err == nil {
			return proxy, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempts = append(attempts, remoteHelperAttempt{Candidate: candidate, Err: err})
		var noFallback remoteHelperNoFallbackError
		if errors.As(err, &noFallback) {
			break
		}
	}
	return nil, remoteHelperAttemptsError(attempts)
}

func (m *sshManager) startPTY(ctx context.Context, ws Workspace, id string, params map[string]any) (*proxyClient, <-chan proxyEvent, func(), map[string]any, error) {
	return m.startEventProcess(ctx, ws, id, "pty_start", params)
}

func sshBrowseSessionKey(ws Workspace) string {
	if ws.Target != "ssh" || ws.SSH == nil {
		return ""
	}
	endpoint := strings.TrimSpace(ws.SSH.Endpoint)
	if endpoint == "" {
		return ""
	}
	return strings.Join([]string{endpoint, strconv.Itoa(sshPort(ws))}, "\x00")
}

func (m *sshManager) cachedBrowseProxy(key string) *proxyClient {
	now := time.Now()
	m.mu.Lock()
	session := m.browse[key]
	if session == nil || session.proxy == nil || !session.proxy.isAlive() || now.After(session.expiresAt) {
		if session != nil {
			delete(m.browse, key)
		}
		m.mu.Unlock()
		if session != nil && session.proxy != nil {
			_ = session.proxy.close()
		}
		return nil
	}
	session.expiresAt = now.Add(sshBrowseSessionTTL)
	proxy := session.proxy
	m.mu.Unlock()
	return proxy
}

func (m *sshManager) storeBrowseProxy(key string, ws Workspace, proxy *proxyClient) {
	existing := (*proxyClient)(nil)
	m.mu.Lock()
	if current := m.browse[key]; current != nil && current.proxy != proxy {
		existing = current.proxy
	}
	m.browse[key] = &sshBrowseSession{
		workspace: ws,
		proxy:     proxy,
		expiresAt: time.Now().Add(sshBrowseSessionTTL),
	}
	m.mu.Unlock()
	if existing != nil {
		_ = existing.close()
	}
	m.scheduleBrowseSessionCleanup(key)
}

func (m *sshManager) extendBrowseSession(key string) {
	m.mu.Lock()
	if session := m.browse[key]; session != nil {
		session.expiresAt = time.Now().Add(sshBrowseSessionTTL)
	}
	m.mu.Unlock()
}

func (m *sshManager) closeBrowseSession(key string, proxy *proxyClient) {
	m.mu.Lock()
	session := m.browse[key]
	if session != nil && (proxy == nil || session.proxy == proxy) {
		delete(m.browse, key)
	} else {
		session = nil
	}
	m.mu.Unlock()
	if session != nil && session.proxy != nil {
		_ = session.proxy.close()
	}
}

func (m *sshManager) scheduleBrowseSessionCleanup(key string) {
	time.AfterFunc(sshBrowseSessionTTL, func() {
		m.mu.Lock()
		session := m.browse[key]
		if session == nil {
			m.mu.Unlock()
			return
		}
		delay := time.Until(session.expiresAt)
		if delay > 0 {
			m.mu.Unlock()
			time.AfterFunc(delay, func() { m.closeExpiredBrowseSession(key) })
			return
		}
		delete(m.browse, key)
		m.mu.Unlock()
		if session.proxy != nil {
			_ = session.proxy.close()
		}
	})
}

func (m *sshManager) closeExpiredBrowseSession(key string) {
	m.mu.Lock()
	session := m.browse[key]
	if session == nil || time.Now().Before(session.expiresAt) {
		m.mu.Unlock()
		return
	}
	delete(m.browse, key)
	m.mu.Unlock()
	if session.proxy != nil {
		_ = session.proxy.close()
	}
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
				killStartedEventProcess(proxy, method, id)
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
	if m.deps.stopWorkspaceSessions != nil {
		m.deps.stopWorkspaceSessions(ws.ID, message)
	}
	return nil, nil, nil, nil, errors.New(message)
}

func killStartedEventProcess(proxy *proxyClient, startMethod string, id string) {
	killMethod := ""
	switch startMethod {
	case "exec_start":
		killMethod = "exec_kill"
	case "pty_start":
		killMethod = "pty_kill"
	}
	if killMethod == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = proxy.call(ctx, killMethod, map[string]any{"id": id}, nil)
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
	if m == nil || m.deps.emit == nil {
		return
	}
	m.deps.emit(AstralEvent{WorkspaceID: ws.ID, Agent: ws.Agent, Kind: "workspace.connection", Normalized: eventNormalized("workspace.connection", state)})
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
	out, stderr, err := runSSHOutput(ctx, ws, "probe", script, map[string]any{"remote_cwd": remoteCWD})
	if err != nil {
		return sshProbe{}, fmt.Errorf("ssh probe failed: %s%s", err.Error(), stderrSuffix(stderr))
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

func (m *sshManager) tryRemoteHelperCandidateOnce(ctx context.Context, ws Workspace, candidate remoteHelperCandidate, localHelper, localSum string, forceUpload bool) (helper helperUpload, proxy *proxyClient, hello map[string]any, err error) {
	candidateStartedAt := logDiagnosticSpanStart("ssh.helper.candidate", sshHelperCandidateLogFields(ws, candidate, forceUpload, helper))
	defer func() {
		fields := sshHelperCandidateLogFields(ws, candidate, forceUpload, helper)
		if err != nil {
			logDiagnosticSpanFailed("ssh.helper.candidate", candidateStartedAt, err, fields)
			return
		}
		logDiagnosticSpanCompleted("ssh.helper.candidate", candidateStartedAt, fields)
	}()
	helper, err = m.ensureHelperAt(ctx, ws, candidate, localHelper, localSum, forceUpload)
	if err != nil {
		return
	}
	proxy, err = startProxyClient(ws, helper)
	if err != nil {
		err = remoteHelperNoFallbackError{err: err}
		return
	}
	hello, err = proxy.hello(ctx)
	if err != nil {
		_ = proxy.close()
		err = remoteHelperNoFallbackError{err: err}
		return
	}
	if validateErr := validateProxyHello(hello); validateErr != nil {
		_ = proxy.close()
		err = remoteHelperValidationError{err: validateErr}
		return
	}
	return
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
	if out, err := runSSHCombinedOutput(ctx, ws, "helper.mkdir", mkdir, map[string]any{
		"candidate":  candidate.Label,
		"base_dir":   candidate.BaseDir,
		"remote_dir": remoteDir,
	}); err != nil {
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
		if out, err := runSSHCombinedOutput(ctx, ws, "helper.install", install, map[string]any{
			"candidate":   candidate.Label,
			"remote_path": remotePath,
			"remote_temp": remoteTemp,
		}); err != nil {
			cleanup := "rm -f " + shellQuote(remoteTemp)
			_, _ = runSSHCombinedOutput(context.Background(), ws, "helper.cleanup", cleanup, map[string]any{
				"candidate":   candidate.Label,
				"remote_temp": remoteTemp,
			})
			return helper, fmt.Errorf("install remote helper failed: %s%s", err.Error(), stderrSuffix(string(out)))
		}
	}
	helper.Uploaded = uploaded
	verify := "exec " + shellQuote(remotePath) + " --self-test"
	if out, err := runSSHCombinedOutput(ctx, ws, "helper.self_test", verify, map[string]any{
		"candidate":   candidate.Label,
		"remote_path": remotePath,
		"uploaded":    uploaded,
	}); err != nil {
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
	out, _, err := runSSHOutput(ctx, ws, "helper.hash", script, map[string]any{"remote_path": remotePath})
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

	details := map[string]any{
		"local_path":  localPath,
		"remote_path": remotePath,
	}
	if stat, statErr := file.Stat(); statErr == nil {
		details["bytes"] = stat.Size()
	}
	stderr, err := runSSHWithInput(ctx, ws, "helper.upload", "cat > "+shellQuote(remotePath), file, details)
	if err != nil {
		return fmt.Errorf("%s%s", err.Error(), stderrSuffix(stderr))
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
	out := filepath.Join(m.deps.dataDir, "helpers", probe.OS+"-"+probe.Arch, "astral-proxy-agent")
	root := repoRootGuess()
	if _, err := os.Stat(filepath.Join(root, "proxy-agent", "main.go")); err == nil {
		if st, statErr := os.Stat(out); statErr == nil && helperBinaryUsable(st) && helperBinaryFresh(root, st.ModTime()) {
			return out, nil
		}
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
	if body, err := runLocalCombinedOutput(cmd, "helper.build", map[string]any{
		"os":   probe.OS,
		"arch": probe.Arch,
		"out":  out,
		"root": root,
	}); err != nil {
		return "", fmt.Errorf("build proxy helper for %s/%s failed: %s%s", probe.OS, probe.Arch, err.Error(), stderrSuffix(string(body)))
	}
	return out, nil
}

func helperBinaryFresh(root string, builtAt time.Time) bool {
	latest := time.Time{}
	for _, path := range []string{filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum")} {
		if st, err := os.Stat(path); err == nil && st.ModTime().After(latest) {
			latest = st.ModTime()
		}
	}
	sourceRoot := filepath.Join(root, "proxy-agent")
	_ = filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if st, statErr := entry.Info(); statErr == nil && st.ModTime().After(latest) {
			latest = st.ModTime()
		}
		return nil
	})
	return !latest.IsZero() && !latest.After(builtAt)
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

func runSSHOutput(ctx context.Context, ws Workspace, operation string, remoteCommand string, details map[string]any) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, "ssh", sshOneShotCommandArgs(ws, remoteCommand)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	fields := sshCommandLogFields(ws, operation, details)
	fields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	startedAt := logDiagnosticSpanStart("ssh.command", fields)
	out, err := cmd.Output()
	doneFields := sshCommandLogFields(ws, operation, details)
	doneFields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	doneFields["stdout_bytes"] = len(out)
	if stderr.Len() > 0 {
		doneFields["stderr_bytes"] = stderr.Len()
	}
	if err != nil {
		if stderr.Len() > 0 {
			doneFields["stderr_tail"] = diagnosticLogTail(stderr.String())
		}
		logDiagnosticSpanFailed("ssh.command", startedAt, err, doneFields)
		return out, userSSHStderr(stderr.String()), err
	}
	logDiagnosticSpanCompleted("ssh.command", startedAt, doneFields)
	return out, userSSHStderr(stderr.String()), nil
}

func runSSHCombinedOutput(ctx context.Context, ws Workspace, operation string, remoteCommand string, details map[string]any) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ssh", sshOneShotCommandArgs(ws, remoteCommand)...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	fields := sshCommandLogFields(ws, operation, details)
	fields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	startedAt := logDiagnosticSpanStart("ssh.command", fields)
	err := cmd.Run()
	doneFields := sshCommandLogFields(ws, operation, details)
	doneFields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	doneFields["stdout_bytes"] = stdout.Len()
	if stderr.Len() > 0 {
		doneFields["stderr_bytes"] = stderr.Len()
	}
	if err != nil {
		if stdout.Len() > 0 {
			doneFields["stdout_tail"] = diagnosticLogTail(stdout.String())
		}
		if stderr.Len() > 0 {
			doneFields["stderr_tail"] = diagnosticLogTail(stderr.String())
		}
		logDiagnosticSpanFailed("ssh.command", startedAt, err, doneFields)
		return combinedCommandOutput(stdout.String(), userSSHStderr(stderr.String())), err
	}
	logDiagnosticSpanCompleted("ssh.command", startedAt, doneFields)
	return stdout.Bytes(), nil
}

func runSSHWithInput(ctx context.Context, ws Workspace, operation string, remoteCommand string, input io.Reader, details map[string]any) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh", sshOneShotCommandArgs(ws, remoteCommand)...)
	cmd.Stdin = input
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	fields := sshCommandLogFields(ws, operation, details)
	fields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	startedAt := logDiagnosticSpanStart("ssh.command", fields)
	err := cmd.Run()
	doneFields := sshCommandLogFields(ws, operation, details)
	doneFields["ssh_verbose"] = daemonDiagnosticLoggingEnabled()
	if stderr.Len() > 0 {
		doneFields["stderr_bytes"] = stderr.Len()
	}
	if err != nil {
		if stderr.Len() > 0 {
			doneFields["stderr_tail"] = diagnosticLogTail(stderr.String())
		}
		logDiagnosticSpanFailed("ssh.command", startedAt, err, doneFields)
		return userSSHStderr(stderr.String()), err
	}
	logDiagnosticSpanCompleted("ssh.command", startedAt, doneFields)
	return userSSHStderr(stderr.String()), nil
}

func runLocalCombinedOutput(cmd *exec.Cmd, operation string, details map[string]any) ([]byte, error) {
	fields := copyDiagnosticFields(details)
	fields["operation"] = operation
	fields["binary"] = filepath.Base(cmd.Path)
	if cmd.Dir != "" {
		fields["cwd"] = cmd.Dir
	}
	startedAt := logDiagnosticSpanStart("local.command", fields)
	out, err := cmd.CombinedOutput()
	doneFields := copyDiagnosticFields(fields)
	doneFields["output_bytes"] = len(out)
	if err != nil {
		if len(out) > 0 {
			doneFields["output_tail"] = diagnosticLogTail(string(out))
		}
		logDiagnosticSpanFailed("local.command", startedAt, err, doneFields)
		return out, err
	}
	logDiagnosticSpanCompleted("local.command", startedAt, doneFields)
	return out, nil
}

func sshCommandLogFields(ws Workspace, operation string, details map[string]any) map[string]any {
	fields := map[string]any{
		"workspace_id": ws.ID,
		"operation":    operation,
		"binary":       "ssh",
		"port":         sshPort(ws),
	}
	if ws.SSH != nil {
		fields["endpoint"] = ws.SSH.Endpoint
		fields["remote_cwd"] = ws.SSH.RemoteCWD
	}
	for key, value := range details {
		fields[key] = value
	}
	return fields
}

func sshOneShotCommandArgs(ws Workspace, remoteCommand string) []string {
	args := append([]string{}, sshArgs(ws)...)
	if daemonDiagnosticLoggingEnabled() {
		args = append(args, "-v")
	}
	return append(args, ws.SSH.Endpoint, remoteCommand)
}

func userSSHStderr(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	filtered := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "OpenSSH_") || strings.HasPrefix(trimmed, "debug1:") || strings.HasPrefix(trimmed, "debug2:") || strings.HasPrefix(trimmed, "debug3:") {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	return strings.Join(filtered, "\n")
}

func combinedCommandOutput(stdout string, stderr string) []byte {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return []byte(stderr)
	case stderr == "":
		return []byte(stdout)
	default:
		return []byte(stdout + "\n" + stderr)
	}
}

func sshHelperCandidateLogFields(ws Workspace, candidate remoteHelperCandidate, forceUpload bool, helper helperUpload) map[string]any {
	fields := map[string]any{
		"workspace_id":  ws.ID,
		"endpoint":      "",
		"port":          sshPort(ws),
		"candidate":     candidate.Label,
		"base_dir":      candidate.BaseDir,
		"remote_dir":    candidate.RemoteDir,
		"force_upload":  forceUpload,
		"helper_upload": helper.Uploaded,
	}
	if ws.SSH != nil {
		fields["endpoint"] = ws.SSH.Endpoint
		fields["remote_cwd"] = ws.SSH.RemoteCWD
	}
	if helper.RemotePath != "" {
		fields["remote_path"] = helper.RemotePath
	}
	return fields
}

func startProxyClient(ws Workspace, helper helperUpload) (*proxyClient, error) {
	command := "exec " + shellQuote(helper.RemotePath) + " --cwd " + shellQuote(ws.SSH.RemoteCWD)
	cmd := exec.Command("ssh", append(sshArgs(ws), ws.SSH.Endpoint, command)...)
	fields := sshCommandLogFields(ws, "proxy.start", map[string]any{
		"remote_path": helper.RemotePath,
		"remote_dir":  helper.RemoteDir,
		"uploaded":    helper.Uploaded,
	})
	startedAt := logDiagnosticSpanStart("ssh.command", fields)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		failedFields := sshCommandLogFields(ws, "proxy.start", map[string]any{
			"remote_path": helper.RemotePath,
			"remote_dir":  helper.RemoteDir,
			"uploaded":    helper.Uploaded,
			"step":        "stdin_pipe",
		})
		logDiagnosticSpanFailed("ssh.command", startedAt, err, failedFields)
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		failedFields := sshCommandLogFields(ws, "proxy.start", map[string]any{
			"remote_path": helper.RemotePath,
			"remote_dir":  helper.RemoteDir,
			"uploaded":    helper.Uploaded,
			"step":        "stdout_pipe",
		})
		logDiagnosticSpanFailed("ssh.command", startedAt, err, failedFields)
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		failedFields := sshCommandLogFields(ws, "proxy.start", map[string]any{
			"remote_path": helper.RemotePath,
			"remote_dir":  helper.RemoteDir,
			"uploaded":    helper.Uploaded,
			"step":        "stderr_pipe",
		})
		logDiagnosticSpanFailed("ssh.command", startedAt, err, failedFields)
		return nil, err
	}
	client := newProxyClient(ws, cmd, stdin, stdout, stderr)
	if err := cmd.Start(); err != nil {
		failedFields := sshCommandLogFields(ws, "proxy.start", map[string]any{
			"remote_path": helper.RemotePath,
			"remote_dir":  helper.RemoteDir,
			"uploaded":    helper.Uploaded,
			"step":        "start",
		})
		logDiagnosticSpanFailed("ssh.command", startedAt, err, failedFields)
		return nil, err
	}
	completedFields := sshCommandLogFields(ws, "proxy.start", map[string]any{
		"remote_path": helper.RemotePath,
		"remote_dir":  helper.RemoteDir,
		"uploaded":    helper.Uploaded,
		"pid":         cmd.Process.Pid,
	})
	logDiagnosticSpanCompleted("ssh.command", startedAt, completedFields)
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
	startedAt := logSSHProxyCallStart(p.workspace, method, params)
	id := strconv.FormatInt(time.Now().UnixNano(), 36) + "_" + strconv.FormatInt(p.next(), 36)
	req := map[string]any{"id": id, "method": method, "params": params}
	body, _ := json.Marshal(req)
	ch := make(chan proxyResponse, 1)
	p.mu.Lock()
	if !p.alive {
		p.mu.Unlock()
		err := proxyTransportError{err: errors.New("ssh proxy is not running")}
		logSSHProxyCallFailed(p.workspace, method, startedAt, err)
		return err
	}
	p.pending[id] = ch
	_, err := p.stdin.Write(append(body, '\n'))
	p.mu.Unlock()
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		callErr := proxyTransportError{err: err}
		logSSHProxyCallFailed(p.workspace, method, startedAt, callErr)
		return callErr
	}
	select {
	case res := <-ch:
		if res.Error != "" {
			if res.Transport {
				err := proxyTransportError{err: errors.New(res.Error)}
				logSSHProxyCallFailed(p.workspace, method, startedAt, err)
				return err
			}
			err := errors.New(res.Error)
			logSSHProxyCallFailed(p.workspace, method, startedAt, err)
			return err
		}
		if out == nil {
			logSSHProxyCallCompleted(p.workspace, method, startedAt)
			return nil
		}
		if err := json.Unmarshal(res.Result, out); err != nil {
			logSSHProxyCallFailed(p.workspace, method, startedAt, err)
			return err
		}
		logSSHProxyCallCompleted(p.workspace, method, startedAt)
		return nil
	case <-ctx.Done():
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		err := proxyTransportError{err: ctx.Err()}
		logSSHProxyCallFailed(p.workspace, method, startedAt, err)
		return err
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
	if err != nil {
		return "."
	}
	return repoRootGuessFrom(wd, func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		return err == nil
	}, filepath.Dir)
}

func repoRootGuessFrom(wd string, hasGoMod func(string) bool, parentDir func(string) string) string {
	if wd == "" {
		return "."
	}
	for dir := wd; ; {
		if hasGoMod != nil && hasGoMod(dir) {
			return dir
		}
		parent := "."
		if parentDir != nil {
			parent = parentDir(dir)
		}
		if parent == "" || parent == dir || dir == "." {
			break
		}
		dir = parent
	}
	return wd
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
