package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

const (
	CapabilityCoreRead            = "core.read"
	CapabilityCoreControl         = "core.control"
	CapabilityInteractionRespond  = "interaction.respond"
	CapabilitySessionEdit         = "session.edit"
	CapabilityAttachmentIngest    = "attachment.ingest"
	CapabilityMediaRead           = "media.read"
	CapabilityMediaDownload       = "media.download"
	CapabilityMediaStream         = "media.stream"
	CapabilityWorkspaceFilesRead  = "workspace.files.read"
	CapabilityWorkspaceFilesWrite = "workspace.files.write"
	CapabilityWorkspaceExec       = "workspace.exec"
	CapabilityTerminalOpen        = "terminal.open"
	CapabilityTerminalInput       = "terminal.input"
	CapabilityHostManage          = "host.manage"
)

const (
	ControlActionSessionView        = "core.read.session_view"
	ControlActionSessions           = "core.read.sessions"
	ControlActionWorkspaces         = "core.read.workspaces"
	ControlActionSessionInput       = "core.control.session_input"
	ControlActionInterrupt          = "core.control.interrupt"
	ControlActionInteractionRespond = "interaction.respond"
	ControlActionSessionEdit        = "session.edit"
	ControlActionAttachmentIngest   = "attachment.ingest"
	ControlActionMediaRead          = "media.read"
	ControlActionMediaDownload      = "media.download"
	ControlActionTerminalOpen       = "terminal.open"
	ControlActionTerminalAttach     = "terminal.attach"
	ControlActionTerminalDetach     = "terminal.detach"
	ControlActionTerminalInput      = "terminal.input"
	ControlActionTerminalResize     = "terminal.resize"
	ControlActionTerminalClose      = "terminal.close"
)

type ControlRequest struct {
	RequestID          string         `json:"request_id,omitempty"`
	ControllerDeviceID string         `json:"controller_device_id"`
	Capability         string         `json:"capability"`
	Action             string         `json:"action"`
	Params             map[string]any `json:"params,omitempty"`
}

type ControlResponse struct {
	RequestID string        `json:"request_id,omitempty"`
	OK        bool          `json:"ok"`
	Result    any           `json:"result,omitempty"`
	Error     *ControlError `json:"error,omitempty"`
}

