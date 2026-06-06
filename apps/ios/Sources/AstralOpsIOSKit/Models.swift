import Foundation

struct DeviceIdentity: Codable, Equatable {
    var deviceID: String
    var deviceName: String?
    var deviceKind: String?
    var publicKey: String?
    var publicKeyFingerprint: String
    var capabilities: [String]?
    var createdAt: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case deviceName = "device_name"
        case deviceKind = "device_kind"
        case publicKey = "public_key"
        case publicKeyFingerprint = "public_key_fingerprint"
        case capabilities
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}
struct StoredIdentity: Codable, Equatable {
    var deviceID: String
    var deviceName: String?
    var deviceKind: String?
    var publicKey: String?
    var publicKeyFingerprint: String
    var capabilities: [String]?
    var createdAt: String?
    var updatedAt: String?
    var privateKey: String

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case deviceName = "device_name"
        case deviceKind = "device_kind"
        case publicKey = "public_key"
        case publicKeyFingerprint = "public_key_fingerprint"
        case capabilities
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case privateKey = "private_key"
    }
}

struct CloudSession: Codable, Equatable {
    var baseURL: String
    var accountToken: String
    var accountIDHash: String?
    var relayID: String?
    var relayURL: String?
    var relayCredential: String?
    var membershipSigningPublicKey: String?
    var membershipLease: JSONValue?
    var expiresAt: String?

    enum CodingKeys: String, CodingKey {
        case baseURL = "base_url"
        case accountToken = "account_token"
        case accountIDHash = "account_id_hash"
        case relayID = "relay_id"
        case relayURL = "relay_url"
        case relayCredential = "relay_credential"
        case membershipSigningPublicKey = "membership_signing_public_key"
        case membershipLease = "membership_lease"
        case expiresAt = "expires_at"
    }
}

struct StartConfig: Codable {
    var storedIdentity: StoredIdentity?
    var deviceName: String?
    var forceRelayOnly: Bool

    enum CodingKeys: String, CodingKey {
        case storedIdentity = "stored_identity"
        case deviceName = "device_name"
        case forceRelayOnly = "force_relay_only"
    }
}

struct StartResult: Codable {
    var ok: Bool?
    var started: Bool?
    var identity: DeviceIdentity?
    var storedIdentity: StoredIdentity?

    enum CodingKeys: String, CodingKey {
        case ok
        case started
        case identity
        case storedIdentity = "stored_identity"
    }
}

struct CloudSessionInput: Codable {
    var storedIdentity: StoredIdentity?
    var session: CloudSession?
    var baseURL: String?
    var loginCode: String?

    enum CodingKeys: String, CodingKey {
        case storedIdentity = "stored_identity"
        case session
        case baseURL = "base_url"
        case loginCode = "login_code"
    }
}

struct CloudSessionResult: Codable {
    var ok: Bool
    var session: CloudSession
}

struct MeshState: Codable {
    var selfDevice: MeshSelfState?
    var cloud: MeshCloudState?
    var hosts: [RemoteHostRecord]
    var pendingPairingCount: Int?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case selfDevice = "self"
        case cloud
        case hosts
        case pendingPairingCount = "pending_pairing_count"
        case updatedAt = "updated_at"
    }
}

struct MeshSelfState: Codable {
    var deviceID: String?
    var deviceName: String?
    var canHost: Bool?
    var canControl: Bool?
    var cloudActive: Bool?
    var relayConnected: Bool?

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case deviceName = "device_name"
        case canHost = "can_host"
        case canControl = "can_control"
        case cloudActive = "cloud_active"
        case relayConnected = "relay_connected"
    }
}

struct MeshCloudState: Codable {
    var enabled: Bool?
    var accountIDHash: String?
    var relayID: String?
    var relayURL: String?
    var credentialExpiresAt: String?

    enum CodingKeys: String, CodingKey {
        case enabled
        case accountIDHash = "account_id_hash"
        case relayID = "relay_id"
        case relayURL = "relay_url"
        case credentialExpiresAt = "credential_expires_at"
    }
}

struct RemoteHostRecord: Codable, Identifiable, Equatable {
    var id: String { deviceID }
    var deviceID: String
    var deviceName: String?
    var deviceKind: String?
    var publicKeyFingerprint: String?
    var knownIdentity: Bool?
    var status: String?
    var connection: String?
    var authorizationState: String?
    var pairingRequestID: String?
    var pairingStatus: String?
    var capabilities: [String]?
    var control: ControlState?

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case deviceName = "device_name"
        case deviceKind = "device_kind"
        case publicKeyFingerprint = "public_key_fingerprint"
        case knownIdentity = "known_identity"
        case status
        case connection
        case authorizationState = "authorization_state"
        case pairingRequestID = "pairing_request_id"
        case pairingStatus = "pairing_status"
        case capabilities
        case control
    }
}

