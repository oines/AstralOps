package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/oines/astralops/pkg/protocol"
)

const (
	CapabilityCoreRead             = protocol.CapabilityCoreRead
	CapabilityCoreControl          = protocol.CapabilityCoreControl
	CapabilityInteractionRespond   = protocol.CapabilityInteractionRespond
	CapabilitySessionEdit          = protocol.CapabilitySessionEdit
	CapabilityAttachmentIngest     = protocol.CapabilityAttachmentIngest
	CapabilityMediaRead            = protocol.CapabilityMediaRead
	CapabilityMediaDownload        = protocol.CapabilityMediaDownload
	CapabilityMediaStream          = protocol.CapabilityMediaStream
	CapabilityWorkspaceFilesRead   = protocol.CapabilityWorkspaceFilesRead
	CapabilityWorkspaceFilesWrite  = protocol.CapabilityWorkspaceFilesWrite
	CapabilityWorkspaceExec        = protocol.CapabilityWorkspaceExec
	CapabilityTerminalOpen         = protocol.CapabilityTerminalOpen
	CapabilityTerminalInput        = protocol.CapabilityTerminalInput
	CapabilityHostFileSystemBrowse = protocol.CapabilityHostFileSystemBrowse
	CapabilityHostManage           = protocol.CapabilityHostManage
)

const (
	ControlActionHostSnapshot               = protocol.ControlActionHostSnapshot
	ControlActionWorkbench                  = protocol.ControlActionWorkbench
	ControlActionPing                       = protocol.ControlActionPing
	ControlActionSessionView                = protocol.ControlActionSessionView
	ControlActionSessions                   = protocol.ControlActionSessions
	ControlActionNativeSessions             = protocol.ControlActionNativeSessions
	ControlActionWorkspaces                 = protocol.ControlActionWorkspaces
	ControlActionWorkspaceConnection        = protocol.ControlActionWorkspaceConnection
	ControlActionEvents                     = protocol.ControlActionEvents
	ControlActionEventsSubscribe            = protocol.ControlActionEventsSubscribe
	ControlActionEventsUnsubscribe          = protocol.ControlActionEventsUnsubscribe
	ControlActionSessionInput               = protocol.ControlActionSessionInput
	ControlActionInterrupt                  = protocol.ControlActionInterrupt
	ControlActionQueueCancel                = protocol.ControlActionQueueCancel
	ControlActionQueueSteer                 = protocol.ControlActionQueueSteer
	ControlActionWorkspaceCreate            = protocol.ControlActionWorkspaceCreate
	ControlActionWorkspaceConnect           = protocol.ControlActionWorkspaceConnect
	ControlActionWorkspaceDisconnect        = protocol.ControlActionWorkspaceDisconnect
	ControlActionWorkspaceDelete            = protocol.ControlActionWorkspaceDelete
	ControlActionSessionCreate              = protocol.ControlActionSessionCreate
	ControlActionSessionFork                = protocol.ControlActionSessionFork
	ControlActionSessionDelete              = protocol.ControlActionSessionDelete
	ControlActionNativeSessionImport        = protocol.ControlActionNativeSessionImport
	ControlActionInteractionRespond         = protocol.ControlActionInteractionRespond
	ControlActionSessionEdit                = protocol.ControlActionSessionEdit
	ControlActionAttachmentIngest           = protocol.ControlActionAttachmentIngest
	ControlActionAttachmentIngestStart      = protocol.ControlActionAttachmentIngestStart
	ControlActionAttachmentIngestChunk      = protocol.ControlActionAttachmentIngestChunk
	ControlActionAttachmentIngestFinish     = protocol.ControlActionAttachmentIngestFinish
	ControlActionMediaRead                  = protocol.ControlActionMediaRead
	ControlActionMediaDownload              = protocol.ControlActionMediaDownload
	ControlActionMediaStream                = protocol.ControlActionMediaStream
	ControlActionMediaStreamCancel          = protocol.ControlActionMediaStreamCancel
	ControlActionWorkspaceFilesRead         = protocol.ControlActionWorkspaceFilesRead
	ControlActionWorkspaceFilesWrite        = protocol.ControlActionWorkspaceFilesWrite
	ControlActionWorkspaceFilesApplyPatch   = protocol.ControlActionWorkspaceFilesApplyPatch
	ControlActionWorkspaceFilesDelete       = protocol.ControlActionWorkspaceFilesDelete
	ControlActionWorkspaceFilesMove         = protocol.ControlActionWorkspaceFilesMove
	ControlActionWorkspaceFilesStream       = protocol.ControlActionWorkspaceFilesStream
	ControlActionWorkspaceFilesStreamCancel = protocol.ControlActionWorkspaceFilesStreamCancel
	ControlActionWorkspaceExec              = protocol.ControlActionWorkspaceExec
	ControlActionTerminalOpen               = protocol.ControlActionTerminalOpen
	ControlActionTerminalList               = protocol.ControlActionTerminalList
	ControlActionTerminalAttach             = protocol.ControlActionTerminalAttach
	ControlActionTerminalDetach             = protocol.ControlActionTerminalDetach
	ControlActionTerminalHeartbeatAck       = protocol.ControlActionTerminalHeartbeatAck
	ControlActionTerminalInput              = protocol.ControlActionTerminalInput
	ControlActionTerminalResize             = protocol.ControlActionTerminalResize
	ControlActionTerminalClose              = protocol.ControlActionTerminalClose
	ControlActionHostFileSystemBrowse       = protocol.ControlActionHostFileSystemBrowse
	ControlActionHostTrustList              = protocol.ControlActionHostTrustList
	ControlActionHostTrustRevoke            = protocol.ControlActionHostTrustRevoke
	ControlActionHostPairingList            = protocol.ControlActionHostPairingList
	ControlActionHostPairingApprove         = protocol.ControlActionHostPairingApprove
	ControlActionHostPairingDeny            = protocol.ControlActionHostPairingDeny
)

