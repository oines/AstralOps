package main

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	commandKindAction = "action"
	commandKindClient = "client"
	commandKindPrompt = "prompt"
)

func (a *app) listSessionCommands(sessionID string) ([]SessionCommand, bool) {
	ss, ok := a.store.getSession(sessionID)
	if !ok {
		return nil, false
	}
	ws, _ := a.store.getWorkspace(ss.WorkspaceID)
	context := a.sessionProjections().latestContext(sessionID)
	usedPercent := contextUsedPercent(context)
	running := ss.Status == "running"

	commands := []SessionCommand{
		{
			ID:          "compact",
			Title:       "Compact",
			Description: compactDescription(usedPercent),
			Icon:        "rotate-ccw",
			Kind:        commandKindAction,
			Agent:       ss.Agent,
			Enabled:     !running,
		},
		{
			ID:          "status",
			Title:       "Status",
			Description: "Show conversation ID, context usage, and rate limits",
			Icon:        "radio",
			Kind:        commandKindAction,
			Agent:       ss.Agent,
			Enabled:     true,
		},
		{
			ID:           "model",
			Title:        "Model",
			Description:  currentModelDescription(a.agents[ss.Agent]),
			Icon:         "box",
			Kind:         commandKindClient,
			Agent:        ss.Agent,
			Enabled:      true,
			ClientAction: "open_model_menu",
		},
		{
			ID:           "reasoning",
			Title:        "Reasoning",
			Description:  currentEffortDescription(a.agents[ss.Agent]),
			Icon:         "brain",
			Kind:         commandKindClient,
			Agent:        ss.Agent,
			Enabled:      true,
			ClientAction: "open_model_menu",
		},
		{
			ID:           "plan-mode",
			Title:        "Plan mode",
			Description:  "Enable plan mode",
			Icon:         "list-checks",
			Kind:         commandKindClient,
			Agent:        ss.Agent,
			Enabled:      true,
			ClientAction: "run_mode",
			Payload:      map[string]any{"run_mode": "plan"},
		},
	}
	if ss.Agent == AgentCodex {
		commands = append(commands,
			SessionCommand{ID: "goal", Title: "Goal", Description: "Set a goal that Codex will keep working toward", Icon: "target", Kind: commandKindClient, Agent: ss.Agent, Enabled: true, ClientAction: "goal_mode"},
		)
	}
	if ss.Agent == AgentClaude {
		seen := map[string]bool{}
		for _, command := range commands {
			seen[command.ID] = true
		}
		for _, slash := range a.sessionProjections().claudeSlashCommands(sessionID) {
			if seen[slash] {
				continue
			}
			commands = append(commands, SessionCommand{
				ID:          "claude:" + slash,
				Title:       slash,
				Description: "Send /" + slash + " to Claude Code",
				Icon:        claudeSlashIcon(slash),
				Kind:        commandKindPrompt,
				Agent:       ss.Agent,
				Enabled:     !running,
				Payload:     map[string]any{"input": "/" + slash},
			})
		}
	}
	for index := range commands {
		if !commands[index].Enabled && commands[index].DisabledReason == "" {
			commands[index].DisabledReason = "The current session is running"
		}
		if ws.Target == "ssh" && ss.Agent == AgentClaude && commands[index].ID == "compact" {
			commands[index].Description = "Send /compact to Claude Code"
		}
	}
	sort.SliceStable(commands, func(i, j int) bool {
		return commandRank(commands[i].ID) < commandRank(commands[j].ID)
	})
	return commands, true
}

func (a *app) handleListSessionCommands(w http.ResponseWriter, sessionID string) {
	commands, ok := a.listSessionCommands(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, SessionCommandListResponse{Commands: commands})
}

func (a *app) handleRunSessionCommand(w http.ResponseWriter, sessionID, commandID string, req SessionCommandRequest) {
	commands, ok := a.listSessionCommands(sessionID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	var command SessionCommand
	for _, item := range commands {
		if item.ID == commandID {
			command = item
			break
		}
	}
	if command.ID == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "command not found"})
		return
	}
	if !command.Enabled {
		writeJSON(w, http.StatusConflict, map[string]string{"error": firstString(command.DisabledReason, "command is disabled")})
		return
	}
	ss, _ := a.store.getSession(sessionID)
	ws, _ := a.store.getWorkspace(ss.WorkspaceID)
	if command.Kind != commandKindClient && command.ID != "status" {
		linked, err := a.linkSessionForCommand(ss)
		if err != nil {
			writeActionError(w, err)
			return
		}
		ss = linked
		ws, _ = a.store.getWorkspace(ss.WorkspaceID)
	}
	switch command.Kind {
	case commandKindPrompt:
		input := firstString(mapValue(command.Payload)["input"], "/"+strings.TrimPrefix(command.ID, "claude:"))
		a.startSessionPromptCommand(w, ss, ws, input)
	case commandKindAction:
		if command.ID == "status" {
			a.emitSessionStatusSnapshot(ss)
			writeJSON(w, http.StatusOK, SessionCommandResponse{OK: true})
			return
		}
		runtime, ok := a.runtimes[ss.Agent]
		if !ok {
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
			return
		}
		runner, ok := runtime.(CommandRunner)
		if !ok {
			if ss.Agent == AgentClaude && command.ID == "compact" {
				a.startSessionPromptCommand(w, ss, ws, "/compact")
				return
			}
			writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "command is not implemented for this agent"})
			return
		}
		if err := runner.RunCommand(ss, ws, command.ID, req.Args); err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, ErrSessionRunning) {
				status = http.StatusConflict
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, SessionCommandResponse{OK: true})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "client command cannot be executed by daemon"})
	}
}

