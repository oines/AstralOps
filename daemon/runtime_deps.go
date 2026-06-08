package main

import "context"

type sshRuntimeClient interface {
	Call(context.Context, Workspace, string, any, any) error
}

type runtimeDeps struct {
	store                           *store
	agents                          map[AgentKind]agentInfo
	ssh                             sshRuntimeClient
	emit                            func(AstralEvent)
	setSessionStatus                func(string, string)
	startNextQueuedTurn             func(string)
	syncRemoteSkillTree             func(context.Context, Workspace, string, string) error
	writeClaudeRemoteMCPConfig      func(Workspace) (string, error)
	prepareCodexRemoteHome          func(context.Context, Workspace) (string, error)
	prepareCodexRemoteBundledSkills func(context.Context, Workspace, string) (string, error)
	codexExecServerURL              func(string) string
	setCodexRemoteHome              func(string, string)
	codexExecCommand                func(string, string) (codexExecCommand, bool)
}

func runtimeDepsFromApp(a *app) runtimeDeps {
	deps := runtimeDeps{
		store:  a.store,
		agents: a.agents,
		emit: func(event AstralEvent) {
			if a.runtimeEvents != nil {
				a.runtimeEvents.Emit(event)
				return
			}
			a.emit(event)
		},
		setSessionStatus: func(sessionID, status string) {
			a.sessionControlPlane().RecordRuntimeStatus(sessionID, status)
		},
		startNextQueuedTurn: func(sessionID string) {
			if a.runtimeEvents != nil && a.runtimeEvents.HasSink(sessionID) {
				return
			}
			a.sessionControlPlane().StartNextQueuedTurn(sessionID)
		},
		syncRemoteSkillTree:             a.syncRemoteSkillTree,
		writeClaudeRemoteMCPConfig:      a.writeClaudeRemoteMCPConfig,
		prepareCodexRemoteHome:          a.prepareCodexRemoteHome,
		prepareCodexRemoteBundledSkills: a.prepareCodexRemoteBundledSkills,
		codexExecServerURL:              a.codexExecServerURL,
		setCodexRemoteHome:              a.setCodexRemoteHome,
		codexExecCommand:                a.codexExecCommand,
	}
	if ssh := a.sshService(); ssh != nil {
		deps.ssh = ssh
	}
	return deps
}

func (d runtimeDeps) updateSessionStatus(sessionID, status string) {
	if d.setSessionStatus != nil {
		d.setSessionStatus(sessionID, status)
		return
	}
	if d.store != nil {
		d.store.updateSessionStatus(sessionID, status)
	}
}