func (s *remoteControlService) executeControlRequest(req ControlRequest) (ControlResponse, error) {
	return s.executeControlRequestWithConnection(req, nil)
}

func (s *remoteControlService) executeControlRequestWithConnection(req ControlRequest, conn controlConnection) (ControlResponse, error) {
	ctx := context.Background()
	if conn != nil {
		ctx = conn.requestContext()
	}
	return s.executeControlRequestWithContext(ctx, req, conn)
}

func (s *remoteControlService) executeControlRequestWithContext(ctx context.Context, req ControlRequest, conn controlConnection) (response ControlResponse, err error) {
	startedAt := logControlActionStart(req)
	defer func() {
		if err != nil {
			logControlActionFailed(req, startedAt, err)
			return
		}
		logControlActionCompleted(req, startedAt)
	}()
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
	grant, ok := s.store.trustedControlGrant(req.ControllerDeviceID)
	if !ok || !trustGrantAllows(grant, string(requiredCapability)) {
		return ControlResponse{RequestID: req.RequestID}, newActionError(http.StatusForbidden, "capability_denied", "controller is not allowed to use capability")
	}

	return s.dispatchAuthorizedControlRequest(ctx, req, conn, grant)
}

func (s *remoteControlService) executeAuthorizedControlRequestWithContext(ctx context.Context, req ControlRequest, conn controlConnection, grant TrustGrant) (response ControlResponse, err error) {
	startedAt := logControlActionStart(req)
	defer func() {
		if err != nil {
			logControlActionFailed(req, startedAt, err)
			return
		}
		logControlActionCompleted(req, startedAt)
	}()
	return s.dispatchAuthorizedControlRequest(ctx, req, conn, grant)
}

func (s *remoteControlService) dispatchAuthorizedControlRequest(ctx context.Context, req ControlRequest, conn controlConnection, grant TrustGrant) (ControlResponse, error) {
	result, err := s.dispatchControlAction(ctx, req, conn, grant)
	if err != nil {
		return ControlResponse{RequestID: req.RequestID}, err
	}
	return ControlResponse{RequestID: req.RequestID, OK: true, Result: result}, nil
}

func controlActionCapability(action ControlAction) ControlCapability {
	return protocol.RequiredCapability(action)
}

