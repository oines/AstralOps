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
    var workspaces: [String: Workspace]
    var sessions: [String: SessionRecord]
    var sessionViews: [String: SessionView]
    var terminalTabs: [String: TerminalTab]

    enum CodingKeys: String, CodingKey {
        case version
        case updatedAt = "updated_at"
        case workspaces
        case sessions
        case sessionViews = "session_views"
        case terminalTabs = "terminal_tabs"
    }

    static var empty: WorkbenchState {
        WorkbenchState(version: 0, updatedAt: nil, workspaces: [:], sessions: [:], sessionViews: [:], terminalTabs: [:])
    }
}

struct Workspace: Codable, Identifiable, Equatable {
    var id: String
    var name: String
    var target: String?
    var agent: String?
    var createdAt: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case target
        case agent
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct SessionRecord: Codable, Identifiable, Equatable {
    var id: String
    var workspaceID: String
    var agent: String?
    var title: String?
    var status: String?
    var createdAt: String?
    var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case workspaceID = "workspace_id"
        case agent
        case title
        case status
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

    enum CodingKeys: String, CodingKey {
        case session
        case title
        case status
        case pendingInteraction = "pending_interaction"
        case queuedInputs = "queued_inputs"
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

struct TerminalTab: Codable, Identifiable, Equatable {
    var id: String { terminalID }
    var terminalID: String
    var workspaceID: String
    var shell: String?
    var cwd: String?
    var status: String?
    var outputSeq: Int?

    enum CodingKeys: String, CodingKey {
        case terminalID = "terminal_id"
        case workspaceID = "workspace_id"
        case shell
        case cwd
        case status
        case outputSeq = "output_seq"
    }
}

struct ControlResponseEnvelope<T: Decodable>: Decodable {
    var ok: Bool?
    var result: T?
}

struct SnapshotResult: Codable {
    var workbench: WorkbenchState?
    var events: [AstralEvent]?
    var initialSessionEvents: [SessionEvents]?

    enum CodingKeys: String, CodingKey {
        case workbench
        case events
        case initialSessionEvents = "initial_session_events"
    }
}

struct SessionEvents: Codable {
    var sessionID: String
    var events: [AstralEvent]

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case events
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
