import Foundation
import SwiftUI

@MainActor
final class AppModel: ObservableObject {
    enum Page: Int {
        case navigator = 0
        case transcript = 1
        case terminal = 2
    }

    @Published var page: Page = .transcript
    @Published var identity: DeviceIdentity?
    @Published var cloudSession: CloudSession?
    @Published var mesh: MeshState?
    @Published var hosts: [RemoteHostRecord] = []
    @Published var workbench: WorkbenchState = .empty
    @Published var selectedHostID = ""
    @Published var selectedWorkspaceID = ""
    @Published var selectedSessionID = ""
    @Published var selectedTerminalID = ""
    @Published var composerText = ""
    @Published var errorMessage = ""
    @Published var isBusy = false
    @Published var settingsPresented = false
    @Published var composerActionsPresented = false
    @Published var pendingInteraction: PendingInteractionView?
    @Published var forceRelayOnly = false

    let transcriptBridge = WebViewBridge()
    let terminalBridge = WebViewBridge()

    private let bridge: MobileCoreBridge
    private let keychain = KeychainStore(service: "dev.oines.astralops.ios")
    private var storedIdentity: StoredIdentity?
    private var eventsBySession: [String: [AstralEvent]] = [:]
    private var eventTask: Task<Void, Never>?

    private let storedIdentityAccount = "stored_identity"
    private let cloudSessionAccount = "cloud_session"

    init(bridge: MobileCoreBridge = MobileCoreBridge()) {
        self.bridge = bridge
        self.forceRelayOnly = UserDefaults.standard.bool(forKey: "force_relay_only")
        eventTask = Task { [weak self] in
            guard let self else { return }
            for await event in bridge.events {
                await self.handle(event)
            }
        }
    }

    deinit {
        eventTask?.cancel()
    }

    var selectedHost: RemoteHostRecord? {
        hosts.first { $0.deviceID == selectedHostID } ?? hosts.first
    }

    var workspaces: [Workspace] {
        sortByUpdated(Array(workbench.workspaces.values))
    }

    var sessions: [SessionRecord] {
        sortByUpdated(Array(workbench.sessions.values).filter { selectedWorkspaceID.isEmpty || $0.workspaceID == selectedWorkspaceID })
    }

    var terminalTabs: [TerminalTab] {
        sortByUpdated(Array(workbench.terminalTabs.values).filter { selectedWorkspaceID.isEmpty || $0.workspaceID == selectedWorkspaceID })
    }

    var selectedSessionView: SessionView? {
        workbench.sessionViews[selectedSessionID]
    }

    var activeTranscriptEvents: [AstralEvent] {
        eventsBySession[selectedSessionID] ?? []
    }

