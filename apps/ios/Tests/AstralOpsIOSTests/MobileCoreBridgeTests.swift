import XCTest
import Security
@testable import AstralOpsIOS

@MainActor
final class MobileCoreBridgeTests: XCTestCase {
    func testStartDecodesStoredIdentityWithoutDerivingState() async throws {
        let raw = FakeRawClient()
        raw.responses["start"] = """
        {
          "ok": true,
          "started": true,
          "identity": {
            "device_id": "dev_phone",
            "device_name": "Phone",
            "device_kind": "mobile",
            "public_key_fingerprint": "sha256:PHONE"
          },
          "stored_identity": {
            "device_id": "dev_phone",
            "device_name": "Phone",
            "device_kind": "mobile",
            "public_key_fingerprint": "sha256:PHONE",
            "private_key": "secret"
          }
        }
        """
        let bridge = MobileCoreBridge(raw: raw)

        let result = try await bridge.start(StartConfig(storedIdentity: nil, deviceName: "Phone", forceRelayOnly: true))

        XCTAssertEqual(result.identity?.deviceID, "dev_phone")
        XCTAssertEqual(result.storedIdentity?.privateKey, "secret")
        XCTAssertTrue(raw.calls.contains { $0.name == "start" && $0.payload.contains("\"force_relay_only\":true") })
    }

    func testSendInputPassesInputJSONThroughRawClient() async throws {
        let raw = FakeRawClient()
        let bridge = MobileCoreBridge(raw: raw)

        try await bridge.sendInput(hostDeviceID: "dev_host", sessionID: "sess_1", text: "hello")

        XCTAssertEqual(raw.calls.last?.name, "sendInput")
        XCTAssertEqual(raw.calls.last?.hostDeviceID, "dev_host")
        XCTAssertEqual(raw.calls.last?.sessionID, "sess_1")
        XCTAssertTrue(raw.calls.last?.payload.contains("\"input\":\"hello\"") ?? false)
    }

    func testSendInputIncludesRunOptionsAndHostOwnedAttachments() async throws {
        let raw = FakeRawClient()
        let bridge = MobileCoreBridge(raw: raw)

        try await bridge.sendInput(
            hostDeviceID: "dev_host",
            sessionID: "sess_1",
            text: "hello",
            options: .object([
                "model": .string("gpt-test"),
                "attachments": .array([
                    .object([
                        "id": .string("att_1"),
                        "kind": .string("image"),
                        "name": .string("clip.png"),
                        "mime_type": .string("image/png"),
                        "size": .number(4)
                    ])
                ])
            ])
        )

        XCTAssertEqual(raw.calls.last?.name, "sendInput")
        XCTAssertTrue(raw.calls.last?.payload.contains("\"model\":\"gpt-test\"") ?? false)
        XCTAssertTrue(raw.calls.last?.payload.contains("\"attachments\"") ?? false)
        XCTAssertFalse(raw.calls.last?.payload.contains("\"path\"") ?? true)
    }

    func testControlRequestDecodesResultAndSurfacesRemoteErrors() async throws {
        let raw = FakeRawClient()
        raw.responses["controlRequest"] = #"{"ok":true,"result":{"workspace_id":"ws_1","path":"note.txt"}}"#
        let bridge = MobileCoreBridge(raw: raw)

        let result = try await bridge.controlRequest(hostDeviceID: "dev_host", capability: "workspace.files.read", action: "workspace.files.read", params: .object(["workspace_id": .string("ws_1")]))

        XCTAssertEqual(result.objectValue?["workspace_id"]?.stringValue, "ws_1")
        XCTAssertEqual(raw.calls.last?.name, "controlRequest")
        XCTAssertEqual(raw.calls.last?.hostDeviceID, "dev_host")
        XCTAssertTrue(raw.calls.last?.payload.contains("\"action\":\"workspace.files.read\"") ?? false)

        raw.responses["controlRequest"] = #"{"ok":false,"error":{"status":403,"code":"capability_denied","message":"denied"}}"#
        do {
            _ = try await bridge.controlRequest(hostDeviceID: "dev_host", capability: "workspace.files.read", action: "workspace.files.read")
            XCTFail("expected control error")
        } catch {
            XCTAssertEqual(error.localizedDescription, "denied")
        }
    }

