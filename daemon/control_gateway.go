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
	ControlActionSessionView                = "core.read.session_view"
	ControlActionSessions                   = "core.read.sessions"
	ControlActionWorkspaces                 = "core.read.workspaces"
	ControlActionSessionInput               = "core.control.session_input"
	ControlActionInterrupt                  = "core.control.interrupt"
	ControlActionInteractionRespond         = "interaction.respond"
	ControlActionSessionEdit                = "session.edit"
	ControlActionAttachmentIngest           = "attachment.ingest"
	ControlActionAttachmentIngestStart      = "attachment.ingest.start"
	ControlActionAttachmentIngestChunk      = "attachment.ingest.chunk"
	ControlActionAttachmentIngestFinish     = "attachment.ingest.finish"
	ControlActionMediaRead                  = "media.read"
	ControlActionMediaDownload              = "media.download"
	ControlActionMediaStream                = "media.stream"
	ControlActionMediaStreamCancel          = "media.stream.cancel"
	ControlActionWorkspaceFilesRead         = "workspace.files.read"
	ControlActionWorkspaceFilesWrite        = "workspace.files.write"
	ControlActionWorkspaceFilesApplyPatch   = "workspace.files.apply_patch"
	ControlActionWorkspaceFilesDelete       = "workspace.files.delete"
	ControlActionWorkspaceFilesMove         = "workspace.files.move"
	ControlActionWorkspaceFilesStream       = "workspace.files.stream"
	ControlActionWorkspaceFilesStreamCancel = "workspace.files.stream.cancel"
	ControlActionWorkspaceExec              = "workspace.exec"
	ControlActionTerminalOpen               = "terminal.open"
	ControlActionTerminalAttach             = "terminal.attach"
	ControlActionTerminalDetach             = "terminal.detach"
	ControlActionTerminalInput              = "terminal.input"
	ControlActionTerminalResize             = "terminal.resize"
	ControlActionTerminalClose              = "terminal.close"
	ControlActionHostTrustList              = "host.trust.list"
	ControlActionHostTrustRevoke            = "host.trust.revoke"
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

	result, err := a.dispatchControlAction(req, conn, grant)
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
	case ControlActionAttachmentIngest, ControlActionAttachmentIngestStart, ControlActionAttachmentIngestChunk, ControlActionAttachmentIngestFinish:
		return CapabilityAttachmentIngest
	case ControlActionMediaRead:
		return CapabilityMediaRead
	case ControlActionMediaDownload:
		return CapabilityMediaDownload
	case ControlActionMediaStream, ControlActionMediaStreamCancel:
		return CapabilityMediaStream
	case ControlActionWorkspaceFilesRead, ControlActionWorkspaceFilesStream, ControlActionWorkspaceFilesStreamCancel:
		return CapabilityWorkspaceFilesRead
	case ControlActionWorkspaceFilesWrite, ControlActionWorkspaceFilesApplyPatch, ControlActionWorkspaceFilesDelete, ControlActionWorkspaceFilesMove:
		return CapabilityWorkspaceFilesWrite
	case ControlActionWorkspaceExec:
		return CapabilityWorkspaceExec
	case ControlActionTerminalOpen, ControlActionTerminalAttach, ControlActionTerminalDetach:
		return CapabilityTerminalOpen
	case ControlActionTerminalInput, ControlActionTerminalResize, ControlActionTerminalClose:
		return CapabilityTerminalInput
	case ControlActionHostTrustList, ControlActionHostTrustRevoke:
		return CapabilityHostManage
	default:
		return ""
	}
}

func (a *app) dispatchControlAction(req ControlRequest, conn *controlWSConn, grant TrustGrant) (any, error) {
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
	case ControlActionAttachmentIngestStart:
		var params attachmentIngestStartParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.startControlAttachmentIngest(params)
	case ControlActionAttachmentIngestChunk:
		var params attachmentIngestChunkParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.appendControlAttachmentChunk(params)
	case ControlActionAttachmentIngestFinish:
		var params attachmentIngestFinishParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.finishControlAttachmentIngest(params)
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
	case ControlActionMediaStream:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "media.stream requires an encrypted control connection")
		}
		var params mediaStreamParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.prepareControlMediaStream(params)
	case ControlActionMediaStreamCancel:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "media.stream.cancel requires an encrypted control connection")
		}
		var params mediaStreamCancelParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		streamID := strings.TrimSpace(params.StreamID)
		if streamID == "" {
			return nil, newActionError(http.StatusBadRequest, "media_stream_id_required", "stream_id required")
		}
		return mediaStreamCancelResult{StreamID: streamID, Cancelled: conn.cancelMediaStream(streamID)}, nil
	case ControlActionWorkspaceFilesRead:
		var params workspaceFilesReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.readControlWorkspaceFiles(context.Background(), params)
	case ControlActionWorkspaceFilesWrite:
		var params workspaceFilesWriteParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.writeControlWorkspaceFile(context.Background(), params)
	case ControlActionWorkspaceFilesApplyPatch:
		var params workspaceFilesApplyPatchParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.applyControlWorkspacePatch(context.Background(), params)
	case ControlActionWorkspaceFilesDelete:
		var params workspaceFilesDeleteParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.deleteControlWorkspacePath(context.Background(), params)
	case ControlActionWorkspaceFilesMove:
		var params workspaceFilesMoveParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.moveControlWorkspacePath(context.Background(), params)
	case ControlActionWorkspaceFilesStream:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "workspace.files.stream requires an encrypted control connection")
		}
		var params workspaceFilesStreamParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.prepareControlWorkspaceFileStream(context.Background(), params)
	case ControlActionWorkspaceFilesStreamCancel:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "workspace.files.stream.cancel requires an encrypted control connection")
		}
		var params workspaceFileStreamCancelParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		streamID := strings.TrimSpace(params.StreamID)
		if streamID == "" {
			return nil, newActionError(http.StatusBadRequest, "workspace_file_stream_id_required", "stream_id required")
		}
		return workspaceFileStreamCancelResult{StreamID: streamID, Cancelled: conn.cancelWorkspaceFileStream(streamID)}, nil
	case ControlActionWorkspaceExec:
		var params workspaceExecParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return a.executeControlWorkspaceCommand(context.Background(), params, grant)
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
	case ControlActionHostTrustList:
		return hostTrustListResult{Grants: a.store.listTrustGrants()}, nil
	case ControlActionHostTrustRevoke:
		var params hostTrustRevokeParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		exceptConnectionID := ""
		if conn != nil {
			exceptConnectionID = conn.id
		}
		return a.revokeTrustedControlDevice(params.ControllerDeviceID, exceptConnectionID)
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
