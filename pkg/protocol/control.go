package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type ControlCapability string

const (
	CapabilityCoreRead             ControlCapability = "core.read"
	CapabilityCoreControl          ControlCapability = "core.control"
	CapabilityInteractionRespond   ControlCapability = "interaction.respond"
	CapabilitySessionEdit          ControlCapability = "session.edit"
	CapabilityAttachmentIngest     ControlCapability = "attachment.ingest"
	CapabilityMediaRead            ControlCapability = "media.read"
	CapabilityMediaDownload        ControlCapability = "media.download"
	CapabilityMediaStream          ControlCapability = "media.stream"
	CapabilityWorkspaceFilesRead   ControlCapability = "workspace.files.read"
	CapabilityWorkspaceFilesWrite  ControlCapability = "workspace.files.write"
	CapabilityWorkspaceExec        ControlCapability = "workspace.exec"
	CapabilityTerminalOpen         ControlCapability = "terminal.open"
	CapabilityTerminalInput        ControlCapability = "terminal.input"
	CapabilityHostFileSystemBrowse ControlCapability = "host.fs.browse"
	CapabilityHostManage           ControlCapability = "host.manage"
)

type ControlAction string

const (
	ControlActionHostSnapshot               ControlAction = "core.read.host_snapshot"
	ControlActionWorkbench                  ControlAction = "core.read.workbench"
	ControlActionPing                       ControlAction = "core.read.ping"
	ControlActionSessionView                ControlAction = "core.read.session_view"
	ControlActionSessions                   ControlAction = "core.read.sessions"
	ControlActionNativeSessions             ControlAction = "core.read.native_sessions"
	ControlActionWorkspaces                 ControlAction = "core.read.workspaces"
	ControlActionWorkspaceConnection        ControlAction = "core.read.workspace.connection"
	ControlActionEvents                     ControlAction = "core.read.events"
	ControlActionEventsSubscribe            ControlAction = "core.subscribe.events"
	ControlActionEventsUnsubscribe          ControlAction = "core.unsubscribe.events"
	ControlActionSessionInput               ControlAction = "core.control.session_input"
	ControlActionInterrupt                  ControlAction = "core.control.interrupt"
	ControlActionQueueCancel                ControlAction = "core.control.queue.cancel"
	ControlActionQueueSteer                 ControlAction = "core.control.queue.steer"
	ControlActionWorkspaceCreate            ControlAction = "core.control.workspace.create"
	ControlActionWorkspaceConnect           ControlAction = "core.control.workspace.connect"
	ControlActionWorkspaceDisconnect        ControlAction = "core.control.workspace.disconnect"
	ControlActionWorkspaceDelete            ControlAction = "core.control.workspace.delete"
	ControlActionSessionCreate              ControlAction = "core.control.session.create"
	ControlActionSessionFork                ControlAction = "core.control.session.fork"
	ControlActionSessionDelete              ControlAction = "core.control.session.delete"
	ControlActionNativeSessionImport        ControlAction = "core.control.native_session.import"
	ControlActionInteractionRespond         ControlAction = "interaction.respond"
	ControlActionSessionEdit                ControlAction = "session.edit"
	ControlActionAttachmentIngest           ControlAction = "attachment.ingest"
	ControlActionAttachmentIngestStart      ControlAction = "attachment.ingest.start"
	ControlActionAttachmentIngestChunk      ControlAction = "attachment.ingest.chunk"
	ControlActionAttachmentIngestFinish     ControlAction = "attachment.ingest.finish"
	ControlActionMediaRead                  ControlAction = "media.read"
	ControlActionMediaDownload              ControlAction = "media.download"
	ControlActionMediaStream                ControlAction = "media.stream"
	ControlActionMediaStreamCancel          ControlAction = "media.stream.cancel"
	ControlActionWorkspaceFilesRead         ControlAction = "workspace.files.read"
	ControlActionWorkspaceFilesWrite        ControlAction = "workspace.files.write"
	ControlActionWorkspaceFilesApplyPatch   ControlAction = "workspace.files.apply_patch"
	ControlActionWorkspaceFilesDelete       ControlAction = "workspace.files.delete"
	ControlActionWorkspaceFilesMove         ControlAction = "workspace.files.move"
	ControlActionWorkspaceFilesStream       ControlAction = "workspace.files.stream"
	ControlActionWorkspaceFilesStreamCancel ControlAction = "workspace.files.stream.cancel"
	ControlActionWorkspaceExec              ControlAction = "workspace.exec"
	ControlActionTerminalOpen               ControlAction = "terminal.open"
	ControlActionTerminalList               ControlAction = "terminal.list"
	ControlActionTerminalAttach             ControlAction = "terminal.attach"
	ControlActionTerminalDetach             ControlAction = "terminal.detach"
	ControlActionTerminalHeartbeatAck       ControlAction = "terminal.heartbeat_ack"
	ControlActionTerminalInput              ControlAction = "terminal.input"
	ControlActionTerminalResize             ControlAction = "terminal.resize"
	ControlActionTerminalClose              ControlAction = "terminal.close"
	ControlActionHostFileSystemBrowse       ControlAction = "host.fs.browse"
	ControlActionHostTrustList              ControlAction = "host.trust.list"
	ControlActionHostTrustRevoke            ControlAction = "host.trust.revoke"
	ControlActionHostPairingList            ControlAction = "host.pairing.list"
	ControlActionHostPairingApprove         ControlAction = "host.pairing.approve"
	ControlActionHostPairingDeny            ControlAction = "host.pairing.deny"
)

