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
    case controlError(ControlErrorEnvelope)

    var errorDescription: String? {
        switch self {
        case .unavailable:
            "Mobilecore.xcframework is not built or linked."
        case .invalidUTF8:
            "Mobile Core returned invalid UTF-8 JSON."
        case .emptyResponse:
            "Mobile Core returned an empty response."
        case .controlError(let error):
            error.message.isEmpty ? error.code : error.message
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
    func controlRequest(hostDeviceID: String, capability: String, action: String, paramsJSON: String) async throws -> String
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String
    func listTerminals(hostDeviceID: String) async throws -> String
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String
    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws -> String
    func detachTerminal(hostDeviceID: String, terminalID: String) async throws -> String
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
        let optionsJSON = try jsonString(JSONValue.object([
            "restore_on_launch": .bool(true),
            "event_limit": .number(1000)
        ]))
        let envelope = try await call(ControlResponseEnvelope<SnapshotResult>.self) {
            try await raw.snapshot(hostDeviceID: hostDeviceID, optionsJSON: optionsJSON)
        }
        return envelope.result ?? SnapshotResult(workbench: nil, events: nil, initialSessionEvents: nil)
    }

    func sendInput(hostDeviceID: String, sessionID: String, text: String, options: JSONValue = .object([:])) async throws {
        var input = options.objectValue ?? [:]
        input["input"] = .string(text)
        _ = try await raw.sendInput(hostDeviceID: hostDeviceID, sessionID: sessionID, inputJSON: jsonString(input))
    }

    func respondInteraction(hostDeviceID: String, interactionID: String, response: JSONValue) async throws {
        _ = try await raw.respondInteraction(hostDeviceID: hostDeviceID, interactionID: interactionID, responseJSON: jsonString(response))
    }

    func controlRequest(hostDeviceID: String, capability: String, action: String, params: JSONValue = .object([:])) async throws -> JSONValue {
        let envelope = try await call(ControlResponseEnvelope<JSONValue>.self) {
            try await raw.controlRequest(hostDeviceID: hostDeviceID, capability: capability, action: action, paramsJSON: jsonString(params))
        }
        if envelope.ok == false, let error = envelope.error {
            throw MobileCoreBridgeError.controlError(error)
        }
        if let result = envelope.result {
            return result
        }
        return .object(["ok": .bool(envelope.ok ?? false)])
    }

    func subscribeEvents(hostDeviceID: String, sessionID: String?) async throws {
        var payload: [String: JSONValue] = [:]
        if let sessionID, !sessionID.isEmpty {
            payload["session_id"] = .string(sessionID)
        }
        _ = try await raw.subscribeEvents(hostDeviceID: hostDeviceID, optionsJSON: jsonString(JSONValue.object(payload)))
    }

    func listTerminals(hostDeviceID: String) async throws -> [TerminalTab] {
        let envelope = try await call(ControlResponseEnvelope<[TerminalTab]>.self) {
            try await raw.listTerminals(hostDeviceID: hostDeviceID)
        }
        if envelope.ok == false, let error = envelope.error {
            throw MobileCoreBridgeError.controlError(error)
        }
        return envelope.result ?? []
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

    func attachTerminal(hostDeviceID: String, terminalID: String) async throws -> JSONValue {
        try await attachTerminal(hostDeviceID: hostDeviceID, terminalID: terminalID, afterSeq: 0)
    }

    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws {
        _ = try await raw.terminalInput(hostDeviceID: hostDeviceID, terminalID: terminalID, data: data)
    }

    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws {
        _ = try await raw.terminalResize(hostDeviceID: hostDeviceID, terminalID: terminalID, cols: cols, rows: rows)
    }

    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws {
        _ = try await raw.terminalHeartbeatAck(hostDeviceID: hostDeviceID, terminalID: terminalID, heartbeatSeq: heartbeatSeq, renderedSeq: renderedSeq)
    }

    func detachTerminal(hostDeviceID: String, terminalID: String) async throws {
        _ = try await raw.detachTerminal(hostDeviceID: hostDeviceID, terminalID: terminalID)
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
    func controlRequest(hostDeviceID: String, capability: String, action: String, paramsJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func listTerminals(hostDeviceID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func detachTerminal(hostDeviceID: String, terminalID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String { throw MobileCoreBridgeError.unavailable }
}

#if canImport(Mobilecore)
private final class GoMobileCoreBox: @unchecked Sendable {
    let core: MobilecoreCore

    init() {
        core = MobilecoreNew()!
    }
}

@MainActor
final class GoMobileCoreRawClient: NSObject, MobileCoreRawClient, MobilecoreCallbackProtocol {
    var onEvent: ((MobileCoreEvent) -> Void)?
    private let box: GoMobileCoreBox
    private let queue = DispatchQueue(label: "dev.oines.astralops.mobilecore")

    override init() {
        self.box = GoMobileCoreBox()
        super.init()
        box.core.setCallback(self)
    }

    func start(_ configJSON: String) async throws -> String { try await invoke { core, error in core.start(configJSON, error: error) } }
    func setCloudSession(_ sessionJSON: String) async throws -> String { try await invoke { core, error in core.setCloudSession(sessionJSON, error: error) } }
    func cloudSession() async throws -> String { try await invoke { core, error in core.cloudSession(error) } }
    func logout() async throws -> String { try await invoke { core, error in core.logout(error) } }
    func refreshMesh() async throws -> String { try await invoke { core, error in core.refreshMesh(error) } }
    func requestPairing(hostDeviceID: String) async throws -> String { try await invoke { core, error in core.requestPairing(hostDeviceID, error: error) } }
    func openHostSession(hostDeviceID: String) async throws -> String { try await invoke { core, error in core.openHostSession(hostDeviceID, error: error) } }
    func snapshot(hostDeviceID: String, optionsJSON: String) async throws -> String { try await invoke { core, error in core.snapshot(hostDeviceID, optionsJSON: optionsJSON, error: error) } }
    func sendInput(hostDeviceID: String, sessionID: String, inputJSON: String) async throws -> String { try await invoke { core, error in core.sendInput(hostDeviceID, sessionID: sessionID, inputJSON: inputJSON, error: error) } }
    func respondInteraction(hostDeviceID: String, interactionID: String, responseJSON: String) async throws -> String { try await invoke { core, error in core.respondInteraction(hostDeviceID, interactionID: interactionID, responseJSON: responseJSON, error: error) } }
    func controlRequest(hostDeviceID: String, capability: String, action: String, paramsJSON: String) async throws -> String { try await invoke { core, error in core.controlRequest(hostDeviceID, capability: capability, action: action, paramsJSON: paramsJSON, error: error) } }
    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String { try await invoke { core, error in core.subscribeEvents(hostDeviceID, optionsJSON: optionsJSON, error: error) } }
    func listTerminals(hostDeviceID: String) async throws -> String { try await invoke { core, error in core.listTerminals(hostDeviceID, error: error) } }
    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String { try await invoke { core, error in core.openTerminal(hostDeviceID, workspaceID: workspaceID, error: error) } }
    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String { try await invoke { core, error in core.attachTerminal(hostDeviceID, terminalID: terminalID, afterSeq: Int64(afterSeq), error: error) } }
    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String { try await invoke { core, error in core.terminalInput(hostDeviceID, terminalID: terminalID, data: data, error: error) } }
    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String { try await invoke { core, error in core.terminalResize(hostDeviceID, terminalID: terminalID, cols: cols, rows: rows, error: error) } }
    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws -> String { try await invoke { core, error in core.terminalHeartbeatAck(hostDeviceID, terminalID: terminalID, heartbeatSeq: Int64(heartbeatSeq), renderedSeq: Int64(renderedSeq), error: error) } }
    func detachTerminal(hostDeviceID: String, terminalID: String) async throws -> String { try await invoke { core, error in core.detachTerminal(hostDeviceID, terminalID: terminalID, error: error) } }
    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String { try await invoke { core, error in core.terminalClose(hostDeviceID, terminalID: terminalID, error: error) } }

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

    private func invoke(_ operation: @escaping @Sendable (MobilecoreCore, NSErrorPointer) -> String) async throws -> String {
        let box = box
        return try await withCheckedThrowingContinuation { continuation in
            queue.async {
                var error: NSError?
                let result = operation(box.core, &error)
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: result)
                }
            }
        }
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