func (s *remoteControlService) dispatchControlAction(ctx context.Context, req ControlRequest, conn controlConnection, grant TrustGrant) (any, error) {
	switch req.Action {
	case ControlActionPing:
		return map[string]any{
			"ok":             true,
			"ts":             time.Now().UTC().Format(time.RFC3339Nano),
			"host_device_id": s.store.hostInfo().Identity.DeviceID,
		}, nil
	case ControlActionHostSnapshot:
		var params hostSnapshotParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.buildHostSnapshot(params), nil
	case ControlActionWorkbench:
		return s.buildWorkbenchState(), nil
	case ControlActionSessionView:
		var params struct {
			SessionID string `json:"session_id"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		view, ok := s.buildSessionView(params.SessionID)
		if !ok {
			return nil, newActionError(http.StatusNotFound, "session_not_found", "session not found")
		}
		workspace, _ := s.store.getWorkspace(view.Session.WorkspaceID)
		return sanitizeControlSessionView(view, workspace), nil
	case ControlActionSessions:
		var params struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return sanitizeControlSessions(s.store.listSessions(params.WorkspaceID)), nil
	case ControlActionNativeSessions:
		var params protocol.NativeSessionsReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		if _, ok := s.store.getWorkspace(params.WorkspaceID); !ok {
			return nil, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
		}
		return protocol.NativeSessionListResponse{
			Sessions: sanitizeControlSessions(s.store.listNativeSessionCandidates(params.WorkspaceID)),
		}, nil
	case ControlActionWorkspaces:
		return sanitizeControlWorkspaces(s.store.listWorkspaces()), nil
	case ControlActionWorkspaceConnection:
		var params workspaceReferenceParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		workspace, err := s.workspaceService().controlWorkspace(params.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return sanitizeControlWorkspaceConnection(s.sshService().Connection(ctx, workspace)), nil
	case ControlActionEvents:
		var params struct {
			WorkspaceID string `json:"workspace_id"`
			SessionID   string `json:"session_id"`
			AfterSeq    int64  `json:"after_seq"`
			BeforeSeq   int64  `json:"before_seq"`
			Limit       int    `json:"limit"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return sanitizeControlEvents(s.queryEventsWindow(params.WorkspaceID, params.SessionID, params.AfterSeq, params.BeforeSeq, params.Limit)), nil
	case ControlActionEventsSubscribe:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "core.subscribe.events requires an encrypted control connection")
		}
		var params eventSubscriptionParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.prepareControlEventSubscription(params)
	case ControlActionEventsUnsubscribe:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "core.unsubscribe.events requires an encrypted control connection")
		}
		var params eventSubscriptionCancelParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		streamID := strings.TrimSpace(params.StreamID)
		if streamID == "" {
			return nil, newActionError(http.StatusBadRequest, "event_subscription_id_required", "stream_id required")
		}
		return eventSubscriptionCancelResult{StreamID: streamID, Cancelled: conn.cancelControlStream(streamID)}, nil
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
		attachments, err := s.mediaService().resolveControlInputAttachments(params.SessionID, params.Attachments)
		if err != nil {
			return nil, err
		}
		return s.sessions().startSessionInput(params.SessionID, params.Input, TurnOptions{
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
		return s.sessions().interruptSession(params.SessionID)
	case ControlActionQueueCancel:
		var params queueControlParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.sessions().cancelControlQueuedTurn(params)
	case ControlActionQueueSteer:
		var params queueControlParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.sessions().steerControlQueuedTurn(params)
	case ControlActionWorkspaceCreate:
		var params createWorkspaceRequest
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		workspace, err := s.createWorkspace(params)
		if err != nil {
			return nil, err
		}
		return sanitizeControlWorkspace(workspace), nil
	case ControlActionWorkspaceConnect:
		var params workspaceReferenceParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		workspace, err := s.workspaceService().controlWorkspace(params.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return s.sshService().Connect(ctx, workspace)
	case ControlActionWorkspaceDisconnect:
		var params workspaceReferenceParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		workspace, err := s.workspaceService().controlWorkspace(params.WorkspaceID)
		if err != nil {
			return nil, err
		}
		return s.sshService().Disconnect(ctx, workspace), nil
	case ControlActionWorkspaceDelete:
		var params workspaceReferenceParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.deleteWorkspace(params.WorkspaceID)
	case ControlActionSessionCreate:
		var params createSessionRequest
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		session, err := s.sessions().createSession(params)
		if err != nil {
			return nil, err
		}
		return sanitizeControlSession(session), nil
	case ControlActionSessionFork:
		var params sessionForkControlParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.sessions().forkSession(params.SessionID, forkSessionRequest{EventSeq: params.EventSeq})
	case ControlActionSessionDelete:
		var params sessionDeleteParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.sessions().deleteSessionByID(params.SessionID)
	case ControlActionNativeSessionImport:
		var params protocol.NativeSessionImportParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		if _, ok := s.store.getWorkspace(params.WorkspaceID); !ok {
			return nil, newActionError(http.StatusNotFound, "workspace_not_found", "workspace not found")
		}
		session, err := s.store.importNativeSession(params.WorkspaceID, params.SessionID)
		if err != nil {
			return nil, newActionError(http.StatusNotFound, "native_session_not_found", err.Error())
		}
		s.emit(AstralEvent{WorkspaceID: session.WorkspaceID, SessionID: session.ID, Agent: session.Agent, Kind: "session.started", Normalized: eventNormalized("session.started", session)})
		return protocol.NativeSessionImportResponse{Session: sanitizeControlSession(session)}, nil
	case ControlActionInteractionRespond:
		var params struct {
			InteractionID string         `json:"interaction_id"`
			Response      map[string]any `json:"response"`
		}
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.sessions().respondInteraction(params.InteractionID, params.Response)
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
		return s.sessions().editLastUserMessage(params.SessionID, editLastUserMessageRequest{
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
		return s.mediaService().ingestControlAttachment(params)
	case ControlActionAttachmentIngestStart:
		var params attachmentIngestStartParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().startControlAttachmentIngest(params)
	case ControlActionAttachmentIngestChunk:
		var params attachmentIngestChunkParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().appendControlAttachmentChunk(params)
	case ControlActionAttachmentIngestFinish:
		var params attachmentIngestFinishParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().finishControlAttachmentIngest(params)
	case ControlActionMediaRead:
		var params mediaReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().readControlMedia(params, false)
	case ControlActionMediaDownload:
		var params mediaReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().readControlMedia(params, true)
	case ControlActionMediaStream:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "media.stream requires an encrypted control connection")
		}
		var params mediaStreamParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.mediaService().prepareControlMediaStream(params)
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
		return mediaStreamCancelResult{StreamID: streamID, Cancelled: conn.cancelControlStream(streamID)}, nil
	case ControlActionWorkspaceFilesRead:
		var params workspaceFilesReadParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().readControlWorkspaceFiles(ctx, params)
	case ControlActionWorkspaceFilesWrite:
		var params workspaceFilesWriteParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().writeControlWorkspaceFile(ctx, params)
	case ControlActionWorkspaceFilesApplyPatch:
		var params workspaceFilesApplyPatchParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().applyControlWorkspacePatch(ctx, params)
	case ControlActionWorkspaceFilesDelete:
		var params workspaceFilesDeleteParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().deleteControlWorkspacePath(ctx, params)
	case ControlActionWorkspaceFilesMove:
		var params workspaceFilesMoveParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().moveControlWorkspacePath(ctx, params)
	case ControlActionWorkspaceFilesStream:
		if conn == nil {
			return nil, newActionError(http.StatusBadRequest, "control_connection_required", "workspace.files.stream requires an encrypted control connection")
		}
		var params workspaceFilesStreamParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().prepareControlWorkspaceFileStream(ctx, params)
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
		return workspaceFileStreamCancelResult{StreamID: streamID, Cancelled: conn.cancelControlStream(streamID)}, nil
	case ControlActionWorkspaceExec:
		var params workspaceExecParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.workspaceService().executeControlWorkspaceCommand(ctx, params, grant)
	case ControlActionTerminalOpen:
		var params terminalOpenParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().OpenForController(ctx, req.ControllerDeviceID, params)
	case ControlActionTerminalList:
		return s.terminalService().List(ctx)
	case ControlActionTerminalAttach:
		var params terminalAttachParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().AttachForController(ctx, req.ControllerDeviceID, conn, params)
	case ControlActionTerminalDetach:
		var params terminalDetachParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().DetachForController(ctx, req.ControllerDeviceID, conn, params)
	case ControlActionTerminalHeartbeatAck:
		var params terminalHeartbeatAckParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().HeartbeatAckForController(ctx, req.ControllerDeviceID, params)
	case ControlActionTerminalInput:
		var params terminalInputParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().InputForController(ctx, req.ControllerDeviceID, params)
	case ControlActionTerminalResize:
		var params terminalResizeParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().ResizeForController(ctx, req.ControllerDeviceID, params)
	case ControlActionTerminalClose:
		var params terminalCloseParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.terminalService().CloseForController(ctx, req.ControllerDeviceID, params)
	case ControlActionHostFileSystemBrowse:
		var params hostFileSystemBrowseParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.browseHostFileSystem(ctx, params)
	case ControlActionHostTrustList:
		return hostTrustListResult{Grants: s.store.listTrustGrants()}, nil
	case ControlActionHostTrustRevoke:
		var params hostTrustRevokeParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		exceptConnectionID := ""
		if conn != nil {
			exceptConnectionID = conn.connectionID()
		}
		return s.revokeTrustedControlDevice(params.ControllerDeviceID, exceptConnectionID)
	case ControlActionHostPairingList:
		return pairingRequestListResult{Requests: s.store.listPairingRequests()}, nil
	case ControlActionHostPairingApprove:
		var params pairingRequestResolveParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.approvePairingRequest(params.RequestID)
	case ControlActionHostPairingDeny:
		var params pairingRequestResolveParams
		if err := decodeControlParams(req.Params, &params); err != nil {
			return nil, err
		}
		return s.denyPairingRequest(params.RequestID)
	default:
		return nil, newActionError(http.StatusNotFound, "control_action_unknown", "control action not found")
	}
}

func decodeControlParams(params json.RawMessage, target any) error {
	return protocol.DecodeControlParamsInto("", params, target)
}
