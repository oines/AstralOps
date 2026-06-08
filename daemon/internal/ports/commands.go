package ports

import (
	"context"
	"net/http"

	"github.com/oines/astralops/pkg/protocol"
)

type SessionCommands interface {
	CreateSession(context.Context, protocol.CreateSessionRequest) (protocol.Session, error)
	ReadSessions(context.Context, protocol.SessionsReadParams) ([]protocol.Session, error)
	ReadSessionView(context.Context, protocol.SessionReferenceParams) (protocol.SessionView, error)
	StartInput(context.Context, SessionInputParams) (protocol.SessionInputResult, error)
	CancelQueue(context.Context, protocol.QueueControlParams) (protocol.QueueControlResult, error)
	SteerQueue(context.Context, protocol.QueueControlParams) (protocol.QueueControlResult, error)
	CancelTurn(context.Context, protocol.SessionReferenceParams) (protocol.OkResult, error)
	RespondInteraction(context.Context, protocol.InteractionRespondParams) (protocol.OkResult, error)
	ForkSession(context.Context, protocol.SessionForkControlParams) (protocol.ForkSessionResponse, error)
	EditLastUserMessage(context.Context, protocol.SessionEditParams) (protocol.OkResult, error)
	DeleteSession(context.Context, protocol.SessionDeleteParams) (protocol.SessionDeleteResult, error)
	ListCommands(context.Context, protocol.SessionReferenceParams) (protocol.SessionCommandListResponse, error)
	RunCommand(context.Context, SessionCommandRunParams) (protocol.SessionCommandResponse, error)
}

type SessionInputParams struct {
	SessionID       string
	Input           string
	Attachments     []protocol.InputAttachment
	Model           string
	ReasoningEffort string
	PermissionMode  string
}

type SessionCommandRunParams struct {
	SessionID string
	CommandID string
	Request   protocol.SessionCommandRequest
}

type WorkspaceCommands interface {
	CreateWorkspace(context.Context, protocol.CreateWorkspaceRequest) (protocol.Workspace, error)
	ReadWorkspaces(context.Context) ([]protocol.Workspace, error)
	ReadWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.Workspace, error)
	DeleteWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.OkResult, error)
	ListNativeSessions(context.Context, protocol.NativeSessionsReadParams) (protocol.NativeSessionListResponse, error)
	ImportNativeSession(context.Context, protocol.NativeSessionImportParams) (protocol.NativeSessionImportResponse, error)
	BrowseHostFileSystem(context.Context, protocol.HostFileSystemBrowseParams) (protocol.HostFileSystemBrowseResult, error)
	ReadLegacyWorkspaceFiles(context.Context, LegacyWorkspaceFilesParams) (LegacyWorkspaceFilesResult, error)
	ExecLegacyWorkspaceCommand(context.Context, LegacyWorkspaceExecParams) (map[string]any, error)
	ReadWorkspaceConnection(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error)
	ConnectWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error)
	DisconnectWorkspace(context.Context, protocol.WorkspaceReferenceParams) (protocol.WorkspaceConnection, error)
	ReadFiles(context.Context, protocol.WorkspaceFilesReadParams) (protocol.WorkspaceFilesReadResult, error)
	WriteFiles(context.Context, protocol.WorkspaceFilesWriteParams) (protocol.WorkspaceFilesWriteResult, error)
	ApplyPatch(context.Context, protocol.WorkspaceFilesApplyPatchParams) (protocol.WorkspaceFilesApplyPatchResult, error)
	DeleteFiles(context.Context, protocol.WorkspaceFilesDeleteParams) (protocol.WorkspaceFilesDeleteResult, error)
	MoveFiles(context.Context, protocol.WorkspaceFilesMoveParams) (protocol.WorkspaceFilesMoveResult, error)
	StreamFile(context.Context, protocol.WorkspaceFilesStreamParams) (protocol.WorkspaceFileStreamResult, error)
	CancelFileStream(context.Context, protocol.WorkspaceFileStreamCancelParams) (protocol.WorkspaceFileStreamCancelResult, error)
	Exec(context.Context, protocol.WorkspaceExecParams) (protocol.WorkspaceExecResult, error)
}

type LegacyWorkspaceFilesParams struct {
	WorkspaceID string
	Path        string
}

type LegacyWorkspaceFilesResult struct {
	Path    string                     `json:"path"`
	Root    string                     `json:"root"`
	Entries []LegacyWorkspaceFileEntry `json:"entries"`
}

type LegacyWorkspaceFileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Size    int64  `json:"size,omitempty"`
	ModTime string `json:"mod_time,omitempty"`
}

type LegacyWorkspaceExecParams struct {
	WorkspaceID string
	Command     string
}

type TerminalCommands interface {
	ListTerminals(context.Context) ([]protocol.TerminalTab, error)
	OpenTerminal(context.Context, protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error)
	AttachTerminal(context.Context, protocol.TerminalAttachParams) (protocol.TerminalAttachResult, error)
	DetachTerminal(context.Context, protocol.TerminalDetachParams) (protocol.TerminalAckResult, error)
	InputTerminal(context.Context, protocol.TerminalInputParams) (protocol.TerminalAckResult, error)
	ResizeTerminal(context.Context, protocol.TerminalResizeParams) (protocol.TerminalAckResult, error)
	CloseTerminal(context.Context, protocol.TerminalCloseParams) (protocol.TerminalAckResult, error)
	AckTerminalHeartbeat(context.Context, protocol.TerminalHeartbeatAckParams) (protocol.TerminalAckResult, error)
	OpenLegacyWorkspaceTerminal(context.Context, protocol.WorkspaceReferenceParams) (protocol.TerminalOpenResult, error)
	CloseLegacyWorkspaceTerminal(context.Context, LegacyWorkspaceTerminalCloseParams) (protocol.TerminalAckResult, error)
}