    func testTerminalHeartbeatAndDetachUseTypedWrappers() async throws {
        let raw = FakeRawClient()
        let bridge = MobileCoreBridge(raw: raw)

        try await bridge.terminalHeartbeatAck(hostDeviceID: "dev_host", terminalID: "term_1", heartbeatSeq: 7, renderedSeq: 6)
        try await bridge.detachTerminal(hostDeviceID: "dev_host", terminalID: "term_1")

        XCTAssertEqual(raw.calls.dropLast().last?.name, "terminalHeartbeatAck")
        XCTAssertEqual(raw.calls.dropLast().last?.payload, "term_1:7:6")
        XCTAssertEqual(raw.calls.last?.name, "detachTerminal")
        XCTAssertEqual(raw.calls.last?.payload, "term_1")
    }

    func testAttachTerminalDefaultsToFullReplay() async throws {
        let raw = FakeRawClient()
        let bridge = MobileCoreBridge(raw: raw)

        _ = try await bridge.attachTerminal(hostDeviceID: "dev_host", terminalID: "term_1")

        XCTAssertEqual(raw.calls.last?.name, "attachTerminal")
        XCTAssertEqual(raw.calls.last?.hostDeviceID, "dev_host")
        XCTAssertEqual(raw.calls.last?.payload, "term_1:0")
    }

    func testSnapshotRequestsLaunchEventsAndDecodesInitialEventArray() async throws {
        let raw = FakeRawClient()
        raw.responses["snapshot"] = """
        {
          "ok": true,
          "result": {
            "initial_session_events": [
              {
                "seq": 1,
                "session_id": "sess_1",
                "kind": "message.user",
                "normalized": {"text": "hello"}
              }
            ]
          }
        }
        """
        let bridge = MobileCoreBridge(raw: raw)
        let decoded = try JSONCoding.decode(ControlResponseEnvelope<SnapshotResult>.self, from: try XCTUnwrap(raw.responses["snapshot"]?.data(using: .utf8)))
        XCTAssertEqual(decoded.result?.initialSessionEvents?.count, 1)

        let snapshot = try await bridge.snapshot(hostDeviceID: "dev_host")

        XCTAssertEqual(raw.calls.last?.name, "snapshot")
        XCTAssertTrue(raw.calls.last?.payload.contains("\"restore_on_launch\":true") ?? false)
        XCTAssertTrue(raw.calls.last?.payload.contains("\"event_limit\":1000") ?? false)
        XCTAssertEqual(snapshot.initialSessionEvents?.count, 1)
        XCTAssertEqual(snapshot.initialSessionEvents?.first?.sessionID, "sess_1")
    }

    func testCloudTransportErrorUsesUserReadableMessage() {
        let error = NSError(domain: "gomobile", code: 1, userInfo: [
            NSLocalizedDescriptionKey: #"Get "https://cloud-astralops.oines.dev/v1/account": EOF"#
        ])

        XCTAssertEqual(AppModel.errorDisplayMessage(for: error), "Cloud request failed. Check your network and try again.")
    }

    func testKeychainRoundTripPreservesStoredIdentityJSON() throws {
        let store = KeychainStore(service: "dev.oines.astralops.ios.tests.\(UUID().uuidString)")
        let identityAccount = "stored_identity"
        let cloudSessionAccount = "cloud_session"
        let identity = StoredIdentity(
            deviceID: "dev_phone",
            deviceName: "Phone",
            deviceKind: "mobile",
            publicKey: "public",
            publicKeyFingerprint: "sha256:PHONE",
            capabilities: ["core.read"],
            createdAt: nil,
            updatedAt: nil,
            privateKey: "private"
        )
        let cloudSession = CloudSession(baseURL: "https://cloud.test", accountToken: "token")
        let identityData = try JSONCoding.encode(identity)
        let cloudSessionData = try JSONCoding.encode(cloudSession)

        do {
            try store.save(identityData, account: identityAccount)
            try store.save(cloudSessionData, account: cloudSessionAccount)
            let loadedIdentity = try XCTUnwrap(store.load(identityAccount))
            let loadedCloudSession = try XCTUnwrap(store.load(cloudSessionAccount))
            let decodedIdentity = try JSONCoding.decode(StoredIdentity.self, from: loadedIdentity)
            let decodedCloudSession = try JSONCoding.decode(CloudSession.self, from: loadedCloudSession)

            XCTAssertEqual(decodedIdentity, identity)
            XCTAssertEqual(decodedCloudSession, cloudSession)
            try store.delete(identityAccount)
            try store.delete(cloudSessionAccount)
        } catch KeychainStore.KeychainError.unexpectedStatus(let status) where status == errSecMissingEntitlement {
            throw XCTSkip("Keychain is unavailable when simulator tests run without code signing entitlements.")
        }
    }
}

@MainActor
private final class FakeRawClient: MobileCoreRawClient {
    struct Call {
        var name: String
        var hostDeviceID: String?
        var sessionID: String?
        var payload: String
    }