struct ControlState: Codable, Equatable {
    var state: String?
    var transport: String?
    var routeGeneration: Int?
    var lastErrorCode: String?
    var lastError: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case state
        case transport
        case routeGeneration = "route_generation"
        case lastErrorCode = "last_error_code"
        case lastError = "last_error"
        case updatedAt = "updated_at"
    }
}

struct WorkbenchState: Codable {
    var version: Int?
    var updatedAt: String?
    var agents: [String: AgentInfo]?
    var workspaces: [String: Workspace]
    var workspaceConnections: [String: WorkspaceConnection]?
    var sessions: [String: SessionRecord]
    var sessionViews: [String: SessionView]
    var terminalTabs: [String: TerminalTab]

    enum CodingKeys: String, CodingKey {
        case version
        case updatedAt = "updated_at"
        case agents
        case workspaces
        case workspaceConnections = "workspace_connections"
        case sessions
        case sessionViews = "session_views"
        case terminalTabs = "terminal_tabs"
    }

    static var empty: WorkbenchState {
        WorkbenchState(version: 0, updatedAt: nil, agents: nil, workspaces: [:], workspaceConnections: [:], sessions: [:], sessionViews: [:], terminalTabs: [:])
    }
}

struct AgentInfo: Codable, Equatable {
    var available: Bool = false
    var currentModel: String? = nil
    var currentEffort: String? = nil
    var models: [ModelInfo]? = nil

    enum CodingKeys: String, CodingKey {
        case available
        case currentModel = "current_model"
        case currentEffort = "current_effort"
        case models
    }
}

struct ModelInfo: Codable, Equatable {
    var id: String
    var label: String? = nil
    var source: String? = nil
    var slot: String? = nil
    var defaultReasoningEffort: String? = nil
    var supportedReasoningEfforts: [String]? = nil
    var contextWindow: Int? = nil
    var maxContextWindow: Int? = nil
    var effectiveContextWindow: Int? = nil
    var effectiveContextWindowPercent: Int? = nil

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case source
        case slot
        case defaultReasoningEffort = "default_reasoning_effort"
        case supportedReasoningEfforts = "supported_reasoning_efforts"
        case contextWindow = "context_window"
        case maxContextWindow = "max_context_window"
        case effectiveContextWindow = "effective_context_window"
        case effectiveContextWindowPercent = "effective_context_window_percent"
    }

    var displayName: String {
        let trimmed = (label ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? id : trimmed
    }
}

struct Workspace: Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var target: String?
    var agent: String?
    var localCWD: String?
    var ssh: SSHConfig?
    var createdAt: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case target
        case agent
        case localCWD = "local_cwd"
        case ssh
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct SSHConfig: Codable, Equatable {
    var endpoint: String
    var port: Int
    var remoteCWD: String

    enum CodingKeys: String, CodingKey {
        case endpoint
        case port
        case remoteCWD = "remote_cwd"
    }
}

struct WorkspaceConnection: Codable, Equatable {
    var workspaceID: String
    var target: String?
    var status: String?
    var endpoint: String?
    var port: Int?
    var remoteCWD: String?
    var displayCWD: String?
    var message: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case target
        case status
        case endpoint
        case port
        case remoteCWD = "remote_cwd"
        case displayCWD = "display_cwd"
        case message
        case updatedAt = "updated_at"
    }
}

struct SessionRecord: Codable, Identifiable, Equatable {
    var id: String
    var workspaceID: String
    var agent: String?
    var title: String?
    var status: String?
    var forkedFromSessionID: String?
    var forkedFromEventSeq: Int?
    var forkedFromTitle: String?
    var createdAt: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case workspaceID = "workspace_id"
        case agent
        case title
        case status
        case forkedFromSessionID = "forked_from_session_id"
        case forkedFromEventSeq = "forked_from_event_seq"
        case forkedFromTitle = "forked_from_title"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct SessionView: Codable {
    var session: SessionRecord
    var title: String?
    var status: String?
    var pendingInteraction: PendingInteractionView?
    var queuedInputs: [QueuedInputView]?
    var editableUserMessage: EditableUserMessageView?

    enum CodingKeys: String, CodingKey {
        case session
        case title
        case status
        case pendingInteraction = "pending_interaction"
        case queuedInputs = "queued_inputs"
        case editableUserMessage = "editable_user_message"
    }
}

struct PendingInteractionView: Codable, Identifiable {
    var id: String
    var kind: String
    var title: String
    var detailRows: [InteractionDetailRow]?
    var actions: [InteractionActionView]
    var form: JSONValue?