type ControlErrorCode string

const (
	ControlErrorInvalidParams         ControlErrorCode = "invalid_params"
	ControlErrorActionUnknown         ControlErrorCode = "control_action_unknown"
	ControlErrorCapabilityMismatch    ControlErrorCode = "capability_mismatch"
	ControlErrorCapabilityDenied      ControlErrorCode = "capability_denied"
	ControlErrorAuthorizationRequired ControlErrorCode = "control_authorization_required"
	ControlErrorControllerRequired    ControlErrorCode = "controller_device_required"
	ControlErrorRemoteHostRequired    ControlErrorCode = "remote_host_required"
	ControlErrorHostUnavailable       ControlErrorCode = "host_service_unavailable"
)

type ControlActionSpec struct {
	Action     ControlAction     `json:"action"`
	Capability ControlCapability `json:"capability"`
	ParamsType string            `json:"params_type,omitempty"`
	ResultType string            `json:"result_type,omitempty"`
}

var controlActionSpecs = map[ControlAction]ControlActionSpec{
	ControlActionPing:                       spec(ControlActionPing, CapabilityCoreRead, "", "OkResult"),
	ControlActionHostSnapshot:               spec(ControlActionHostSnapshot, CapabilityCoreRead, "HostSnapshotRequest", "HostSnapshotResponse"),
	ControlActionWorkbench:                  spec(ControlActionWorkbench, CapabilityCoreRead, "", "WorkbenchState"),
	ControlActionSessionView:                spec(ControlActionSessionView, CapabilityCoreRead, "SessionReferenceParams", "SessionView"),
	ControlActionSessions:                   spec(ControlActionSessions, CapabilityCoreRead, "SessionsReadParams", "Session[]"),
	ControlActionNativeSessions:             spec(ControlActionNativeSessions, CapabilityCoreRead, "NativeSessionsReadParams", "NativeSessionListResponse"),
	ControlActionWorkspaces:                 spec(ControlActionWorkspaces, CapabilityCoreRead, "", "Workspace[]"),
	ControlActionWorkspaceConnection:        spec(ControlActionWorkspaceConnection, CapabilityCoreRead, "WorkspaceReferenceParams", "WorkspaceConnection"),
	ControlActionEvents:                     spec(ControlActionEvents, CapabilityCoreRead, "EventWindowParams", "AstralEvent[]"),
	ControlActionEventsSubscribe:            spec(ControlActionEventsSubscribe, CapabilityCoreRead, "EventSubscriptionParams", "EventSubscriptionResult"),
	ControlActionEventsUnsubscribe:          spec(ControlActionEventsUnsubscribe, CapabilityCoreRead, "EventSubscriptionCancelParams", "EventSubscriptionCancelResult"),
	ControlActionSessionInput:               spec(ControlActionSessionInput, CapabilityCoreControl, "SessionInputControlParams", "SessionInputResult"),
	ControlActionInterrupt:                  spec(ControlActionInterrupt, CapabilityCoreControl, "SessionReferenceParams", "OkResult"),
	ControlActionQueueCancel:                spec(ControlActionQueueCancel, CapabilityCoreControl, "QueueControlParams", "QueueControlResult"),
	ControlActionQueueSteer:                 spec(ControlActionQueueSteer, CapabilityCoreControl, "QueueControlParams", "QueueControlResult"),
	ControlActionWorkspaceCreate:            spec(ControlActionWorkspaceCreate, CapabilityCoreControl, "CreateWorkspaceRequest", "Workspace"),
	ControlActionWorkspaceConnect:           spec(ControlActionWorkspaceConnect, CapabilityCoreControl, "WorkspaceReferenceParams", "WorkspaceConnection"),
	ControlActionWorkspaceDisconnect:        spec(ControlActionWorkspaceDisconnect, CapabilityCoreControl, "WorkspaceReferenceParams", "WorkspaceConnection"),
	ControlActionWorkspaceDelete:            spec(ControlActionWorkspaceDelete, CapabilityCoreControl, "WorkspaceReferenceParams", "OkResult"),
	ControlActionSessionCreate:              spec(ControlActionSessionCreate, CapabilityCoreControl, "CreateSessionRequest", "Session"),
	ControlActionSessionFork:                spec(ControlActionSessionFork, CapabilityCoreControl, "SessionForkControlParams", "SessionForkResponse"),
	ControlActionSessionDelete:              spec(ControlActionSessionDelete, CapabilityCoreControl, "SessionDeleteParams", "SessionDeleteResult"),
	ControlActionNativeSessionImport:        spec(ControlActionNativeSessionImport, CapabilityCoreControl, "NativeSessionImportParams", "NativeSessionImportResponse"),
	ControlActionInteractionRespond:         spec(ControlActionInteractionRespond, CapabilityInteractionRespond, "InteractionRespondParams", "OkResult"),
	ControlActionSessionEdit:                spec(ControlActionSessionEdit, CapabilitySessionEdit, "SessionEditParams", "OkResult"),
	ControlActionAttachmentIngest:           spec(ControlActionAttachmentIngest, CapabilityAttachmentIngest, "AttachmentIngestParams", "AttachmentIngestResult"),
	ControlActionAttachmentIngestStart:      spec(ControlActionAttachmentIngestStart, CapabilityAttachmentIngest, "AttachmentIngestStartParams", "AttachmentIngestStartResult"),
	ControlActionAttachmentIngestChunk:      spec(ControlActionAttachmentIngestChunk, CapabilityAttachmentIngest, "AttachmentIngestChunkParams", "AttachmentIngestChunkResult"),
	ControlActionAttachmentIngestFinish:     spec(ControlActionAttachmentIngestFinish, CapabilityAttachmentIngest, "AttachmentIngestFinishParams", "AttachmentIngestFinishResult"),
	ControlActionMediaRead:                  spec(ControlActionMediaRead, CapabilityMediaRead, "MediaReadParams", "MediaReadResult"),
	ControlActionMediaDownload:              spec(ControlActionMediaDownload, CapabilityMediaDownload, "MediaReadParams", "MediaReadResult"),
	ControlActionMediaStream:                spec(ControlActionMediaStream, CapabilityMediaStream, "MediaStreamParams", "MediaStreamResult"),
	ControlActionMediaStreamCancel:          spec(ControlActionMediaStreamCancel, CapabilityMediaStream, "MediaStreamCancelParams", "MediaStreamCancelResult"),
	ControlActionWorkspaceFilesRead:         spec(ControlActionWorkspaceFilesRead, CapabilityWorkspaceFilesRead, "WorkspaceFilesReadParams", "WorkspaceFilesReadResult"),
	ControlActionWorkspaceFilesWrite:        spec(ControlActionWorkspaceFilesWrite, CapabilityWorkspaceFilesWrite, "WorkspaceFilesWriteParams", "WorkspaceFilesWriteResult"),
	ControlActionWorkspaceFilesApplyPatch:   spec(ControlActionWorkspaceFilesApplyPatch, CapabilityWorkspaceFilesWrite, "WorkspaceFilesApplyPatchParams", "WorkspaceFilesApplyPatchResult"),
	ControlActionWorkspaceFilesDelete:       spec(ControlActionWorkspaceFilesDelete, CapabilityWorkspaceFilesWrite, "WorkspaceFilesDeleteParams", "WorkspaceFilesDeleteResult"),
	ControlActionWorkspaceFilesMove:         spec(ControlActionWorkspaceFilesMove, CapabilityWorkspaceFilesWrite, "WorkspaceFilesMoveParams", "WorkspaceFilesMoveResult"),
	ControlActionWorkspaceFilesStream:       spec(ControlActionWorkspaceFilesStream, CapabilityWorkspaceFilesRead, "WorkspaceFilesStreamParams", "WorkspaceFileStreamResult"),
	ControlActionWorkspaceFilesStreamCancel: spec(ControlActionWorkspaceFilesStreamCancel, CapabilityWorkspaceFilesRead, "WorkspaceFileStreamCancelParams", "WorkspaceFileStreamCancelResult"),
	ControlActionWorkspaceExec:              spec(ControlActionWorkspaceExec, CapabilityWorkspaceExec, "WorkspaceExecParams", "WorkspaceExecResult"),
	ControlActionTerminalOpen:               spec(ControlActionTerminalOpen, CapabilityTerminalOpen, "TerminalOpenParams", "TerminalOpenResult"),
	ControlActionTerminalList:               spec(ControlActionTerminalList, CapabilityTerminalOpen, "", "TerminalTab[]"),
	ControlActionTerminalAttach:             spec(ControlActionTerminalAttach, CapabilityTerminalOpen, "TerminalAttachParams", "TerminalAttachResult"),
	ControlActionTerminalDetach:             spec(ControlActionTerminalDetach, CapabilityTerminalOpen, "TerminalDetachParams", "TerminalAttachResult"),
	ControlActionTerminalHeartbeatAck:       spec(ControlActionTerminalHeartbeatAck, CapabilityTerminalOpen, "TerminalHeartbeatAckParams", "TerminalAckResult"),
	ControlActionTerminalInput:              spec(ControlActionTerminalInput, CapabilityTerminalInput, "TerminalInputParams", "TerminalAckResult"),
	ControlActionTerminalResize:             spec(ControlActionTerminalResize, CapabilityTerminalInput, "TerminalResizeParams", "TerminalAckResult"),
	ControlActionTerminalClose:              spec(ControlActionTerminalClose, CapabilityTerminalInput, "TerminalCloseParams", "TerminalAckResult"),
	ControlActionHostFileSystemBrowse:       spec(ControlActionHostFileSystemBrowse, CapabilityHostFileSystemBrowse, "HostFileSystemBrowseParams", "HostFileSystemBrowseResult"),
	ControlActionHostTrustList:              spec(ControlActionHostTrustList, CapabilityHostManage, "", "HostTrustListResult"),
	ControlActionHostTrustRevoke:            spec(ControlActionHostTrustRevoke, CapabilityHostManage, "HostTrustRevokeParams", "HostTrustRevokeResult"),
	ControlActionHostPairingList:            spec(ControlActionHostPairingList, CapabilityHostManage, "", "PairingRequestListResult"),
	ControlActionHostPairingApprove:         spec(ControlActionHostPairingApprove, CapabilityHostManage, "PairingRequestResolveParams", "PairingRequestResolveResult"),
	ControlActionHostPairingDeny:            spec(ControlActionHostPairingDeny, CapabilityHostManage, "PairingRequestResolveParams", "PairingRequestResolveResult"),
}