func (a *app) linkSessionForCommand(ss Session) (Session, error) {
	switch ss.Source {
	case SessionSourceLegacyUnlinked:
		return Session{}, newActionError(http.StatusConflict, "native_history_missing", "native history is missing for this session")
	case SessionSourceDiscovered:
		return Session{}, newActionError(http.StatusConflict, "native_session_not_imported", "native session must be imported before control")
	default:
		return ss, nil
	}
}

func (a *app) startSessionPromptCommand(w http.ResponseWriter, ss Session, ws Workspace, input string) {
	runtime, ok := a.runtimes[ss.Agent]
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "agent runtime is not implemented"})
		return
	}
	if err := runtime.StartTurn(ss, ws, input, TurnOptions{}); err != nil {
		if errors.Is(err, ErrSessionRunning) {
			turn := a.enqueueTurn(ss, input, TurnOptions{})
			writeJSON(w, http.StatusOK, SessionCommandResponse{OK: true, Queued: true, QueueID: turn.ID})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(input) == "/compact" {
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "memory.compacting", Normalized: eventNormalized("memory.compacting", map[string]any{
			"source":  "astralops",
			"command": "compact",
			"status":  "running",
		})})
	}
	writeJSON(w, http.StatusOK, SessionCommandResponse{OK: true})
}

func (a *app) emitSessionStatusSnapshot(ss Session) {
	a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.status", Normalized: eventNormalized("control.status", map[string]any{
		"source":            "astralops",
		"session_id":        ss.ID,
		"native_session_id": ss.NativeSessionID,
		"native_thread_id":  ss.NativeThreadID,
		"status":            ss.Status,
		"message":           "Status refreshed",
	})})
	if context := a.sessionProjections().latestContext(ss.ID); len(context) > 0 {
		context["source"] = "astralops"
		context["session_id"] = ss.ID
		a.emit(AstralEvent{WorkspaceID: ss.WorkspaceID, SessionID: ss.ID, Agent: ss.Agent, Kind: "control.context", Normalized: eventNormalized("control.context", context)})
	}
}

func compactDescription(percent int) string {
	if percent > 0 {
		return fmt.Sprintf("Compact this conversation context (%d%% used)", percent)
	}
	return "Compact this conversation context"
}

func currentModelDescription(info agentInfo) string {
	if info.CurrentModel != "" {
		return info.CurrentModel
	}
	return "Select the model for this session"
}

func currentEffortDescription(info agentInfo) string {
	if info.CurrentEffort != "" {
		return info.CurrentEffort
	}
	return "Adjust reasoning effort"
}

func contextUsedPercent(value map[string]any) int {
	total := numberValue(firstNonNil(value["total_tokens"], value["totalTokens"]))
	window := numberValue(firstNonNil(value["model_context_window"], value["modelContextWindow"], value["context_window"], value["contextWindow"]))
	if total <= 0 || window <= 0 {
		return 0
	}
	percent := int((total / window) * 100)
	if percent < 1 {
		return 1
	}
	if percent > 999 {
		return 999
	}
	return percent
}

func commandRank(id string) int {
	switch id {
	case "compact":
		return 10
	case "status":
		return 20
	case "reasoning":
		return 30
	case "model":
		return 40
	case "plan-mode":
		return 50
	case "goal":
		return 60
	case "fork":
		return 70
	default:
		return 100
	}
}

func claudeSlashIcon(name string) string {
	switch name {
	case "clear":
		return "eraser"
	case "compact":
		return "rotate-ccw"
	case "context", "usage", "status":
		return "radio"
	case "model":
		return "box"
	case "review", "security-review":
		return "shield"
	case "init":
		return "file-plus"
	default:
		return "terminal"
	}
}