    enum CodingKeys: String, CodingKey {
        case id
        case kind
        case title
        case detailRows = "detail_rows"
        case actions
        case form
    }
}

struct InteractionDetailRow: Codable, Identifiable {
    var id: String { key ?? label }
    var key: String?
    var label: String
    var value: String
    var mono: Bool?
}

struct InteractionActionView: Codable, Identifiable {
    var id: String
    var label: String
    var description: String?
    var role: String?
    var requiresFeedback: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case description
        case role
        case requiresFeedback = "requires_feedback"
    }
}

struct QueuedInputView: Codable, Identifiable {
    var id: String
    var sessionID: String
    var text: String

    enum CodingKeys: String, CodingKey {
        case id
        case sessionID = "session_id"
        case text
    }
}

struct EditableUserMessageView: Codable, Equatable {
    var eventSeq: Int
    var text: String

    enum CodingKeys: String, CodingKey {
        case eventSeq = "event_seq"
        case text
    }
}

struct TerminalTab: Codable, Identifiable, Equatable {
    var id: String { terminalID }
    var terminalID: String
    var workspaceID: String
    var target: String?
    var shell: String?
    var cwd: String?
    var status: String?
    var writerDeviceID: String?
    var outputSeq: Int?
    var canInput: Bool?

    enum CodingKeys: String, CodingKey {
        case terminalID = "terminal_id"
        case workspaceID = "workspace_id"
        case target
        case shell
        case cwd
        case status
        case writerDeviceID = "writer_device_id"
        case outputSeq = "output_seq"
        case canInput = "can_input"
    }
}

struct ControlResponseEnvelope<T: Decodable>: Decodable {
    var ok: Bool?
    var result: T?
    var error: ControlErrorEnvelope?
}

struct ControlErrorEnvelope: Codable, Equatable {
    var status: Int?
    var code: String
    var message: String
}

struct CreateWorkspaceRequest: Codable {
    var name: String
    var target: String
    var agent: String?
    var localCWD: String?
    var ssh: SSHConfig?

    enum CodingKeys: String, CodingKey {
        case name
        case target
        case agent
        case localCWD = "local_cwd"
        case ssh
    }
}

struct HostFileSystemBrowseResult: Codable {
    var target: String
    var platform: String?
    var separator: String?
    var path: String
    var parentPath: String?
    var roots: [HostFileSystemRoot]
    var entries: [HostFileSystemEntry]
    var truncated: Bool?

    enum CodingKeys: String, CodingKey {
        case target
        case platform
        case separator
        case path
        case parentPath = "parent_path"
        case roots
        case entries
        case truncated
    }
}

struct HostFileSystemRoot: Codable, Identifiable {
    var id: String
    var label: String
    var path: String
    var kind: String?
}

struct HostFileSystemEntry: Codable, Identifiable {
    var id: String { path }
    var name: String
    var path: String
    var kind: String
    var size: Int?
    var modTime: String?

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case kind
        case size
        case modTime = "mod_time"
    }
}

struct WorkspaceFileEntry: Codable, Identifiable {
    var id: String { path }
    var name: String
    var path: String
    var kind: String
    var size: Int?
    var modTime: String?

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case kind
        case size
        case modTime = "mod_time"
    }
}

struct WorkspaceFilesReadResult: Codable {
    var workspaceID: String
    var target: String
    var path: String
    var kind: String
    var name: String?
    var size: Int?
    var mimeType: String?
    var contentBase64: String?
    var entries: [WorkspaceFileEntry]?
    var truncated: Bool?

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case target
        case path
        case kind
        case name
        case size
        case mimeType = "mime_type"
        case contentBase64 = "content_base64"
        case entries
        case truncated
    }

    var textContent: String {
        guard let contentBase64, let data = Data(base64Encoded: contentBase64) else { return "" }
        return String(data: data, encoding: .utf8) ?? ""
    }
}

struct WorkspaceExecResult: Codable {
    var workspaceID: String
    var target: String?
    var command: String
    var cwd: String?
    var approvalPolicy: String?
    var exitCode: Int?
    var stdout: String?
    var stderr: String?
    var output: String?
    var durationMS: Int?
    var failure: String?

    enum CodingKeys: String, CodingKey {
        case workspaceID = "workspace_id"
        case target
        case command
        case cwd
        case approvalPolicy = "approval_policy"
        case exitCode = "exit_code"
        case stdout
        case stderr
        case output
        case durationMS = "duration_ms"
        case failure
    }
}