func spec(action ControlAction, capability ControlCapability, paramsType, resultType string) ControlActionSpec {
	return ControlActionSpec{Action: action, Capability: capability, ParamsType: paramsType, ResultType: resultType}
}

func ControlActionSpecs() []ControlActionSpec {
	out := make([]ControlActionSpec, 0, len(controlActionSpecs))
	for _, item := range controlActionSpecs {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Action < out[j].Action
	})
	return out
}

func ParseControlAction(value string) (ControlAction, bool) {
	action := ControlAction(strings.TrimSpace(value))
	_, ok := controlActionSpecs[action]
	return action, ok
}

func ParseControlCapability(value string) (ControlCapability, bool) {
	capability := ControlCapability(strings.TrimSpace(value))
	switch capability {
	case CapabilityCoreRead, CapabilityCoreControl, CapabilityInteractionRespond, CapabilitySessionEdit,
		CapabilityAttachmentIngest, CapabilityMediaRead, CapabilityMediaDownload, CapabilityMediaStream,
		CapabilityWorkspaceFilesRead, CapabilityWorkspaceFilesWrite, CapabilityWorkspaceExec,
		CapabilityTerminalOpen, CapabilityTerminalInput, CapabilityHostFileSystemBrowse, CapabilityHostManage:
		return capability, true
	default:
		return capability, false
	}
}