    func start() async {
        isBusy = true
        defer { isBusy = false }
        do {
            storedIdentity = try loadStoredIdentity()
            cloudSession = try loadCloudSession()
            let result = try await bridge.start(StartConfig(storedIdentity: storedIdentity, deviceName: "AstralOps iPhone", forceRelayOnly: forceRelayOnly))
            identity = result.identity
            if let stored = result.storedIdentity {
                storedIdentity = stored
                try saveStoredIdentity(stored)
            }
            if let cloudSession {
                let nextMesh = try await bridge.setCloudSession(CloudSessionInput(storedIdentity: storedIdentity, session: cloudSession, baseURL: nil, loginCode: nil))
                applyMesh(nextMesh)
            }
            renderTranscript()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func refreshMesh(silent: Bool = false) async {
        if !silent { isBusy = true }
        defer { if !silent { isBusy = false } }
        do {
            let next = try await bridge.refreshMesh()
            applyMesh(next)
        } catch {
            if !silent { errorMessage = error.localizedDescription }
        }
    }

    func login(baseURL: String, loginCode: String) async {
        isBusy = true
        defer { isBusy = false }
        do {
            let next = try await bridge.setCloudSession(CloudSessionInput(storedIdentity: storedIdentity, session: nil, baseURL: baseURL, loginCode: loginCode))
            let session = try await bridge.cloudSession()
            cloudSession = session
            try saveCloudSession(session)
            applyMesh(next)
            settingsPresented = false
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func logout() async {
        isBusy = true
        defer { isBusy = false }
        do {
            let result = try await bridge.logout()
            if let stored = result.storedIdentity {
                storedIdentity = stored
                try saveStoredIdentity(stored)
            }
            identity = result.identity
            cloudSession = nil
            try keychain.delete(cloudSessionAccount)
            mesh = nil
            hosts = []
            workbench = .empty
            selectedHostID = ""
            selectedWorkspaceID = ""
            selectedSessionID = ""
            selectedTerminalID = ""
            eventsBySession = [:]
            renderTranscript()
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func toggleForceRelayOnly(_ enabled: Bool) {
        forceRelayOnly = enabled
        UserDefaults.standard.set(enabled, forKey: "force_relay_only")
        Task { await start() }
    }

    func selectHost(_ host: RemoteHostRecord) {
        selectedHostID = host.deviceID
        workbench = .empty
        selectedWorkspaceID = ""
        selectedSessionID = ""
        selectedTerminalID = ""
        eventsBySession = [:]
        renderTranscript()
        Task { await loadSnapshot(for: host.deviceID) }
    }

    func selectWorkspace(_ workspace: Workspace) {
        selectedWorkspaceID = workspace.id
        selectedSessionID = sessions.first?.id ?? ""
        selectedTerminalID = terminalTabs.first?.terminalID ?? ""
        renderTranscript()
    }

    func selectSession(_ session: SessionRecord) {
        selectedWorkspaceID = session.workspaceID
        selectedSessionID = session.id
        pendingInteraction = workbench.sessionViews[session.id]?.pendingInteraction
        renderTranscript()
        Task {
            if let hostID = selectedHost?.deviceID {
                try? await bridge.subscribeEvents(hostDeviceID: hostID, sessionID: session.id)
            }
        }
    }

    func requestPairing(_ host: RemoteHostRecord) async {
        do {
            _ = try await bridge.requestPairing(hostDeviceID: host.deviceID)
            await refreshMesh(silent: false)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func sendComposerText() async {
        let text = composerText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, let host = selectedHost, !selectedSessionID.isEmpty else { return }
        composerText = ""
        do {
            try await bridge.sendInput(hostDeviceID: host.deviceID, sessionID: selectedSessionID, text: text)
        } catch {
            composerText = text
            errorMessage = error.localizedDescription
        }
    }

    func openTerminal() async {
        guard let host = selectedHost else { return }
        let workspaceID = selectedWorkspaceID
        guard !workspaceID.isEmpty else { return }
        do {
            let info = try await bridge.openTerminal(hostDeviceID: host.deviceID, workspaceID: workspaceID)
            if let terminalID = info.objectValue?["terminal_id"]?.stringValue {
                selectedTerminalID = terminalID
            }
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func sendTerminalInput(_ data: String) {
        guard let host = selectedHost, !selectedTerminalID.isEmpty else { return }
        Task {
            do {
                try await bridge.terminalInput(hostDeviceID: host.deviceID, terminalID: selectedTerminalID, data: data)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    func respond(to interaction: PendingInteractionView, action: InteractionActionView, feedback: String = "") async {
        guard let host = selectedHost else { return }
        var response: [String: JSONValue] = ["decision": .string(action.id)]
        if !feedback.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            response["feedback"] = .string(feedback)
        }
        do {
            try await bridge.respondInteraction(hostDeviceID: host.deviceID, interactionID: interaction.id, response: .object(response))
            pendingInteraction = nil
            await loadSnapshot(for: host.deviceID, silent: true)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    func terminalShortcut(_ value: String) {
        sendTerminalInput(value)
    }

    private func loadSnapshot(for hostID: String, silent: Bool = false) async {
        if !silent { isBusy = true }
        defer { if !silent { isBusy = false } }
        do {
            _ = try await bridge.openHostSession(hostDeviceID: hostID)
            let snapshot = try await bridge.snapshot(hostDeviceID: hostID)
            if let nextWorkbench = snapshot.workbench {
                workbench = nextWorkbench
                reconcileSelection()
            }
            if let events = snapshot.events {
                mergeEvents(events)
            }
            if let initial = snapshot.initialSessionEvents {
                for entry in initial {
                    eventsBySession[entry.sessionID] = entry.events
                }
            }
            pendingInteraction = workbench.sessionViews[selectedSessionID]?.pendingInteraction
            renderTranscript()
            try? await bridge.subscribeEvents(hostDeviceID: hostID, sessionID: selectedSessionID.isEmpty ? nil : selectedSessionID)
        } catch {
            if !silent { errorMessage = error.localizedDescription }
        }
    }

    private func handle(_ event: MobileCoreEvent) async {
        switch event {
        case .events(let data):
            if let envelope = try? JSONCoding.decode(EventEnvelope.self, from: data) {
                mergeEvents([envelope.event])
                renderTranscript()
            }
        case .terminalFrame(let data):
            terminalBridge.postNative(type: "terminal.frame", payload: data)
        case .workbenchPatch:
            if let hostID = selectedHost?.deviceID {
                await loadSnapshot(for: hostID, silent: true)
            }
        case .hostState, .error:
            break
        }
    }

    private func applyMesh(_ next: MeshState) {
        mesh = next
        hosts = next.hosts
        if selectedHostID.isEmpty || !hosts.contains(where: { $0.deviceID == selectedHostID }) {
            selectedHostID = hosts.first?.deviceID ?? ""
        }
        if !selectedHostID.isEmpty {
            Task { await loadSnapshot(for: selectedHostID, silent: true) }
        }
    }

    private func reconcileSelection() {
        if selectedWorkspaceID.isEmpty || !workbench.workspaces.keys.contains(selectedWorkspaceID) {
            selectedWorkspaceID = workspaces.first?.id ?? ""
        }
        if selectedSessionID.isEmpty || !workbench.sessions.keys.contains(selectedSessionID) {
            selectedSessionID = sessions.first?.id ?? ""
        }
        if selectedTerminalID.isEmpty || !workbench.terminalTabs.keys.contains(selectedTerminalID) {
            selectedTerminalID = terminalTabs.first?.terminalID ?? ""
        }
    }

    private func mergeEvents(_ events: [AstralEvent]) {
        for event in events {
            guard let sessionID = event.sessionID else { continue }
            var current = eventsBySession[sessionID] ?? []
            if !current.contains(where: { $0.seq == event.seq }) {
                current.append(event)
                current.sort { $0.seq < $1.seq }
            }
            eventsBySession[sessionID] = current
        }
    }

    private func renderTranscript() {
        let payload = TranscriptNativePayload(
            sessionKey: selectedSessionID,
            events: activeTranscriptEvents,
            empty: TranscriptEmptyState(
                title: selectedSessionID.isEmpty ? "Select a session" : "No transcript",
                subtitle: selectedSessionID.isEmpty ? "Choose a Host, workspace, and session." : "Events will appear here as the Host streams them."
            )
        )
        if let data = try? JSONCoding.encode(payload) {
            transcriptBridge.postNative(type: "transcript.events", payload: data)
        }
    }

    private func loadStoredIdentity() throws -> StoredIdentity? {
        guard let data = try keychain.load(storedIdentityAccount) else { return nil }
        return try JSONCoding.decode(StoredIdentity.self, from: data)
    }

    private func saveStoredIdentity(_ identity: StoredIdentity) throws {
        try keychain.save(JSONCoding.encode(identity), account: storedIdentityAccount)
    }

    private func loadCloudSession() throws -> CloudSession? {
        guard let data = try keychain.load(cloudSessionAccount) else { return nil }
        return try JSONCoding.decode(CloudSession.self, from: data)
    }

    private func saveCloudSession(_ session: CloudSession) throws {
        try keychain.save(JSONCoding.encode(session), account: cloudSessionAccount)
    }

    private func sortByUpdated<T>(_ values: [T]) -> [T] {
        values.sorted { left, right in
            dateString(left) > dateString(right)
        }
    }

    private func dateString<T>(_ value: T) -> String {
        if let workspace = value as? Workspace { return workspace.updatedAt ?? workspace.createdAt ?? "" }
        if let session = value as? SessionRecord { return session.updatedAt ?? session.createdAt ?? "" }
        if let terminal = value as? TerminalTab { return String(terminal.outputSeq ?? 0) }
        return ""
    }
}

private struct EventEnvelope: Codable {
    var seq: Int
    var event: AstralEvent
}

private struct TranscriptNativePayload: Codable {
    var sessionKey: String
    var events: [AstralEvent]
    var empty: TranscriptEmptyState
}

private struct TranscriptEmptyState: Codable {
    var title: String
    var subtitle: String
}
