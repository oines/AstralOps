package controllercore

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	CapabilityCoreRead           = "core.read"
	CapabilityWorkspaceFilesRead = "workspace.files.read"
	CapabilityMediaRead          = "media.read"
	CapabilityTerminalOpen       = "terminal.open"
	ActionHostSnapshot           = "core.read.host_snapshot"
	ActionSessionView            = "core.read.session_view"
	ActionSessions               = "core.read.sessions"
	ActionWorkspaces             = "core.read.workspaces"
	ActionWorkspaceConnection    = "core.read.workspace_connection"
	ActionEvents                 = "core.read.events"
	ActionEventsSubscribe        = "core.read.events.subscribe"
	ActionEventsUnsubscribe      = "core.read.events.unsubscribe"
	ActionWorkspaceFilesRead     = "workspace.files.read"
	ActionMediaRead              = "media.read"
	ActionTerminalOpen           = "terminal.open"
	ActionTerminalList           = "terminal.list"
	ActionTerminalAttach         = "terminal.attach"
	ActionTerminalDetach         = "terminal.detach"
	ActionTerminalHeartbeatAck   = "terminal.heartbeat_ack"
	ActionTerminalInput          = "terminal.input"
	ActionTerminalResize         = "terminal.resize"
	ActionTerminalClose          = "terminal.close"
	AuthorizationRequiredCode    = "control_authorization_required"
	TerminalViewerNotReadyCode   = "terminal_viewer_not_live"
	StateIdle                    = "idle"
	StateConnecting              = "connecting"
	StateLive                    = "live"
	StateReconnecting            = "reconnecting"
	StateFailed                  = "failed"
	StateNeedsPairing            = "needs_pairing"
	StateRevoked                 = "revoked"
	WorkbenchLoading             = "loading"
	WorkbenchLive                = "live"
	WorkbenchResyncing           = "resyncing"
	WorkbenchStale               = "stale"
	WorkbenchFailed              = "failed"
	TerminalAttaching            = "attaching"
	TerminalLive                 = "live"
	TerminalResyncing            = "resyncing"
	TerminalPaused               = "paused"
	TerminalFailed               = "failed"
	TerminalClosed               = "closed"
	TransportLAN                 = "lan"
	TransportRelay               = "relay"
)

type ControlRequest struct {
	RequestID          string         `json:"request_id,omitempty"`
	ControllerDeviceID string         `json:"controller_device_id,omitempty"`
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

type ActionError struct {
	Status  int
	Code    string
	Message string
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func NewActionError(status int, code, message string) *ActionError {
	return &ActionError{Status: status, Code: code, Message: message}
}

func ErrorCode(err error) string {
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return actionErr.Code
	}
	return ""
}

func ErrorStatus(err error) int {
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return actionErr.Status
	}
	return 0
}

type ControlState struct {
	State           string `json:"state"`
	Transport       string `json:"transport,omitempty"`
	RouteGeneration int64  `json:"route_generation"`
	LastErrorCode   string `json:"last_error_code,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

type HostSessionState struct {
	HostDeviceID string                   `json:"host_device_id"`
	State        string                   `json:"state"`
	Transport    string                   `json:"transport,omitempty"`
	CanRequest   bool                     `json:"can_request"`
	Workbench    WorkbenchStatus          `json:"workbench"`
	Terminals    map[string]TerminalState `json:"terminals"`
	LastError    string                   `json:"last_error,omitempty"`
	UpdatedAt    string                   `json:"updated_at"`
}

type WorkbenchStatus struct {
	State     string `json:"state"`
	Version   int64  `json:"version,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

type TerminalState struct {
	State     string `json:"state"`
	CanInput  bool   `json:"can_input"`
	OutputSeq int64  `json:"output_seq,omitempty"`
	LastError string `json:"last_error,omitempty"`
	UpdatedAt string `json:"updated_at"`
}

type EventSubscriptionParams struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AfterSeq    int64  `json:"after_seq,omitempty"`
	ReplayLimit int    `json:"replay_limit,omitempty"`
}

type EventEnvelope struct {
	Seq   int64 `json:"seq"`
	Event any   `json:"event"`
}

type EventStream struct {
	Events <-chan EventEnvelope
	Close  func()
}

type TerminalFrame struct {
	Type     string           `json:"type"`
	Response *ControlResponse `json:"response,omitempty"`
	Terminal *TerminalPayload `json:"terminal,omitempty"`
}

type TerminalPayload struct {
	TerminalID   string `json:"terminal_id"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	Target       string `json:"target,omitempty"`
	Status       string `json:"status,omitempty"`
	OutputSeq    int64  `json:"output_seq,omitempty"`
	ViewerID     string `json:"viewer_id,omitempty"`
	InputLeaseID string `json:"input_lease_id,omitempty"`
	HeartbeatSeq int64  `json:"heartbeat_seq,omitempty"`
	Data         string `json:"data,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type TerminalStream interface {
	TerminalID() string
	ViewerID() string
	InputLeaseID() string
	Shell() string
	CWD() string
	OutputSeq() int64
	Frames() <-chan TerminalFrame
	Input(data string) error
	Resize(cols, rows int) error
	AckHeartbeat(seq int64) error
	Close() error
	Detach() error
}

type Transport interface {
	ControlState(hostDeviceID string) ControlState
	Request(ctx context.Context, hostDeviceID, capability, action string, params map[string]any) (ControlResponse, error)
	SubscribeEvents(ctx context.Context, hostDeviceID string, params EventSubscriptionParams) (EventStream, error)
	OpenTerminal(ctx context.Context, hostDeviceID, workspaceID string, afterSeq int64) (TerminalStream, error)
	AttachTerminal(ctx context.Context, hostDeviceID, terminalID string, afterSeq int64) (TerminalStream, error)
	Invalidate(hostDeviceID, reason string)
}

func FailureState(err error) string {
	code := ErrorCode(err)
	switch code {
	case AuthorizationRequiredCode:
		return StateNeedsPairing
	case "known_host_revoked", "cloud_device_revoked":
		return StateRevoked
	case "remote_host_unknown":
		return StateFailed
	}
	status := ErrorStatus(err)
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		return StateFailed
	}
	return StateReconnecting
}

func nowString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