func RequiredCapability(action ControlAction) ControlCapability {
	if spec, ok := controlActionSpecs[action]; ok {
		return spec.Capability
	}
	return ""
}

func RequiredCapabilityString(action string) string {
	return string(RequiredCapability(ControlAction(strings.TrimSpace(action))))
}

type ControlRequest struct {
	RequestID          string            `json:"request_id,omitempty"`
	ControllerDeviceID string            `json:"controller_device_id,omitempty"`
	Capability         ControlCapability `json:"capability"`
	Action             ControlAction     `json:"action"`
	Params             json.RawMessage   `json:"params,omitempty"`
}

type ControlResponse struct {
	RequestID string        `json:"request_id,omitempty"`
	OK        bool          `json:"ok"`
	Result    any           `json:"result,omitempty"`
	Error     *ControlError `json:"error,omitempty"`
}

type ControlError struct {
	Status  int               `json:"status,omitempty"`
	Code    ControlErrorCode  `json:"code"`
	Message string            `json:"message"`
	Details map[string]string `json:"details,omitempty"`
}

type ActionError struct {
	Status  int
	Code    ControlErrorCode
	Message string
	Details map[string]string
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return string(e.Code)
}

func NewActionError(status int, code ControlErrorCode, message string) *ActionError {
	return &ActionError{Status: status, Code: code, Message: message}
}

