import Foundation

#if canImport(Mobilecore)
import Mobilecore
#endif

enum MobileCoreEvent: Equatable, Sendable {
    case hostState(Data)
    case workbenchPatch(Data)
    case events(Data)
    case terminalFrame(Data)
    case error(Data)
}

enum MobileCoreBridgeError: Error, LocalizedError {
    case unavailable
    case invalidUTF8
    case emptyResponse

    var errorDescription: String? {
        switch self {
        case .unavailable:
            "Mobilecore.xcframework is not built or linked."
        case .invalidUTF8:
            "Mobile Core returned invalid UTF-8 JSON."
        case .emptyResponse:
            "Mobile Core returned an empty response."
        }
    }
}

@MainActor
protocol MobileCoreRawClient: AnyObject {
    var onEvent: ((MobileCoreEvent) -> Void)? { get set }

    func start(_ configJSON: String) async throws -> String
    func setCloudSession(_ sessionJSON: String) async throws -> String
    func cloudSession() async throws -> String
    func logout() async throws -> String
    func refreshMesh() async throws -> String
    func requestPairing(hostDeviceID: String) async throws -> String
    func openHostSession(hostDeviceID: String) async throws -> String
    func snapshot(hostDeviceID: String, optionsJSON: String) async throws -> String
    func sendInput(hostDeviceID: String, sessionID: String, inputJSON: String) async throws -> String
    func respondInteraction(hostDeviceID: String, interactionID: String, responseJSON: String) async throws -> String
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String
    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String
}

@MainActor
final class MobileCoreBridge: ObservableObject {
    private let raw: MobileCoreRawClient
    private var eventContinuation: AsyncStream<MobileCoreEvent>.Continuation?

    lazy var events: AsyncStream<MobileCoreEvent> = AsyncStream { continuation in
        eventContinuation = continuation
    }

    init(raw: MobileCoreRawClient = MobileCoreClientFactory.make()) {
        self.raw = raw
        self.raw.onEvent = { [weak self] event in
            self?.eventContinuation?.yield(event)
        }
    }

    func start(_ config: StartConfig) async throws -> StartResult {
        try await call(StartResult.self) {
            try await raw.start(jsonString(config))
        }
    }

    func setCloudSession(_ input: CloudSessionInput) async throws -> MeshState {
        try await call(MeshState.self) {
            try await raw.setCloudSession(jsonString(input))
        }
    }

    func cloudSession() async throws -> CloudSession {
        let result = try await call(CloudSessionResult.self) {
            try await raw.cloudSession()
        }
        return result.session
    }

    func logout() async throws -> StartResult {
        try await call(StartResult.self) {
            try await raw.logout()
        }
    }

    func refreshMesh() async throws -> MeshState {
        try await call(MeshState.self) {
            try await raw.refreshMesh()
        }
    }

    func requestPairing(hostDeviceID: String) async throws -> JSONValue {
        try await call(JSONValue.self) {
            try await raw.requestPairing(hostDeviceID: hostDeviceID)
        }
    }

    func openHostSession(hostDeviceID: String) async throws -> JSONValue {
        try await call(JSONValue.self) {
            try await raw.openHostSession(hostDeviceID: hostDeviceID)
        }
    }

    func snapshot(hostDeviceID: String) async throws -> SnapshotResult {
        let envelope = try await call(ControlResponseEnvelope<SnapshotResult>.self) {
            try await raw.snapshot(hostDeviceID: hostDeviceID, optionsJSON: "{}")
        }
        return envelope.result ?? SnapshotResult(workbench: nil, events: nil, initialSessionEvents: nil)
    }

    func sendInput(hostDeviceID: String, sessionID: String, text: String) async throws {
        let input = JSONValue.object(["input": .string(text)])
        _ = try await raw.sendInput(hostDeviceID: hostDeviceID, sessionID: sessionID, inputJSON: jsonString(input))
    }

    func respondInteraction(hostDeviceID: String, interactionID: String, response: JSONValue) async throws {
        _ = try await raw.respondInteraction(hostDeviceID: hostDeviceID, interactionID: interactionID, responseJSON: jsonString(response))
    }

    func subscribeEvents(hostDeviceID: String, sessionID: String?) async throws {
        var payload: [String: JSONValue] = [:]
        if let sessionID, !sessionID.isEmpty {
            payload["session_id"] = .string(sessionID)
        }
        _ = try await raw.subscribeEvents(hostDeviceID: hostDeviceID, optionsJSON: jsonString(JSONValue.object(payload)))
    }

    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> JSONValue {
        try await call(JSONValue.self) {
            try await raw.openTerminal(hostDeviceID: hostDeviceID, workspaceID: workspaceID)
        }
    }

    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> JSONValue {
        try await call(JSONValue.self) {
            try await raw.attachTerminal(hostDeviceID: hostDeviceID, terminalID: terminalID, afterSeq: afterSeq)
        }
    }

    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws {
        _ = try await raw.terminalInput(hostDeviceID: hostDeviceID, terminalID: terminalID, data: data)
    }

    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws {
        _ = try await raw.terminalResize(hostDeviceID: hostDeviceID, terminalID: terminalID, cols: cols, rows: rows)
    }

    func terminalClose(hostDeviceID: String, terminalID: String) async throws {
        _ = try await raw.terminalClose(hostDeviceID: hostDeviceID, terminalID: terminalID)
    }

