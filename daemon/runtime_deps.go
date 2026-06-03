package main

import "context"

type runtimeDeps struct {
	store                           *store
	agents                          map[AgentKind]agentInfo
	ssh                             *sshManager
	emit                            func(AstralEvent)
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
	return runtimeDeps{
		store:  a.store,
		agents: a.agents,
		ssh:    a.ssh,
		emit:   a.emit,
		startNextQueuedTurn: func(sessionID string) {
			a.sessions().startNextQueuedTurn(sessionID)
		},
		syncRemoteSkillTree:             a.syncRemoteSkillTree,
		writeClaudeRemoteMCPConfig:      a.writeClaudeRemoteMCPConfig,
		prepareCodexRemoteHome:          a.prepareCodexRemoteHome,
		prepareCodexRemoteBundledSkills: a.prepareCodexRemoteBundledSkills,
		codexExecServerURL:              a.codexExecServerURL,
		setCodexRemoteHome:              a.setCodexRemoteHome,
		codexExecCommand:                a.codexExecCommand,
	}
}