func NewActionErrorString(status int, code, message string) *ActionError {
	return NewActionError(status, ControlErrorCode(code), message)
}

func ActionErrorCode(err error) string {
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return string(actionErr.Code)
	}
	return ""
}

func ActionErrorStatus(err error) int {
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return actionErr.Status
	}
	return 0
}

func ControlErrorFromError(err error) *ControlError {
	if err == nil {
		return nil
	}
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return &ControlError{
			Status:  actionErr.Status,
			Code:    actionErr.Code,
			Message: actionErr.Message,
			Details: actionErr.Details,
		}
	}
	return &ControlError{Status: http.StatusInternalServerError, Code: "internal_error", Message: err.Error()}
}

func NewControlRequest(capability ControlCapability, action ControlAction, params any) (ControlRequest, error) {
	raw, err := MarshalControlParams(params)
	if err != nil {
		return ControlRequest{}, err
	}
	req := ControlRequest{
		Capability: capability,
		Action:     action,
		Params:     raw,
	}
	if err := ValidateControlRequestAction(req); err != nil {
		return ControlRequest{}, err
	}
	if _, err := DecodeControlParams(action, raw); err != nil {
		return ControlRequest{}, err
	}
	return req, nil
}

func MarshalControlParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	switch value := params.(type) {
	case json.RawMessage:
		return normalizeControlParamsRaw(value)
	case []byte:
		return normalizeControlParamsRaw(json.RawMessage(value))
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		return normalizeControlParamsRaw(json.RawMessage(value))
	default:
		body, err := json.Marshal(params)
		if err != nil {
			return nil, NewActionError(ControlErrorInvalidParamsStatus, ControlErrorInvalidParams, err.Error())
		}
		return normalizeControlParamsRaw(body)
	}
}

func normalizeControlParamsRaw(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return nil, NewActionError(ControlErrorInvalidParamsStatus, ControlErrorInvalidParams, err.Error())
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("invalid trailing params")
		}
		return nil, NewActionError(ControlErrorInvalidParamsStatus, ControlErrorInvalidParams, err.Error())
	}
	return append(json.RawMessage(nil), raw...), nil
}