type ControlError struct {
	Status  int    `json:"status,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (a *app) executeControlRequest(req ControlRequest) (ControlResponse, error) {
	return a.executeControlRequestWithConnection(req, nil)
}

func (a *app) executeControlRequestWithConnection(req ControlRequest, conn *controlWSConn) (ControlResponse, error) {
	requiredCapability := controlActionCapability(req.Action)
	if requiredCapability == "" {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusNotFound, "control_action_unknown", "control action not found")
	}
	if strings.TrimSpace(req.ControllerDeviceID) == "" {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusBadRequest, "controller_device_required", "controller_device_id required")
	}
	if req.Capability != requiredCapability {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusForbidden, "capability_mismatch", "control capability does not match action")
	}
	grant, ok := a.store.trustedControlGrant(req.ControllerDeviceID)
	if !ok || !trustGrantAllows(grant, requiredCapability) {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusForbidden, "capability_denied", "controller is not allowed to use capability")
	}

	result, err := a.dispatchControlAction(req, conn)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID}, err
	}
	return ControlResponse{RequestID: req.RequestID, OK: true, Result: result}, nil
}

func controlActionCapability(action string) string {
	switch action {
	case ControlActionSessionView, ControlActionSessions, ControlActionWorkspaces:
		return CapabilityCoreRead
	case ControlActionSessionInput, ControlActionInterrupt:
		return CapabilityCoreControl
	case ControlActionInteractionRespond:
		return CapabilityInteractionRespond
	case ControlActionSessionEdit:
		return CapabilitySessionEdit
	case ControlActionAttachmentIngest:
		return CapabilityAttachmentIngest
	case ControlActionMediaRead:
		return CapabilityMediaRead
	case ControlActionMediaDownload:
		return CapabilityMediaDownload
	case ControlActionTerminalOpen, ControlActionTerminalAttach, ControlActionTerminalDetach:
		return CapabilityTerminalOpen
	case ControlActionTerminalInput, ControlActionTerminalResize, ControlActionTerminalClose:
		return CapabilityTerminalInput
	default:
		return ""
	}
}

func (a *app) dispatchControlAction(req ControlRequest, conn *controlWSConn) (any, error) {
	switch req.Action {
	case ControlActionSessionView:
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		view, ok := a.buildSessionView(params.SessionID)
		if !ok {
			return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
		}
		return view, nil
	case ControlActionSessions:
		var params struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.store.listSessions(params.WorkspaceID), nil
	case ControlActionWorkspaces:
		return a.store.listWorkspaces(), nil
	case ControlActionSessionInput:
		var params struct {
			SessionID       string            `json:"session_id"`
			Input           string            `json:"input"`
			Model           string            `json:"model"`
			ReasoningEffort string            `json:"reasoning_effort"`
			PermissionMode  string            `json:"permission_mode"`
			Attachments     []InputAttachment `json:"attachments"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		attachments, err := a.resolveControlInputAttachments(params.SessionID, params.Attachments)
		if err != nil {
			return nil, err
		}
		return a.startSessionInput(params.SessionID, params.Input, TurnOptions{
			Model:           params.Model,
			ReasoningEffort: params.ReasoningEffort,
			PermissionMode:  params.PermissionMode,
			Attachments:     attachments,
		})
	case ControlActionInterrupt:
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.interruptSession(params.SessionID)
	case ControlActionInteractionRespond:
		var params struct {
			InteractionID string         `json:"interaction_id"`
			Response      map[string]any `json:"response"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.respondInteraction(params.InteractionID, params.Response)
	case ControlActionSessionEdit:
		var params struct {
			SessionID       string `json:"session_id"`
			EventSeq        int64  `json:"event_seq"`
			Input           string `json:"input"`
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoning_effort"`
			PermissionMode  string `json:"permission_mode"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.editLastUserMessage(params.SessionID, editLastUserMessageRequest{
			EventSeq:        params.EventSeq,
			Input:           params.Input,
			Model:           params.Model,
			ReasoningEffort: params.ReasoningEffort,
			PermissionMode:  params.PermissionMode,
		})
	case ControlActionAttachmentIngest:
		var params attachmentIngestParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.ingestControlAttachment(params)
	case ControlActionMediaRead:
		var params mediaReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.readControlMedia(params, false)
	case ControlActionMediaDownload:
		var params mediaReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.readControlMedia(params, true)
	case ControlActionTerminalOpen:
		var params terminalOpenParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().open(context.Background(), req.ControllerDeviceID, params)
	case ControlActionTerminalAttach:
		var params terminalAttachParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().attach(req.ControllerDeviceID, conn, params)
	case ControlActionTerminalDetach:
		var params terminalDetachParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().detach(req.ControllerDeviceID, conn, params)
	case ControlActionTerminalInput:
		var params terminalInputParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().input(context.Background(), req.ControllerDeviceID, params)
	case ControlActionTerminalResize:
		var params terminalResizeParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().resize(context.Background(), req.ControllerDeviceID, params)
	case ControlActionTerminalClose:
		var params terminalCloseParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.terminalManager().close(context.Background(), req.ControllerDeviceID, params)
	default:
		return nil, newActionError(http.StatusNotFound, "control_action_unknown", "control action not found")
	}
}

func decodeControlParams(params map[string]any, target any) error {
	body, err := json.Marshal(params)
	if err != nil {
		return newActionError(http.StatusBadRequest, "invalid_params", err.Error())
	}
	if err := json.Unmarshal(body, target); err != nil {
		return newActionError(http.StatusBadRequest, "invalid_params", err.Error())
	}
	return nil
}