    var onEvent: ((MobileCoreEvent) -> Void)?
    var responses: [String: String] = [:]
    var calls: [Call] = []

    func start(_ configJSON: String) async throws -> String {
        calls.append(Call(name: "start", payload: configJSON))
        return responses["start"] ?? "{}"
    }

    func setCloudSession(_ sessionJSON: String) async throws -> String {
        calls.append(Call(name: "setCloudSession", payload: sessionJSON))
        return responses["setCloudSession"] ?? #"{"hosts":[]}"#
    }

    func cloudSession() async throws -> String {
        calls.append(Call(name: "cloudSession", payload: ""))
        return responses["cloudSession"] ?? #"{"ok":true,"session":{"base_url":"http://cloud.test","account_token":"token"}}"#
    }

    func logout() async throws -> String {
        calls.append(Call(name: "logout", payload: ""))
        return responses["logout"] ?? "{}"
    }

    func refreshMesh() async throws -> String {
        calls.append(Call(name: "refreshMesh", payload: ""))
        return responses["refreshMesh"] ?? #"{"hosts":[]}"#
    }

    func requestPairing(hostDeviceID: String) async throws -> String {
        calls.append(Call(name: "requestPairing", hostDeviceID: hostDeviceID, payload: ""))
        return "{}"
    }

    func openHostSession(hostDeviceID: String) async throws -> String {
        calls.append(Call(name: "openHostSession", hostDeviceID: hostDeviceID, payload: ""))
        return "{}"
    }

    func snapshot(hostDeviceID: String, optionsJSON: String) async throws -> String {
        calls.append(Call(name: "snapshot", hostDeviceID: hostDeviceID, payload: optionsJSON))
        return responses["snapshot"] ?? #"{"ok":true,"result":{}}"#
    }

    func sendInput(hostDeviceID: String, sessionID: String, inputJSON: String) async throws -> String {
        calls.append(Call(name: "sendInput", hostDeviceID: hostDeviceID, sessionID: sessionID, payload: inputJSON))
        return "{}"
    }

    func respondInteraction(hostDeviceID: String, interactionID: String, responseJSON: String) async throws -> String {
        calls.append(Call(name: "respondInteraction", hostDeviceID: hostDeviceID, payload: responseJSON))
        return "{}"
    }

    func controlRequest(hostDeviceID: String, capability: String, action: String, paramsJSON: String) async throws -> String {
        calls.append(Call(name: "controlRequest", hostDeviceID: hostDeviceID, payload: #"{"capability":"\#(capability)","action":"\#(action)","params":\#(paramsJSON)}"#))
        return responses["controlRequest"] ?? #"{"ok":true,"result":{}}"#
    }

    func subscribeEvents(hostDeviceID: String, optionsJSON: String) async throws -> String {
        calls.append(Call(name: "subscribeEvents", hostDeviceID: hostDeviceID, payload: optionsJSON))
        return "{}"
    }

    func listTerminals(hostDeviceID: String) async throws -> String {
        calls.append(Call(name: "listTerminals", hostDeviceID: hostDeviceID, payload: ""))
        return responses["listTerminals"] ?? #"{"ok":true,"result":[]}"#
    }

    func openTerminal(hostDeviceID: String, workspaceID: String) async throws -> String {
        calls.append(Call(name: "openTerminal", hostDeviceID: hostDeviceID, payload: workspaceID))
        return "{}"
    }

    func attachTerminal(hostDeviceID: String, terminalID: String, afterSeq: Int) async throws -> String {
        calls.append(Call(name: "attachTerminal", hostDeviceID: hostDeviceID, payload: "\(terminalID):\(afterSeq)"))
        return "{}"
    }

    func terminalInput(hostDeviceID: String, terminalID: String, data: String) async throws -> String {
        calls.append(Call(name: "terminalInput", hostDeviceID: hostDeviceID, payload: data))
        return "{}"
    }

    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String {
        calls.append(Call(name: "terminalResize", hostDeviceID: hostDeviceID, payload: "\(cols)x\(rows)"))
        return "{}"
    }

    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws -> String {
        calls.append(Call(name: "terminalHeartbeatAck", hostDeviceID: hostDeviceID, payload: "\(terminalID):\(heartbeatSeq):\(renderedSeq)"))
        return "{}"
    }

    func detachTerminal(hostDeviceID: String, terminalID: String) async throws -> String {
        calls.append(Call(name: "detachTerminal", hostDeviceID: hostDeviceID, payload: terminalID))
        return "{}"
    }

    func terminalClose(hostDeviceID: String, terminalID: String) async throws -> String {
        calls.append(Call(name: "terminalClose", hostDeviceID: hostDeviceID, payload: terminalID))
        return "{}"
    }
}
