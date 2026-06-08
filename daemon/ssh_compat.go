package main

import (
	"io"
	"os/exec"
	"time"

	internalssh "github.com/oines/astralops/daemon/internal/ssh"
	"github.com/oines/astralops/pkg/protocol"
)

const (
	connectionDisconnected = internalssh.ConnectionDisconnected
	connectionConnecting   = internalssh.ConnectionConnecting
	connectionConnected    = internalssh.ConnectionConnected
	connectionReconnecting = internalssh.ConnectionReconnecting
	connectionDegraded     = internalssh.ConnectionDegraded
	connectionFailed       = internalssh.ConnectionFailed
	sshProxyMaxAttempts    = internalssh.ProxyMaxAttempts
)

type sshManager = internalssh.Manager
type proxyClient = internalssh.ProxyClient
type sshProbe = internalssh.Probe
type remoteHelperCandidate = internalssh.RemoteHelperCandidate

func newSSHManager(a *app) *sshManager {
	return internalssh.NewManager(sshDepsFromApp(a))
}

func sshDepsFromApp(a *app) internalssh.Deps {
	deps := internalssh.Deps{}
	if a != nil {
		deps.SSHAutoReconnect = func() bool {
			return a.currentSettings().Workspace.SSHAutoReconnect
		}
		deps.StopWorkspaceSessions = a.stopWorkspaceSessions
		deps.Emit = a.emit
		if a.store != nil {
			deps.ListWorkspaces = a.store.listWorkspaces
			deps.LatestConnection = a.store.latestWorkspaceConnection
			deps.DataDir = a.store.dataDir
		}
	}
	return deps
}

func initialSSHConnection(ws Workspace, status string) WorkspaceConnection {
	return internalssh.InitialConnection(ws, status)
}

func validateProxyHello(hello map[string]any) error {
	return internalssh.ValidateProxyHello(hello)
}

func fileSHA256(path string) (string, error) {
	return internalssh.FileSHA256(path)
}

func newProxyClient(ws protocol.Workspace, cmd *exec.Cmd, stdin io.WriteCloser, stdout io.Reader, stderr io.Reader) *proxyClient {
	return internalssh.NewProxyClientForTest(ws, cmd, stdin, stdout, stderr)
}

func shellQuote(value string) string {
	return internalssh.ShellQuote(value)
}

func localTCPHostPort(addr string) string {
	return internalssh.LocalTCPHostPort(addr)
}

func isProxyTransportError(err error) bool {
	return internalssh.IsProxyTransportError(err)
}

func sshArgs(ws Workspace) []string {
	return internalssh.Args(ws)
}

func remoteProbeScript(remoteCWD string) string {
	return internalssh.RemoteProbeScript(remoteCWD)
}

func remoteHelperCandidates(ws Workspace, probe sshProbe) []remoteHelperCandidate {
	return internalssh.RemoteHelperCandidates(ws, probe)
}

func sshBrowseSessionKey(ws Workspace) string {
	return internalssh.BrowseSessionKey(ws)
}

func helperBinaryFresh(root string, builtAt time.Time) bool {
	return internalssh.HelperBinaryFresh(root, builtAt)
}

func repoRootGuessFrom(wd string, hasGoMod func(string) bool, parentDir func(string) string) string {
	return internalssh.RepoRootGuessFrom(wd, hasGoMod, parentDir)
}