func DecodeControlParams(action ControlAction, params json.RawMessage) (any, error) {
	target := controlParamsTarget(action)
	if target == nil {
		target = &map[string]any{}
	}
	if err := DecodeControlParamsInto(action, params, target); err != nil {
		return nil, err
	}
	return dereferenceDecodedControlParams(target), nil
}

func DecodeControlParamsInto(action ControlAction, params json.RawMessage, target any) error {
	if action != "" && RequiredCapability(action) == "" {
		return NewActionError(http.StatusNotFound, ControlErrorActionUnknown, "control action not found")
	}
	body := bytes.TrimSpace(params)
	if len(body) == 0 {
		body = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return NewActionError(ControlErrorInvalidParamsStatus, ControlErrorInvalidParams, err.Error())
	}
	var trailing struct{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = errors.New("invalid trailing params")
		}
		return NewActionError(ControlErrorInvalidParamsStatus, ControlErrorInvalidParams, "invalid trailing params")
	}
	return nil
}

func controlParamsTarget(action ControlAction) any {
	switch action {
	case ControlActionPing, ControlActionWorkbench, ControlActionWorkspaces, ControlActionTerminalList, ControlActionHostTrustList, ControlActionHostPairingList:
		return &struct{}{}
	case ControlActionHostSnapshot:
		return &HostSnapshotRequest{}
	case ControlActionSessionView, ControlActionInterrupt:
		return &SessionReferenceParams{}
	case ControlActionSessions:
		return &SessionsReadParams{}
	case ControlActionNativeSessions:
		return &NativeSessionsReadParams{}
	case ControlActionWorkspaceConnection, ControlActionWorkspaceConnect, ControlActionWorkspaceDisconnect, ControlActionWorkspaceDelete:
		return &WorkspaceReferenceParams{}
	case ControlActionEvents:
		return &EventWindowParams{}
	case ControlActionEventsSubscribe:
		return &EventSubscriptionParams{}
	case ControlActionEventsUnsubscribe:
		return &EventSubscriptionCancelParams{}
	case ControlActionSessionInput:
		return &SessionInputControlParams{}
	case ControlActionQueueCancel, ControlActionQueueSteer:
		return &QueueControlParams{}
	case ControlActionWorkspaceCreate:
		return &CreateWorkspaceRequest{}
	case ControlActionSessionCreate:
		return &CreateSessionRequest{}
	case ControlActionSessionFork:
		return &SessionForkControlParams{}
	case ControlActionSessionDelete:
		return &SessionDeleteParams{}
	case ControlActionNativeSessionImport:
		return &NativeSessionImportParams{}
	case ControlActionInteractionRespond:
		return &InteractionRespondParams{}
	case ControlActionSessionEdit:
		return &SessionEditParams{}
	case ControlActionAttachmentIngest:
		return &AttachmentIngestParams{}
	case ControlActionAttachmentIngestStart:
		return &AttachmentIngestStartParams{}
	case ControlActionAttachmentIngestChunk:
		return &AttachmentIngestChunkParams{}
	case ControlActionAttachmentIngestFinish:
		return &AttachmentIngestFinishParams{}
	case ControlActionMediaRead, ControlActionMediaDownload:
		return &MediaReadParams{}
	case ControlActionMediaStream:
		return &MediaStreamParams{}
	case ControlActionMediaStreamCancel:
		return &MediaStreamCancelParams{}
	case ControlActionWorkspaceFilesRead:
		return &WorkspaceFilesReadParams{}
	case ControlActionWorkspaceFilesWrite:
		return &WorkspaceFilesWriteParams{}
	case ControlActionWorkspaceFilesApplyPatch:
		return &WorkspaceFilesApplyPatchParams{}
	case ControlActionWorkspaceFilesDelete:
		return &WorkspaceFilesDeleteParams{}
	case ControlActionWorkspaceFilesMove:
		return &WorkspaceFilesMoveParams{}
	case ControlActionWorkspaceFilesStream:
		return &WorkspaceFilesStreamParams{}
	case ControlActionWorkspaceFilesStreamCancel:
		return &WorkspaceFileStreamCancelParams{}
	case ControlActionWorkspaceExec:
		return &WorkspaceExecParams{}
	case ControlActionTerminalOpen:
		return &TerminalOpenParams{}
	case ControlActionTerminalAttach:
		return &TerminalAttachParams{}
	case ControlActionTerminalDetach:
		return &TerminalDetachParams{}
	case ControlActionTerminalHeartbeatAck:
		return &TerminalHeartbeatAckParams{}
	case ControlActionTerminalInput:
		return &TerminalInputParams{}
	case ControlActionTerminalResize:
		return &TerminalResizeParams{}
	case ControlActionTerminalClose:
		return &TerminalCloseParams{}
	case ControlActionHostFileSystemBrowse:
		return &HostFileSystemBrowseParams{}
	case ControlActionHostTrustRevoke:
		return &HostTrustRevokeParams{}
	case ControlActionHostPairingApprove, ControlActionHostPairingDeny:
		return &PairingRequestResolveParams{}
	default:
		return nil
	}
}