    private func call<T: Decodable>(_ type: T.Type, operation: () async throws -> String) async throws -> T {
        let raw = try await operation()
        guard let data = raw.data(using: .utf8) else { throw MobileCoreBridgeError.invalidUTF8 }
        guard !data.isEmpty else { throw MobileCoreBridgeError.emptyResponse }
        return try JSONCoding.decode(type, from: data)
    }

    private func jsonString<T: Encodable>(_ value: T) throws -> String {
        let data = try JSONCoding.encode(value)
        guard let string = String(data: data, encoding: .utf8) else { throw MobileCoreBridgeError.invalidUTF8 }
        return string
    }
}

enum MobileCoreClientFactory {
    @MainActor
    static func make() -> MobileCoreRawClient {
        #if canImport(Mobilecore)
        return GoMobileCoreRawClient()
        #else
        return UnavailableMobileCoreRawClient()
        #endif
    }
}

@MainActor
final class UnavailableMobileCoreRawClient: MobileCoreRawClient {
    var onEvent: ((MobileCoreEvent) -> Void)?

    func start(_ configJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func setCloudSession(_ sessionJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func cloudSession() async throws -> String { throw MobileCoreBridgeError.unavailable }
    func logout() async throws -> String { throw MobileCoreBridgeError.unavailable }
    func refreshMesh() async throws -> String { throw MobileCoreBridgeError.unavailable }
    func requestPairing(hostDeviceID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func openHostSession(hostDeviceID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func snapshot(hostDeviceID: String, optionsJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func sendInput(hostDeviceID: String, sessionID: String, inputJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func respondInteraction(hostDeviceID: String, interactionID: String, responseJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
}

#if canImport(Mobilecore)
@MainActor
final class GoMobileCoreRawClient: NSObject, MobileCoreRawClient, MobilecoreCallbackProtocol {
    var onEvent: ((MobileCoreEvent) -> Void)?
    private let core: MobilecoreCore

    override init() {
        self.core = MobilecoreNew()!
        super.init()
        core.setCallback(self)
    }

    func start(_ configJSON: String) async throws -> String { try invoke { core.start(configJSON, error: $0) } }
    func setCloudSession(_ sessionJSON: String) async throws -> String { try invoke { core.setCloudSession(sessionJSON, error: $0) } }
    func cloudSession() async throws -> String { try invoke { core.cloudSession($0) } }
    func logout() async throws -> String { try invoke { core.logout($0) } }
    func refreshMesh() async throws -> String { try invoke { core.refreshMesh($0) } }
    func requestPairing(hostDeviceID: String) async throws -> String { try invoke { core.requestPairing(hostDeviceID, error: $0) } }
    func openHostSession(hostDeviceID: String) async throws -> String { try invoke { core.openHostSession(hostDeviceID, error: $0) } }
    func snapshot(hostDeviceID: String, optionsJSON: String) async throws -> String { try invoke { core.snapshot(hostDeviceID, optionsJSON: optionsJSON, error: $0) } }
    func sendInput(hostDeviceID: String, sessionID: String, inputJSON: String) async throws -> String { try invoke { core.sendInput(hostDeviceID, sessionID: sessionID, inputJSON: inputJSON, error: $0) } }
    func respondInteraction(hostDeviceID: String, interactionID: String, responseJSON: String) async throws -> String { try invoke { core.respondInteraction(hostDeviceID, interactionID: interactionID, responseJSON: responseJSON, error: $0) } }
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String { try invoke { core.subscribeEvents(hostDeviceID, optionsJSON: optionsJSON, error: $0) } }
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String { try invoke { core.openTerminal(hostDeviceID, workspaceID: workspaceID, error: $0) } }
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String { try invoke { core.attachTerminal(hostDeviceID, terminalID: terminalID, afterSeq: Int64(afterSeq), error: $0) } }
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String { try invoke { core.terminalInput(hostDeviceID, terminalID: terminalID, data: data, error: $0) } }
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String { try invoke { core.terminalResize(hostDeviceID, terminalID: terminalID, cols: cols, rows: rows, error: $0) } }
    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String { try invoke { core.terminalClose(hostDeviceID, terminalID: terminalID, error: $0) } }

    nonisolated func onHostState(_ payload: String?) {
        dispatch(event(payload, MobileCoreEvent.hostState))
    }

    nonisolated func onWorkbenchPatch(_ payload: String?) {
        dispatch(event(payload, MobileCoreEvent.workbenchPatch))
    }

    nonisolated func onEvents(_ payload: String?) {
        dispatch(event(payload, MobileCoreEvent.events))
    }

    nonisolated func onTerminalFrame(_ payload: String?) {
        dispatch(event(payload, MobileCoreEvent.terminalFrame))
    }

    nonisolated func onError(_ payload: String?) {
        dispatch(event(payload, MobileCoreEvent.error))
    }

    private func invoke(_ operation: (NSErrorPointer) -> String) throws -> String {
        var error: NSError?
        let result = operation(&error)
        if let error {
            throw error
        }
        return result
    }

    private nonisolated func event(_ payload: String?, _ make: (Data) -> MobileCoreEvent) -> MobileCoreEvent? {
        guard let data = payload?.data(using: .utf8) else { return nil }
        return make(data)
    }

    private nonisolated func dispatch(_ event: MobileCoreEvent?) {
        guard let event else { return }
        Task { @MainActor in
            onEvent?(event)
        }
    }
}
#endif