type LegacyWorkspaceTerminalCloseParams struct {
	WorkspaceID string
	TerminalID  string
}

type LegacyWorkspacePassthrough interface {
	ServeWorkspacePTY(http.ResponseWriter, *http.Request, string)
	ServeClaudeRemoteTool(http.ResponseWriter, *http.Request, string)
}

type MediaCommands interface {
	IngestAttachment(context.Context, protocol.AttachmentIngestParams) (protocol.AttachmentIngestResult, error)
	StartAttachmentIngest(context.Context, protocol.AttachmentIngestStartParams) (protocol.AttachmentIngestStartResult, error)
	AppendAttachmentChunk(context.Context, protocol.AttachmentIngestChunkParams) (protocol.AttachmentIngestChunkResult, error)
	FinishAttachmentIngest(context.Context, protocol.AttachmentIngestFinishParams) (protocol.AttachmentIngestFinishResult, error)
	ReadMedia(context.Context, protocol.MediaReadParams) (protocol.MediaReadResult, error)
	ResolveSessionMedia(context.Context, SessionMediaParams) (SessionMedia, error)
	StreamMedia(context.Context, protocol.MediaStreamParams) (protocol.MediaStreamResult, error)
	CancelMediaStream(context.Context, protocol.MediaStreamCancelParams) (protocol.MediaStreamCancelResult, error)
}

type SessionMediaParams struct {
	SessionID string
	EventSeq  int64
	MediaID   string
}

type SessionMedia struct {
	Path     string
	Name     string
	MIMEType string
}

type CloudCommands interface {
	MeshState(context.Context) (any, error)
	ApplySettings(context.Context, any) (any, error)
	Logout(context.Context) (any, error)
	ResolvePairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error)
	RevokeTrust(context.Context, protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error)
	ServeCloudAuthAction(http.ResponseWriter, *http.Request)
	ServeCloudAccount(http.ResponseWriter, *http.Request)
	ServeCloudAccountRelay(http.ResponseWriter, *http.Request)
	ServeCloudRelays(http.ResponseWriter, *http.Request)
	ServeCloudDevices(http.ResponseWriter, *http.Request)
	ServeCloudDeviceAction(http.ResponseWriter, *http.Request)
	ServeCloudHeartbeat(http.ResponseWriter, *http.Request)
	ServeCloudPairingRequests(http.ResponseWriter, *http.Request)
	ServeCloudPairingRequestAction(http.ResponseWriter, *http.Request)
}

type PairingCommands interface {
	ListPairingRequests(context.Context) (protocol.PairingRequestListResult, error)
	SubmitPairingRequest(context.Context, PairingRequestInput) (PairingRequestSubmitResult, error)
	ReadPairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error)
	ApprovePairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error)
	DenyPairingRequest(context.Context, protocol.PairingRequestResolveParams) (protocol.PairingRequestResolveResult, error)
}

type PairingRequestInput struct {
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerDeviceKind           string   `json:"controller_device_kind,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope,omitempty"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
}

type PairingRequestSubmitResult struct {
	Request protocol.PairingRequest `json:"request"`
}

type TrustCommands interface {
	ListTrustedDevices(context.Context) (protocol.HostTrustListResult, error)
	TrustDevice(context.Context, TrustDeviceRequest) (protocol.TrustGrant, error)
	RevokeTrustedDevice(context.Context, protocol.HostTrustRevokeParams) (protocol.HostTrustRevokeResult, error)
}

type TrustDeviceRequest struct {
	ControllerDeviceID             string   `json:"controller_device_id"`
	ControllerDeviceName           string   `json:"controller_device_name,omitempty"`
	ControllerPublicKey            string   `json:"controller_public_key,omitempty"`
	ControllerPublicKeyFingerprint string   `json:"controller_public_key_fingerprint,omitempty"`
	Scope                          string   `json:"scope,omitempty"`
	Capabilities                   []string `json:"capabilities,omitempty"`
	WorkspaceExecPolicy            string   `json:"workspace_exec_policy,omitempty"`
}

type MeshCommands interface {
	ReadMeshState(context.Context, MeshStateParams) (any, error)
	ServeMeshStateStream(http.ResponseWriter, *http.Request)
}

type MeshStateParams struct {
	Discover bool
}

type RemoteHostCommands interface {
	ListRemoteHosts(context.Context, RemoteHostsListParams) (any, error)
	ServeRemoteHostAction(http.ResponseWriter, *http.Request)
}

type RemoteHostsListParams struct {
	Discover bool
}

type ControlGateway interface {
	Dispatch(context.Context, protocol.ControlRequest) (protocol.ControlResponse, error)
}

type EventCommands interface {
	QueryEvents(context.Context, protocol.EventWindowParams) ([]protocol.AstralEvent, error)
	ReplayEvents(context.Context, EventStreamParams) ([]protocol.AstralEvent, error)
	Subscribe(context.Context) (EventSubscription, error)
}

type EventStreamParams struct {
	WorkspaceID string
	SessionID   string
	AfterSeq    int64
}

type EventSubscription interface {
	Events() <-chan protocol.AstralEvent
	Close()
}