func dereferenceDecodedControlParams(value any) any {
	switch v := value.(type) {
	case *struct{}:
		return struct{}{}
	case *map[string]any:
		return *v
	case *HostSnapshotRequest:
		return *v
	case *SessionReferenceParams:
		return *v
	case *SessionsReadParams:
		return *v
	case *NativeSessionsReadParams:
		return *v
	case *WorkspaceReferenceParams:
		return *v
	case *EventWindowParams:
		return *v
	case *EventSubscriptionParams:
		return *v
	case *EventSubscriptionCancelParams:
		return *v
	case *SessionInputControlParams:
		return *v
	case *QueueControlParams:
		return *v
	case *CreateWorkspaceRequest:
		return *v
	case *CreateSessionRequest:
		return *v
	case *SessionForkControlParams:
		return *v
	case *SessionDeleteParams:
		return *v
	case *NativeSessionImportParams:
		return *v
	case *InteractionRespondParams:
		return *v
	case *SessionEditParams:
		return *v
	case *AttachmentIngestParams:
		return *v
	case *AttachmentIngestStartParams:
		return *v
	case *AttachmentIngestChunkParams:
		return *v
	case *AttachmentIngestFinishParams:
		return *v
	case *MediaReadParams:
		return *v
	case *MediaStreamParams:
		return *v
	case *MediaStreamCancelParams:
		return *v
	case *WorkspaceFilesReadParams:
		return *v
	case *WorkspaceFilesWriteParams:
		return *v
	case *WorkspaceFilesApplyPatchParams:
		return *v
	case *WorkspaceFilesDeleteParams:
		return *v
	case *WorkspaceFilesMoveParams:
		return *v
	case *WorkspaceFilesStreamParams:
		return *v
	case *WorkspaceFileStreamCancelParams:
		return *v
	case *WorkspaceExecParams:
		return *v
	case *TerminalOpenParams:
		return *v
	case *TerminalAttachParams:
		return *v
	case *TerminalDetachParams:
		return *v
	case *TerminalHeartbeatAckParams:
		return *v
	case *TerminalInputParams:
		return *v
	case *TerminalResizeParams:
		return *v
	case *TerminalCloseParams:
		return *v
	case *HostFileSystemBrowseParams:
		return *v
	case *HostTrustRevokeParams:
		return *v
	case *PairingRequestResolveParams:
		return *v
	default:
		return value
	}
}

const ControlErrorInvalidParamsStatus = http.StatusBadRequest

func ValidateControlRequestAction(req ControlRequest) error {
	required := RequiredCapability(req.Action)
	if required == "" {
		return NewActionError(http.StatusNotFound, ControlErrorActionUnknown, "control action not found")
	}
	if req.Capability != required {
		return NewActionError(http.StatusForbidden, ControlErrorCapabilityMismatch, "control capability does not match action")
	}
	return nil
}

func FormatControlActionMismatch(action ControlAction, capability ControlCapability) string {
	required := RequiredCapability(action)
	if required == "" {
		return "control action not found"
	}
	return fmt.Sprintf("control action %s requires capability %s, got %s", action, required, capability)
}
