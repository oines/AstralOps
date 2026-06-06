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

    func testComposerSendsAttachmentOnlyInput() async throws {
        let raw = FakeRawClient()
        let model = AppModel(bridge: MobileCoreBridge(raw: raw))
        configureSelectedHostAndSession(model)
        model.composerText = ""
        model.composerAttachments = [
            ControlAttachmentHandle(id: "att_1", mediaID: nil, kind: "image", name: "clip.png", mimeType: "image/png", size: 4, detail: "high", hostOwned: true)
        ]

        await model.sendComposerText()

        XCTAssertEqual(raw.calls.last?.name, "sendInput")
        XCTAssertEqual(raw.calls.last?.sessionID, "sess_1")
        XCTAssertTrue(raw.calls.last?.payload.contains("\"input\":\"\"") ?? false)
        XCTAssertTrue(raw.calls.last?.payload.contains("\"attachments\"") ?? false)
        XCTAssertFalse(raw.calls.last?.payload.contains("\"path\"") ?? true)
        XCTAssertTrue(model.composerAttachments.isEmpty)
    }

    func testComposerAttachmentUploadUsesChunkedHostIngest() async throws {
        let raw = FakeRawClient()
        raw.responseQueues["controlRequest:attachment.ingest.start"] = [
            #"{"ok":true,"result":{"session_id":"sess_1","upload_id":"up_1","attachment_id":"att_1","chunk_max_bytes":3,"max_bytes":10}}"#
        ]
        raw.responseQueues["controlRequest:attachment.ingest.chunk"] = [
            #"{"ok":true,"result":{"session_id":"sess_1","upload_id":"up_1","seq":1,"offset":0,"received_bytes":3}}"#,
            #"{"ok":true,"result":{"session_id":"sess_1","upload_id":"up_1","seq":2,"offset":3,"received_bytes":5}}"#
        ]
        raw.responseQueues["controlRequest:attachment.ingest.finish"] = [
            #"{"ok":true,"result":{"session_id":"sess_1","upload_id":"up_1","attachment":{"id":"att_1","kind":"image","name":"clip.png","mime_type":"image/png","size":5,"detail":"high","host_owned":true}}}"#
        ]
        let model = AppModel(bridge: MobileCoreBridge(raw: raw))
        configureSelectedHostAndSession(model)

        await model.addComposerAttachment(data: Data([1, 2, 3, 4, 5]), name: "clip.png", mimeType: "image/png", kind: "image")

        XCTAssertEqual(model.composerUploadCount, 0)
        XCTAssertEqual(model.composerAttachments.first?.id, "att_1")
        let controlCalls = raw.calls.filter { $0.name == "controlRequest" }
        XCTAssertEqual(controlCalls.count, 4)
        XCTAssertTrue(controlCalls[0].payload.contains(#""action":"attachment.ingest.start""#))
        XCTAssertTrue(controlCalls[1].payload.contains(#""action":"attachment.ingest.chunk""#))
        XCTAssertTrue(controlCalls[1].payload.contains(#""data_base64":"AQID""#))
        XCTAssertTrue(controlCalls[2].payload.contains(#""data_base64":"BAU=""#))
        XCTAssertTrue(controlCalls[3].payload.contains(#""action":"attachment.ingest.finish""#))
        XCTAssertFalse(controlCalls.contains { $0.payload.contains("\"path\"") })
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

    func testTerminalViewerLifecycleErrorRecoversWithoutGlobalAlert() async throws {
        let raw = FakeRawClient()
        raw.errors["terminalInput"] = MobileCoreBridgeError.controlError(ControlErrorEnvelope(status: 409, code: "terminal_viewer_not_live", message: "terminal viewer is not live"))
        raw.responses["attachTerminal"] = #"{"terminal_id":"term_1","viewer_id":"viewer_1","input_lease_id":"lease_1","output_seq":5}"#
        let model = AppModel(bridge: MobileCoreBridge(raw: raw))
        model.hosts = [
            RemoteHostRecord(
                deviceID: "dev_host",
                deviceName: nil,
                deviceKind: nil,
                publicKeyFingerprint: nil,
                knownIdentity: true,
                status: "online",
                connection: "lan",
                authorizationState: nil,
                pairingRequestID: nil,
                pairingStatus: nil,
                capabilities: nil,
                control: nil
            )
        ]
        model.selectedHostID = "dev_host"
        model.selectedWorkspaceID = "ws_1"
        model.selectedTerminalID = "term_1"
        var workbench = WorkbenchState.empty
        workbench.terminalTabs["term_1"] = TerminalTab(
            terminalID: "term_1",
            workspaceID: "ws_1",
            target: nil,
            shell: "/bin/zsh",
            cwd: "/repo",
            status: "live",
            writerDeviceID: nil,
            outputSeq: 4,
            canInput: true
        )
        model.workbench = workbench

        model.sendTerminalInput("x")
        try await Task.sleep(nanoseconds: 200_000_000)

        XCTAssertEqual(model.errorMessage, "")
        XCTAssertTrue(raw.calls.contains { $0.name == "attachTerminal" && $0.payload == "term_1:0" })
        XCTAssertEqual(raw.calls.filter { $0.name == "terminalInput" }.count, 2)
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

    func testRemoteControlDeadlineErrorUsesUserReadableMessage() {
        let error = NSError(domain: "gomobile", code: 1, userInfo: [
            NSLocalizedDescriptionKey: "remote control request failed: context deadline exceeded"
        ])

        XCTAssertEqual(AppModel.errorDisplayMessage(for: error), "Host request timed out. Check the connection and try again.")
    }

    func testWebViewMediaResponseUsesImageMetadataAndChunksLargePayloads() throws {
        let url = try XCTUnwrap(URL(string: "astralmedia://media?session_id=sess_1&event_seq=7&media_id=img_1"))
        let media = WebViewMediaResponse(data: Data(repeating: 0x42, count: 300_000), mimeType: "image/png")

        let response = media.urlResponse(for: url)
        let chunks = media.chunks()

        XCTAssertEqual(response.mimeType, "image/png")
        XCTAssertEqual(response.expectedContentLength, 300_000)
        XCTAssertEqual(chunks.count, 3)
        XCTAssertEqual(chunks.map(\.count), [131_072, 131_072, 37_856])
    }

    func testBrowseAndWorkspaceEntriesTreatDirKindAsDirectory() throws {
        let hostEntry = try JSONCoding.decode(HostFileSystemEntry.self, from: Data(#"{"name":"src","path":"/repo/src","kind":"dir"}"#.utf8))
        let workspaceEntry = try JSONCoding.decode(WorkspaceFileEntry.self, from: Data(#"{"name":"src","path":"/repo/src","kind":"dir"}"#.utf8))

        XCTAssertTrue(hostEntry.isDirectory)
        XCTAssertTrue(workspaceEntry.isDirectory)
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

    private func configureSelectedHostAndSession(_ model: AppModel) {
        model.hosts = [
            RemoteHostRecord(
                deviceID: "dev_host",
                deviceName: nil,
                deviceKind: nil,
                publicKeyFingerprint: nil,
                knownIdentity: true,
                status: "online",
                connection: "lan",
                authorizationState: nil,
                pairingRequestID: nil,
                pairingStatus: nil,
                capabilities: nil,
                control: nil
            )
        ]
        model.selectedHostID = "dev_host"
        model.selectedSessionID = "sess_1"
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
    var responseQueues: [String: [String]] = [:]
    var errors: [String: Error] = [:]
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
        let actionKey = "controlRequest:\(action)"
        if var queue = responseQueues[actionKey], !queue.isEmpty {
            let next = queue.removeFirst()
            responseQueues[actionKey] = queue
            return next
        }
        if let response = responses[actionKey] {
            return response
        }
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
        if let error = errors["terminalInput"] {
            throw error
        }
        return "{}"
    }

    func terminalResize(hostDeviceID: String, terminalID: String, cols: Int, rows: Int) async throws -> String {
        calls.append(Call(name: "terminalResize", hostDeviceID: hostDeviceID, payload: "\(cols)x\(rows)"))
        if let error = errors["terminalResize"] {
            throw error
        }
        return "{}"
    }

    func terminalHeartbeatAck(hostDeviceID: String, terminalID: String, heartbeatSeq: Int, renderedSeq: Int) async throws -> String {
        calls.append(Call(name: "terminalHeartbeatAck", hostDeviceID: hostDeviceID, payload: "\(terminalID):\(heartbeatSeq):\(renderedSeq)"))
        if let error = errors["terminalHeartbeatAck"] {
            throw error
        }
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