struct ControlAttachmentHandle: Codable, Equatable, Identifiable {
    var id: String
    var mediaID: String?
    var kind: String
    var name: String
    var mimeType: String?
    var size: Int?
    var detail: String?
    var hostOwned: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case mediaID = "media_id"
        case kind
        case name
        case mimeType = "mime_type"
        case size
        case detail
        case hostOwned = "host_owned"
    }
}

struct AttachmentIngestStartResult: Codable {
    var sessionID: String
    var uploadID: String
    var attachmentID: String
    var chunkMaxBytes: Int
    var maxBytes: Int

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case uploadID = "upload_id"
        case attachmentID = "attachment_id"
        case chunkMaxBytes = "chunk_max_bytes"
        case maxBytes = "max_bytes"
    }
}

struct AttachmentIngestChunkResult: Codable {
    var sessionID: String
    var uploadID: String
    var seq: Int
    var offset: Int
    var receivedBytes: Int

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case uploadID = "upload_id"
        case seq
        case offset
        case receivedBytes = "received_bytes"
    }
}

struct AttachmentIngestFinishResult: Codable {
    var sessionID: String
    var uploadID: String
    var attachment: ControlAttachmentHandle

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case uploadID = "upload_id"
        case attachment
    }
}

struct AttachmentIngestResult: Codable {
    var sessionID: String
    var attachment: ControlAttachmentHandle

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case attachment
    }
}

struct MediaReadResult: Codable {
    var sessionID: String
    var eventSeq: Int
    var mediaID: String
    var kind: String
    var name: String
    var mimeType: String?
    var size: Int?
    var contentBase64: String
    var download: Bool?

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case eventSeq = "event_seq"
        case mediaID = "media_id"
        case kind
        case name
        case mimeType = "mime_type"
        case size
        case contentBase64 = "content_base64"
        case download
    }
}

struct HostTrustListResult: Codable {
    var grants: [TrustGrant]
}

struct TrustGrant: Codable, Identifiable {
    var id: String { controllerDeviceID }
    var controllerDeviceID: String
    var controllerDeviceName: String?
    var controllerDeviceKind: String?
    var controllerPublicKeyFingerprint: String?
    var scope: String?
    var status: String?
    var capabilities: [String]?
    var workspaceExecPolicy: String?
    var createdAt: String?
    var updatedAt: String?
    var revokedAt: String?

    enum CodingKeys: String, CodingKey {
        case controllerDeviceID = "controller_device_id"
        case controllerDeviceName = "controller_device_name"
        case controllerDeviceKind = "controller_device_kind"
        case controllerPublicKeyFingerprint = "controller_public_key_fingerprint"
        case scope
        case status
        case capabilities
        case workspaceExecPolicy = "workspace_exec_policy"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case revokedAt = "revoked_at"
    }
}

struct PairingRequestListResult: Codable {
    var requests: [PairingRequest]
}

struct PairingRequest: Codable, Identifiable {
    var id: String { requestID }
    var requestID: String
    var source: String?
    var hostDeviceID: String?
    var controllerDeviceID: String
    var controllerDeviceName: String?
    var controllerDeviceKind: String?
    var controllerPublicKeyFingerprint: String?
    var scope: String?
    var status: String?
    var capabilities: [String]?
    var workspaceExecPolicy: String?
    var createdAt: String?
    var updatedAt: String?
    var resolvedAt: String?

    enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case source
        case hostDeviceID = "host_device_id"
        case controllerDeviceID = "controller_device_id"
        case controllerDeviceName = "controller_device_name"
        case controllerDeviceKind = "controller_device_kind"
        case controllerPublicKeyFingerprint = "controller_public_key_fingerprint"
        case scope
        case status
        case capabilities
        case workspaceExecPolicy = "workspace_exec_policy"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case resolvedAt = "resolved_at"
    }
}

struct SnapshotResult: Codable {
    var workbench: WorkbenchState?
    var events: [AstralEvent]?
    var initialSessionEvents: [AstralEvent]?

    enum CodingKeys: String, CodingKey {
        case workbench
        case events
        case initialSessionEvents = "initial_session_events"
    }
}

struct AstralEvent: Codable {
    var seq: Int
    var ts: String?
    var workspaceID: String?
    var sessionID: String?
    var agent: String?
    var kind: String
    var normalized: JSONValue

    enum CodingKeys: String, CodingKey {
        case seq
        case ts
        case workspaceID = "workspace_id"
        case sessionID = "session_id"
        case agent
        case kind
        case normalized
    }
}
