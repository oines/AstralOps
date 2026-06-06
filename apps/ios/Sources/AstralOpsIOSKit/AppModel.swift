import Foundation
import SwiftUI
import UIKit

enum AppModelError: Error, LocalizedError {
    case missingHost
    case missingWorkspace
    case missingSession
    case attachmentUnavailable

    var errorDescription: String? {
        switch self {
        case .missingHost:
            return "Select a Host first."
        case .missingWorkspace:
            return "Select a workspace first."
        case .missingSession:
            return "Select a session first."
        case .attachmentUnavailable:
            return "Attachment data is unavailable."
        }
    }
}

@MainActor
final class AppModel: ObservableObject {
    enum Page: Int {
        case transcript = 0
        case files = 1
        case terminal = 2
    }

    @Published var page: Page = .transcript
    @Published var identity: DeviceIdentity?
    @Published var cloudSession: CloudSession?
    @Published var mesh: MeshState?
    @Published var hosts: [RemoteHostRecord] = []
    @Published var workbench: WorkbenchState = .empty
    @Published var globalWorkbenches: [String: WorkbenchState] = [:]
    @Published var selectedHostID = ""
    @Published var selectedWorkspaceID = ""
    @Published var selectedSessionID = ""
    @Published var selectedTerminalID = ""
    @Published var composerText = ""
    @Published var errorMessage = ""
    @Published var isBusy = false
    @Published var settingsPresented = false
    @Published var showSideMenu = false
    @Published var composerActionsPresented = false
    @Published var pendingInteraction: PendingInteractionView?
    @Published var forceRelayOnly = false
    @Published var workspaceCreatorPresented = false
    @Published var sessionCreatorPresented = false
    @Published var hostBrowserPresented = false
    @Published var selectedRunModel = ""
    @Published var selectedReasoningEffort = ""
    @Published var selectedPermissionMode = "default"
    @Published var composerAttachments: [ControlAttachmentHandle] = []
    @Published var composerUploadCount = 0
    @Published var workspaceFiles: WorkspaceFilesReadResult?
    @Published var selectedFilePath = ""
    @Published var fileEditorText = ""
    @Published var execCommand = ""
    @Published var execResult: WorkspaceExecResult?
    @Published var trustGrants: [TrustGrant] = []
    @Published var pairingRequests: [PairingRequest] = []
    @Published var mediaPreview: MediaReadResult?

    let transcriptBridge = WebViewBridge()
    let terminalBridge = WebViewBridge()

    private let bridge: MobileCoreBridge
    private let keychain = KeychainStore(service: "dev.oines.astralops.ios")
    private var storedIdentity: StoredIdentity?
    private var eventsBySession: [String: [AstralEvent]] = [:]
    private var eventTask: Task<Void, Never>?
    private var storedSelection: StoredControllerSelection
    private var sessionEventLoads = Set<String>()
    private var terminalRecoveryInFlight = Set<String>()

    private let storedIdentityAccount = "stored_identity"
    private let cloudSessionAccount = "cloud_session"
    private static let storedSelectionKey = "controller_selection_v1"

    var canSendComposerInput: Bool {
        let hasText = !composerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        return composerUploadCount == 0
            && (hasText || !composerAttachments.isEmpty)
            && selectedHost != nil
            && !selectedSessionID.isEmpty
    }

    init(bridge: MobileCoreBridge = MobileCoreBridge()) {
        self.bridge = bridge
        self.storedSelection = Self.loadStoredSelection(key: Self.storedSelectionKey)
        self.forceRelayOnly = UserDefaults.standard.bool(forKey: "force_relay_only")
        eventTask = Task { [weak self] in
            guard let self else { return }
            for await event in bridge.events {
                await self.handle(event)
            }
        }
        transcriptBridge.onReady = { [weak self] in
            self?.renderTranscript()
        }
        terminalBridge.onReady = { [weak self] in
            self?.renderSelectedTerminalSurface()
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

    var allPendingInteractions: [(session: SessionRecord, interaction: PendingInteractionView)] {
        var results: [(SessionRecord, PendingInteractionView)] = []
        for session in sessions {
            if let view = workbench.sessionViews[session.id], let interaction = view.pendingInteraction {
                results.append((session, interaction))
            }
        }
        return results
    }

    var allQueuedInputs: [(session: SessionRecord, queued: QueuedInputView)] {
        var results: [(SessionRecord, QueuedInputView)] = []
        for session in sessions {
            if let view = workbench.sessionViews[session.id], let queued = view.queuedInputs {
                for q in queued {
                    results.append((session, q))
                }
            }
        }
        return results
    }

    var allGlobalTerminals: [(host: RemoteHostRecord, tab: TerminalTab)] {
        var results: [(host: RemoteHostRecord, tab: TerminalTab)] = []
        for host in hosts {
            if let w = globalWorkbenches[host.deviceID] {
                for tab in w.terminalTabs.values {
                    results.append((host, tab))
                }
            }
        }
        return results.sorted(by: { ($0.tab.outputSeq ?? 0) > ($1.tab.outputSeq ?? 0) })
    }

    var allGlobalSessions: [(host: RemoteHostRecord, session: SessionRecord)] {
        var results: [(host: RemoteHostRecord, session: SessionRecord)] = []
        for host in hosts {
            if let w = globalWorkbenches[host.deviceID] {
                for session in w.sessions.values {
                    results.append((host, session))
                }
            }
        }
        return results.sorted(by: { ($0.session.updatedAt ?? "") > ($1.session.updatedAt ?? "") })
    }

    var allGlobalPending: [(host: RemoteHostRecord, interaction: PendingInteractionView, session: SessionRecord)] {
        var results: [(RemoteHostRecord, PendingInteractionView, SessionRecord)] = []
        for host in hosts {
            if let w = globalWorkbenches[host.deviceID] {
                for session in w.sessions.values {
                    if let view = w.sessionViews[session.id], let interaction = view.pendingInteraction {
                        results.append((host, interaction, session))
                    }
                }
            }
        }
        return results
    }

    var selectedSession: SessionRecord? {
        workbench.sessions[selectedSessionID]
    }

    var activeAgentInfo: AgentInfo? {
        guard let agent = selectedSession?.agent?.trimmingCharacters(in: .whitespacesAndNewlines),
              !agent.isEmpty
        else {
            return nil
        }
        return workbench.agents?[agent]
    }

    var modelOptions: [ModelInfo] {
        Self.dedupedModelOptions(activeAgentInfo?.models ?? [], currentModel: activeAgentInfo?.currentModel)
    }

    var defaultModelMenuTitle: String {
        let current = activeAgentInfo?.currentModel?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return current.isEmpty ? "Host default" : "Host default (\(current))"
    }

    var selectedWorkspace: Workspace? {
        workbench.workspaces[selectedWorkspaceID]
    }

    var selectedWorkspaceConnection: WorkspaceConnection? {
        workbench.workspaceConnections?[selectedWorkspaceID]
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
                do {
                    let nextMesh = try await bridge.setCloudSession(CloudSessionInput(storedIdentity: storedIdentity, session: cloudSession, baseURL: nil, loginCode: nil))
                    applyMesh(nextMesh)
                } catch {
                    clearCloudViewState()
                }
            }
            renderTranscript()
        } catch {
            presentError(error)
        }
    }

    func refreshMesh(silent: Bool = false) async {
        guard cloudSession != nil else {
            clearCloudViewState()
            renderTranscript()
            return
        }
        if !silent { isBusy = true }
        defer { if !silent { isBusy = false } }
        do {
            let next = try await bridge.refreshMesh()
            applyMesh(next)
        } catch {
            if !silent { presentError(error) }
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
            presentError(error)
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
            presentError(error)
        }
    }

    func toggleForceRelayOnly(_ enabled: Bool) {
        forceRelayOnly = enabled
        UserDefaults.standard.set(enabled, forKey: "force_relay_only")
        Task { await start() }
    }

    func selectHost(_ host: RemoteHostRecord) {
        setActiveHost(host)
        reconcileSelection()
        saveCurrentSelection()
        renderTranscript()
        Task { await loadSnapshot(for: host.deviceID) }
    }

    func selectWorkspace(_ workspace: Workspace) {
        selectedWorkspaceID = workspace.id
        selectedSessionID = restoredSessionID(for: workspace.id) ?? sessions.first?.id ?? ""
        selectedTerminalID = terminalTabs.first?.terminalID ?? ""
        reconcileRunModelSelection()
        saveCurrentSelection()
        renderTranscript()
    }

    func selectSession(_ session: SessionRecord) {
        selectedWorkspaceID = session.workspaceID
        selectedSessionID = session.id
        selectedTerminalID = terminalTabs.first?.terminalID ?? ""
        pendingInteraction = workbench.sessionViews[session.id]?.pendingInteraction
        reconcileRunModelSelection()
        saveCurrentSelection()
        renderTranscript()
        let sessionID = session.id
        Task {
            if let hostID = selectedHost?.deviceID {
                try? await bridge.subscribeEvents(hostDeviceID: hostID, sessionID: sessionID)
                if selectedHostID == hostID, selectedSessionID == sessionID {
                    await loadSnapshot(for: hostID, silent: true)
                    await loadLatestSessionEvents(sessionID: sessionID, hostID: hostID)
                }
            }
        }
    }

    func selectSession(_ session: SessionRecord, on host: RemoteHostRecord) {
        setActiveHost(host)
        selectSession(session)
    }

    func selectTerminal(_ tab: TerminalTab, on host: RemoteHostRecord? = nil) async {
        if let host {
            setActiveHost(host)
        }
        selectedWorkspaceID = tab.workspaceID
        selectedTerminalID = tab.terminalID
        page = .terminal
        saveCurrentSelection()
        await attachTerminal(tab)
        if let hostID = selectedHost?.deviceID {
            await loadSnapshot(for: hostID, silent: true)
        }
    }

    func requestPairing(_ host: RemoteHostRecord) async {
        do {
            _ = try await bridge.requestPairing(hostDeviceID: host.deviceID)
            await refreshMesh(silent: false)
        } catch {
            presentError(error)
        }
    }

    func sendComposerText() async {
        let text = composerText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard composerUploadCount == 0, (!text.isEmpty || !composerAttachments.isEmpty), let host = selectedHost, !selectedSessionID.isEmpty else { return }
        let previousAttachments = composerAttachments
        let options = inputOptions()
        composerText = ""
        composerAttachments = []
        do {
            try await bridge.sendInput(hostDeviceID: host.deviceID, sessionID: selectedSessionID, text: text, options: options)
        } catch {
            composerText = text
            composerAttachments = previousAttachments
            presentError(error)
        }
    }

    func beginComposerUpload() {
        composerUploadCount += 1
    }

    func finishComposerUpload() {
        composerUploadCount = max(0, composerUploadCount - 1)
    }

    func addComposerAttachment(data: Data, name: String, mimeType: String?, kind: String, tracksUpload: Bool = true) async {
        do {
            guard !selectedSessionID.isEmpty else { throw AppModelError.missingSession }
            if tracksUpload {
                beginComposerUpload()
            }
            defer {
                if tracksUpload {
                    finishComposerUpload()
                }
            }
            let handle = try await ingestAttachment(data: data, name: name, mimeType: mimeType, kind: kind)
            if !composerAttachments.contains(where: { $0.id == handle.id }) {
                composerAttachments.append(handle)
            }
        } catch {
            presentError(error)
        }
    }

    func removeComposerAttachment(_ attachment: ControlAttachmentHandle) {
        composerAttachments.removeAll { $0.id == attachment.id }
    }

    func browseHostFileSystem(target: String, path: String, ssh: SSHConfig? = nil) async throws -> HostFileSystemBrowseResult {
        var params: [String: JSONValue] = [
            "target": .string(target),
            "path": .string(path)
        ]
        if let ssh {
            params["ssh"] = try jsonValue(ssh)
        }
        return try await controlResult(
            HostFileSystemBrowseResult.self,
            capability: .capabilityHostFileSystemBrowse,
            action: .hostFileSystemBrowse,
            params: .object(params)
        )
    }

    func createWorkspace(name: String, target: String, localCWD: String, ssh: SSHConfig?) async {
        do {
            var request = CreateWorkspaceRequest(
                name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                target: target,
                agent: nil,
                localCWD: localCWD.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty,
                ssh: ssh
            )
            if request.name.isEmpty {
                request.name = request.localCWD.map { $0.lastPathComponent }.flatMap { $0.nonEmpty } ?? "Workspace"
            }
            let workspace = try await controlResult(
                Workspace.self,
                capability: .capabilityCoreControl,
                action: .workspaceCreate,
                params: jsonValue(request)
            )
            workspaceCreatorPresented = false
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
            selectWorkspace(workspace)
        } catch {
            presentError(error)
        }
    }

    func connectSelectedWorkspace() async {
        await workspaceReferenceAction(action: .workspaceConnect, workspaceID: selectedWorkspaceID)
    }

    func disconnectSelectedWorkspace() async {
        await workspaceReferenceAction(action: .workspaceDisconnect, workspaceID: selectedWorkspaceID)
    }

    func connectWorkspace(_ workspace: Workspace) async {
        await workspaceReferenceAction(action: .workspaceConnect, workspaceID: workspace.id)
    }

    func disconnectWorkspace(_ workspace: Workspace) async {
        await workspaceReferenceAction(action: .workspaceDisconnect, workspaceID: workspace.id)
    }

    func deleteSelectedWorkspace() async {
        if let workspace = selectedWorkspace {
            await deleteWorkspace(workspace)
        } else {
            await workspaceReferenceAction(action: .workspaceDelete, workspaceID: selectedWorkspaceID)
        }
    }

    func deleteWorkspace(_ workspace: Workspace) async {
        do {
            _ = try await controlValue(
                capability: .capabilityCoreControl,
                action: .workspaceDelete,
                params: .object(["workspace_id": .string(workspace.id)])
            )
            let removedSessionIDs = workbench.sessions.values
                .filter { $0.workspaceID == workspace.id }
                .map(\.id)
            for sessionID in removedSessionIDs {
                eventsBySession.removeValue(forKey: sessionID)
            }
            if selectedWorkspaceID == workspace.id {
                selectedWorkspaceID = ""
                selectedSessionID = ""
                selectedTerminalID = ""
            }
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
            renderTranscript()
        } catch {
            presentError(error)
        }
    }

    func createSession(agent: String = "") async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            var params: [String: JSONValue] = ["workspace_id": .string(selectedWorkspaceID)]
            if !agent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                params["agent"] = .string(agent.trimmingCharacters(in: .whitespacesAndNewlines))
            }
            let session = try await controlResult(
                SessionRecord.self,
                capability: .capabilityCoreControl,
                action: .sessionCreate,
                params: .object(params)
            )
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
            selectSession(session)
            sessionCreatorPresented = false
            page = .transcript
        } catch {
            presentError(error)
        }
    }

    func deleteSession(_ session: SessionRecord) async {
        do {
            _ = try await controlValue(
                capability: .capabilityCoreControl,
                action: .sessionDelete,
                params: .object(["session_id": .string(session.id)])
            )
            eventsBySession.removeValue(forKey: session.id)
            if selectedSessionID == session.id {
                selectedSessionID = ""
            }
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
            renderTranscript()
        } catch {
            presentError(error)
        }
    }

    func interruptSelectedSession() async {
        do {
            guard !selectedSessionID.isEmpty else { throw AppModelError.missingSession }
            _ = try await controlValue(
                capability: .capabilityCoreControl,
                action: .interrupt,
                params: .object(["session_id": .string(selectedSessionID)])
            )
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
        } catch {
            presentError(error)
        }
    }

    func cancelQueuedInput(_ queued: QueuedInputView) async {
        await queueAction(action: .queueCancel, queued: queued)
    }

    func steerQueuedInput(_ queued: QueuedInputView) async {
        await queueAction(action: .queueSteer, queued: queued)
    }

    func forkSession(eventSeq: Int) async {
        do {
            guard !selectedSessionID.isEmpty else { throw AppModelError.missingSession }
            let session = try await controlResult(
                SessionRecord.self,
                capability: .capabilityCoreControl,
                action: .sessionFork,
                params: .object(["session_id": .string(selectedSessionID), "event_seq": .number(eventSeq)])
            )
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
            selectSession(session)
        } catch {
            presentError(error)
        }
    }

    func editLastUserMessage(eventSeq: Int, input: String) async {
        do {
            guard !selectedSessionID.isEmpty else { throw AppModelError.missingSession }
            var params = runOptions()
            params["session_id"] = .string(selectedSessionID)
            params["event_seq"] = .number(eventSeq)
            params["input"] = .string(input)
            _ = try await controlValue(capability: .capabilitySessionEdit, action: .sessionEdit, params: .object(params))
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
        } catch {
            presentError(error)
        }
    }

    func handleTranscriptAction(_ payload: JSONValue) {
        guard let object = payload.objectValue, let type = object["type"]?.stringValue else { return }
        let sessionID = object["session_id"]?.stringValue ?? selectedSessionID
        let eventSeq = object["event_seq"]?.intValue ?? 0
        switch type {
        case "fork_session":
            rememberSessionID(sessionID)
            Task { await forkSession(eventSeq: eventSeq) }
        case "edit_user_message":
            let input = object["input"]?.stringValue ?? ""
            rememberSessionID(sessionID)
            Task { await editLastUserMessage(eventSeq: eventSeq, input: input) }
        case "open_source_session":
            if let sourceSessionID = object["source_session_id"]?.stringValue, !sourceSessionID.isEmpty {
                rememberSessionID(sourceSessionID)
                renderTranscript()
            }
        case "open_media":
            let mediaID = object["media_id"]?.stringValue ?? ""
            if !mediaID.isEmpty {
                Task { await previewMedia(sessionID: sessionID, eventSeq: eventSeq, mediaID: mediaID, download: false) }
            }
        case "open_files":
            page = .files
        default:
            break
        }
    }

    func loadWorkspaceFiles(path: String = "", mode: String = "auto") async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            let result = try await controlResult(
                WorkspaceFilesReadResult.self,
                capability: .capabilityWorkspaceFilesRead,
                action: .workspaceFilesRead,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "path": .string(path),
                    "mode": .string(mode),
                    "max_bytes": .number(512 * 1024)
                ])
            )
            workspaceFiles = result
            if result.kind == "file" {
                selectedFilePath = result.path
                fileEditorText = result.textContent
            } else {
                selectedFilePath = ""
                fileEditorText = ""
            }
        } catch {
            presentError(error)
        }
    }

    func openWorkspaceEntry(_ entry: WorkspaceFileEntry) async {
        await loadWorkspaceFiles(path: entry.path, mode: entry.kind == "file" ? "text" : "auto")
    }

    func saveSelectedFile() async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            guard !selectedFilePath.isEmpty else { return }
            _ = try await controlValue(
                capability: .capabilityWorkspaceFilesWrite,
                action: .workspaceFilesWrite,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "path": .string(selectedFilePath),
                    "content": .string(fileEditorText),
                    "create_parents": .bool(true)
                ])
            )
            await loadWorkspaceFiles(path: selectedFilePath, mode: "text")
        } catch {
            presentError(error)
        }
    }

    func applyReplace(oldString: String, newString: String, replaceAll: Bool) async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            guard !selectedFilePath.isEmpty else { return }
            _ = try await controlValue(
                capability: .capabilityWorkspaceFilesWrite,
                action: .workspaceFilesApplyPatch,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "path": .string(selectedFilePath),
                    "edits": .array([
                        .object([
                            "old_string": .string(oldString),
                            "new_string": .string(newString),
                            "replace_all": .bool(replaceAll)
                        ])
                    ])
                ])
            )
            await loadWorkspaceFiles(path: selectedFilePath, mode: "text")
        } catch {
            presentError(error)
        }
    }

    func deleteWorkspacePath(path: String, recursive: Bool = false) async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            _ = try await controlValue(
                capability: .capabilityWorkspaceFilesWrite,
                action: .workspaceFilesDelete,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "path": .string(path),
                    "recursive": .bool(recursive)
                ])
            )
            await loadWorkspaceFiles(path: parentPath(path))
        } catch {
            presentError(error)
        }
    }

    func moveWorkspacePath(path: String, destinationPath: String, overwrite: Bool = false) async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            _ = try await controlValue(
                capability: .capabilityWorkspaceFilesWrite,
                action: .workspaceFilesMove,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "path": .string(path),
                    "destination_path": .string(destinationPath),
                    "overwrite": .bool(overwrite)
                ])
            )
            await loadWorkspaceFiles(path: parentPath(destinationPath))
        } catch {
            presentError(error)
        }
    }

    func runWorkspaceExec() async {
        do {
            guard !selectedWorkspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            let command = execCommand.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !command.isEmpty else { return }
            execResult = try await controlResult(
                WorkspaceExecResult.self,
                capability: .capabilityWorkspaceExec,
                action: .workspaceExec,
                params: .object([
                    "workspace_id": .string(selectedWorkspaceID),
                    "command": .string(command),
                    "cwd": .string(workspaceFiles?.kind == "directory" ? workspaceFiles?.path : parentPath(selectedFilePath)),
                    "timeout_ms": .number(30_000)
                ])
            )
        } catch {
            presentError(error)
        }
    }

    func loadHostManagement() async {
        do {
            let trust = try await controlResult(HostTrustListResult.self, capability: .capabilityHostManage, action: .hostTrustList)
            let pairing = try await controlResult(PairingRequestListResult.self, capability: .capabilityHostManage, action: .hostPairingList)
            trustGrants = trust.grants
            pairingRequests = pairing.requests
        } catch {
            presentError(error)
        }
    }

    func revokeTrust(_ grant: TrustGrant) async {
        do {
            _ = try await controlValue(
                capability: .capabilityHostManage,
                action: .hostTrustRevoke,
                params: .object(["controller_device_id": .string(grant.controllerDeviceID)])
            )
            await loadHostManagement()
        } catch {
            presentError(error)
        }
    }

    func resolvePairingRequest(_ request: PairingRequest, approve: Bool) async {
        do {
            _ = try await controlValue(
                capability: .capabilityHostManage,
                action: approve ? .hostPairingApprove : .hostPairingDeny,
                params: .object(["request_id": .string(request.requestID)])
            )
            await loadHostManagement()
            await refreshMesh(silent: true)
        } catch {
            presentError(error)
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
            renderTerminal(streamInfo: info)
            await refreshTerminals()
        } catch {
            presentError(error)
        }
    }

    func activateTerminalPage() async {
        guard selectedHost != nil else { return }
        await refreshTerminals()
        guard let tab = terminalTabs.first(where: { $0.terminalID == selectedTerminalID }) ?? terminalTabs.first else { return }
        await attachTerminal(tab)
    }

    func refreshTerminals() async {
        do {
            let tabs = try await bridge.listTerminals(hostDeviceID: try requireHost().deviceID)
            var next = workbench
            next.terminalTabs = Dictionary(uniqueKeysWithValues: tabs.map { ($0.terminalID, $0) })
            workbench = next
            reconcileSelection()
        } catch {
            presentError(error)
        }
    }

    func attachTerminal(_ tab: TerminalTab) async {
        do {
            selectedWorkspaceID = tab.workspaceID
            selectedTerminalID = tab.terminalID
            let info = try await bridge.attachTerminal(hostDeviceID: try requireHost().deviceID, terminalID: tab.terminalID)
            renderTerminal(tab: tab, streamInfo: info)
        } catch {
            presentError(error)
        }
    }

    func detachSelectedTerminal() async {
        do {
            guard !selectedTerminalID.isEmpty else { return }
            try await bridge.detachTerminal(hostDeviceID: try requireHost().deviceID, terminalID: selectedTerminalID)
            await refreshTerminals()
        } catch {
            presentError(error)
        }
    }

    func closeSelectedTerminal() async {
        do {
            guard !selectedTerminalID.isEmpty else { return }
            try await bridge.terminalClose(hostDeviceID: try requireHost().deviceID, terminalID: selectedTerminalID)
            await refreshTerminals()
        } catch {
            presentError(error)
        }
    }

    func sendTerminalInput(_ data: String) {
        let terminalID = selectedTerminalID
        guard let host = selectedHost, !terminalID.isEmpty else { return }
        Task {
            do {
                try await bridge.terminalInput(hostDeviceID: host.deviceID, terminalID: terminalID, data: data)
            } catch {
                if Self.isTerminalViewerLifecycleError(error) {
                    await recoverTerminalViewer(terminalID: terminalID, retryInput: data)
                } else {
                    presentError(error)
                }
            }
        }
    }

    func terminalResize(terminalID: String, cols: Int, rows: Int) {
        guard let host = selectedHost, !terminalID.isEmpty else { return }
        Task {
            do {
                try await bridge.terminalResize(hostDeviceID: host.deviceID, terminalID: terminalID, cols: cols, rows: rows)
            } catch {
                if Self.isTerminalViewerLifecycleError(error) {
                    await recoverTerminalViewer(terminalID: terminalID)
                } else {
                    presentError(error)
                }
            }
        }
    }

    func terminalHeartbeatAck(terminalID: String, heartbeatSeq: Int, renderedSeq: Int) {
        guard let host = selectedHost, !terminalID.isEmpty else { return }
        Task {
            do {
                try await bridge.terminalHeartbeatAck(hostDeviceID: host.deviceID, terminalID: terminalID, heartbeatSeq: heartbeatSeq, renderedSeq: renderedSeq)
            } catch {
                if Self.isTerminalViewerLifecycleError(error) {
                    await recoverTerminalViewer(terminalID: terminalID)
                } else {
                    presentError(error)
                }
            }
        }
    }

    func dismissTerminalKeyboard() {
        terminalBridge.postNative(type: "terminal.keyboard.dismiss", payload: .object([:]))
        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
    }

    func respond(to interaction: PendingInteractionView, action: InteractionActionView, feedback: String = "") async {
        var response: [String: JSONValue] = ["action_id": .string(action.id)]
        if !feedback.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            response["feedback"] = .string(feedback)
        }
        await respond(to: interaction, response: .object(response))
    }

    func respond(to interaction: PendingInteractionView, response: JSONValue) async {
        guard let host = selectedHost else { return }
        do {
            try await bridge.respondInteraction(hostDeviceID: host.deviceID, interactionID: interaction.id, response: response)
            pendingInteraction = nil
            await loadSnapshot(for: host.deviceID, silent: true)
        } catch {
            presentError(error)
        }
    }

    func previewMedia(sessionID: String, eventSeq: Int, mediaID: String, download: Bool) async {
        do {
            mediaPreview = try await loadMedia(sessionID: sessionID, eventSeq: eventSeq, mediaID: mediaID, download: download)
        } catch {
            presentError(error)
        }
    }

    func loadMedia(sessionID: String, eventSeq: Int, mediaID: String, download: Bool = false) async throws -> MediaReadResult {
        try await controlResult(
            MediaReadResult.self,
            capability: download ? .capabilityMediaDownload : .capabilityMediaRead,
            action: download ? .mediaDownload : .mediaRead,
            params: .object([
                "session_id": .string(sessionID),
                "event_seq": .number(eventSeq),
                "media_id": .string(mediaID)
            ])
        )
    }

    func mediaResponse(for url: URL) async throws -> WebViewMediaResponse {
        let items = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems ?? []
        func query(_ name: String) -> String {
            items.first { $0.name == name }?.value ?? ""
        }
        let sessionID = query("session_id")
        let eventSeq = Int(query("event_seq")) ?? 0
        let mediaID = query("media_id")
        let download = query("download") == "1" || query("download") == "true"
        let result = try await loadMedia(sessionID: sessionID, eventSeq: eventSeq, mediaID: mediaID, download: download)
        guard let data = Data(base64Encoded: result.contentBase64) else {
            throw MobileCoreBridgeError.controlError(ControlErrorEnvelope(status: 400, code: "media_content_invalid", message: "Media content is unavailable."))
        }
        return WebViewMediaResponse(data: data, mimeType: result.mimeType ?? "application/octet-stream")
    }

    private func ingestAttachment(data: Data, name: String, mimeType: String?, kind: String) async throws -> ControlAttachmentHandle {
        let start = try await controlResult(
            AttachmentIngestStartResult.self,
            capability: .capabilityAttachmentIngest,
            action: .attachmentIngestStart,
            params: .object([
                "session_id": .string(selectedSessionID),
                "name": .string(name),
                "kind": .string(kind == "image" ? "image" : "file"),
                "mime_type": .string(mimeType ?? "application/octet-stream"),
                "detail": .string(kind == "image" ? "high" : ""),
                "size": .number(data.count)
            ])
        )
        let maxChunkSize = max(1, min(start.chunkMaxBytes, 4 * 1024 * 1024))
        var offset = 0
        var seq = 1
        while offset < data.count {
            let end = min(offset + maxChunkSize, data.count)
            let chunk = data.subdata(in: offset..<end).base64EncodedString()
            _ = try await controlResult(
                AttachmentIngestChunkResult.self,
                capability: .capabilityAttachmentIngest,
                action: .attachmentIngestChunk,
                params: .object([
                    "session_id": .string(selectedSessionID),
                    "upload_id": .string(start.uploadID),
                    "seq": .number(seq),
                    "offset": .number(offset),
                    "data_base64": .string(chunk)
                ])
            )
            offset = end
            seq += 1
        }
        let finish = try await controlResult(
            AttachmentIngestFinishResult.self,
            capability: .capabilityAttachmentIngest,
            action: .attachmentIngestFinish,
            params: .object([
                "session_id": .string(selectedSessionID),
                "upload_id": .string(start.uploadID)
            ])
        )
        return finish.attachment
    }

    private func inputOptions() -> JSONValue {
        var options = runOptions()
        if !composerAttachments.isEmpty {
            options["attachments"] = .array(composerAttachments.map(attachmentInputPayload))
        }
        return .object(options)
    }

    func resetRunOptions() {
        selectedRunModel = ""
        selectedReasoningEffort = ""
        selectedPermissionMode = "default"
    }

    private func runOptions() -> [String: JSONValue] {
        var options: [String: JSONValue] = [:]
        let model = selectedRunModel.trimmingCharacters(in: .whitespacesAndNewlines)
        let reasoning = selectedReasoningEffort.trimmingCharacters(in: .whitespacesAndNewlines)
        let permission = selectedPermissionMode.trimmingCharacters(in: .whitespacesAndNewlines)
        if !model.isEmpty {
            options["model"] = .string(model)
        }
        if !reasoning.isEmpty {
            options["reasoning_effort"] = .string(reasoning)
        }
        if !permission.isEmpty && permission != "default" {
            options["permission_mode"] = .string(permission)
        }
        return options
    }

    private func attachmentInputPayload(_ attachment: ControlAttachmentHandle) -> JSONValue {
        var payload: [String: JSONValue] = [
            "id": .string(attachment.id),
            "kind": .string(attachment.kind),
            "name": .string(attachment.name)
        ]
        if let mimeType = attachment.mimeType {
            payload["mime_type"] = .string(mimeType)
        }
        if let size = attachment.size {
            payload["size"] = .number(size)
        }
        if let detail = attachment.detail {
            payload["detail"] = .string(detail)
        }
        return .object(payload)
    }

    private func workspaceReferenceAction(action: GeneratedProtocol.ControlAction, workspaceID: String) async {
        do {
            guard !workspaceID.isEmpty else { throw AppModelError.missingWorkspace }
            _ = try await controlValue(
                capability: .capabilityCoreControl,
                action: action,
                params: .object(["workspace_id": .string(workspaceID)])
            )
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
        } catch {
            presentError(error)
        }
    }

    private func queueAction(action: GeneratedProtocol.ControlAction, queued: QueuedInputView) async {
        do {
            _ = try await controlValue(
                capability: .capabilityCoreControl,
                action: action,
                params: .object([
                    "session_id": .string(queued.sessionID),
                    "queue_id": .string(queued.id)
                ])
            )
            await loadSnapshot(for: try requireHost().deviceID, silent: true)
        } catch {
            presentError(error)
        }
    }

    private func controlValue(
        capability: GeneratedProtocol.ControlCapability,
        action: GeneratedProtocol.ControlAction,
        params: JSONValue = .object([:])
    ) async throws -> JSONValue {
        let host = try requireHost()
        return try await bridge.controlRequest(hostDeviceID: host.deviceID, capability: capability.rawValue, action: action.rawValue, params: params)
    }

    private func controlResult<T: Decodable>(
        _ type: T.Type,
        capability: GeneratedProtocol.ControlCapability,
        action: GeneratedProtocol.ControlAction,
        params: JSONValue = .object([:])
    ) async throws -> T {
        let value = try await controlValue(capability: capability, action: action, params: params)
        return try decodeValue(type, from: value)
    }

    private func requireHost() throws -> RemoteHostRecord {
        guard let host = selectedHost else { throw AppModelError.missingHost }
        return host
    }

    private func jsonValue<T: Encodable>(_ value: T) throws -> JSONValue {
        try JSONCoding.decode(JSONValue.self, from: JSONCoding.encode(value))
    }

    private func decodeValue<T: Decodable>(_ type: T.Type, from value: JSONValue) throws -> T {
        try JSONCoding.decode(type, from: JSONCoding.encode(value))
    }

    private func parentPath(_ path: String) -> String {
        let trimmed = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed != "." else { return "" }
        let parent = (trimmed as NSString).deletingLastPathComponent
        return parent == "." ? "" : parent
    }

    private func renderTerminal(tab: TerminalTab, streamInfo: JSONValue? = nil) {
        let stream = streamInfo?.objectValue ?? [:]
        let terminalID = stream["terminal_id"]?.stringValue ?? tab.terminalID
        let outputSeq = stream["output_seq"]?.intValue ?? tab.outputSeq ?? 0
        let hasInputLease = !(stream["viewer_id"]?.stringValue ?? "").isEmpty && !(stream["input_lease_id"]?.stringValue ?? "").isEmpty
        let isLive = hasInputLease || tab.canInput == true
        let state = isLive ? "live" : (tab.status ?? "")
        let message = isLive ? "" : (tab.status ?? "Input paused")
        terminalBridge.postNative(
            type: "terminal.render",
            payload: .object([
                "terminalId": .string(terminalID),
                "output": .string(""),
                "output_seq": .number(outputSeq),
                "replayFromStart": .bool(hasInputLease),
                "canInput": .bool(isLive),
                "state": .string(state),
                "message": .string(message)
            ])
        )
    }

    private func renderTerminal(streamInfo: JSONValue) {
        guard let terminalID = streamInfo.objectValue?["terminal_id"]?.stringValue else { return }
        let tab = workbench.terminalTabs[terminalID] ?? TerminalTab(
            terminalID: terminalID,
            workspaceID: selectedWorkspaceID,
            target: nil,
            shell: streamInfo.objectValue?["shell"]?.stringValue,
            cwd: streamInfo.objectValue?["cwd"]?.stringValue,
            status: "live",
            writerDeviceID: nil,
            outputSeq: streamInfo.objectValue?["output_seq"]?.intValue,
            canInput: true
        )
        renderTerminal(tab: tab, streamInfo: streamInfo)
    }

    private func renderSelectedTerminalSurface() {
        if let tab = workbench.terminalTabs[selectedTerminalID] ?? terminalTabs.first {
            renderTerminal(tab: tab)
            return
        }
        terminalBridge.postNative(
            type: "terminal.render",
            payload: .object([
                "terminalId": .string(""),
                "output": .string(""),
                "canInput": .bool(false),
                "state": .string("idle"),
                "message": .string("No terminal")
            ])
        )
    }

    private func sourceSessionExists(for session: SessionRecord?) -> Bool {
        guard let sourceID = session?.forkedFromSessionID, !sourceID.isEmpty else { return false }
        return workbench.sessions[sourceID] != nil
    }

    func presentError(_ error: Error) {
        errorMessage = Self.errorDisplayMessage(for: error)
    }

    static func errorDisplayMessage(for error: Error) -> String {
        let message = error.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines)
        let lower = message.lowercased()
        if lower.contains("cloud account_token is required") || lower.contains("cloud session is not configured") {
            return "Cloud session is missing. Sign in again."
        }
        if lower.contains("oauth_provider_not_configured") {
            return "This sign-in provider is not configured. Use GitHub."
        }
        if lower.contains("cloud request failed") || lower.contains("cloud-astralops.oines.dev") || lower.contains("/v1/") || lower.contains("eof") {
            return "Cloud request failed. Check your network and try again."
        }
        if lower.contains("context deadline exceeded") || lower.contains("remote control request failed") && lower.contains("deadline") {
            return "Host request timed out. Check the connection and try again."
        }
        return message.isEmpty ? "Something went wrong." : message
    }

    static func isTerminalViewerLifecycleError(_ error: Error) -> Bool {
        if case MobileCoreBridgeError.controlError(let envelope) = error {
            if envelope.code == "terminal_viewer_not_live" {
                return true
            }
            let message = envelope.message.lowercased()
            return message == "terminal viewer is not live"
                || message == "terminal viewer is not attached"
                || message == "terminal input requires an attached healthy viewer"
        }
        let message = error.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return message == "terminal viewer is not live"
            || message == "terminal viewer is not attached"
            || message == "terminal input requires an attached healthy viewer"
    }

    func terminalShortcut(_ value: String) {
        sendTerminalInput(value)
    }

    private func recoverTerminalViewer(terminalID: String, retryInput: String? = nil) async {
        guard terminalID == selectedTerminalID else { return }
        guard !terminalRecoveryInFlight.contains(terminalID) else { return }
        guard let tab = workbench.terminalTabs[terminalID] ?? terminalTabs.first(where: { $0.terminalID == terminalID }) else { return }
        terminalRecoveryInFlight.insert(terminalID)
        defer { terminalRecoveryInFlight.remove(terminalID) }
        do {
            selectedWorkspaceID = tab.workspaceID
            selectedTerminalID = tab.terminalID
            let hostID = try requireHost().deviceID
            let info = try await bridge.attachTerminal(hostDeviceID: hostID, terminalID: terminalID)
            renderTerminal(tab: tab, streamInfo: info)
            if let retryInput, !retryInput.isEmpty {
                try await bridge.terminalInput(hostDeviceID: hostID, terminalID: terminalID, data: retryInput)
            }
        } catch {
            if !Self.isTerminalViewerLifecycleError(error) {
                presentError(error)
            }
        }
    }

    private func loadSnapshot(for hostID: String, silent: Bool = false) async {
        if !silent { isBusy = true }
        defer { if !silent { isBusy = false } }
        do {
            _ = try await bridge.openHostSession(hostDeviceID: hostID)
            let snapshot = try await bridge.snapshot(hostDeviceID: hostID)
            globalWorkbenches[hostID] = snapshot.workbench ?? .empty
            if let nextWorkbench = snapshot.workbench {
                guard selectedHostID == hostID else { return }
                workbench = nextWorkbench
                reconcileSelection()
            }
            if let initial = snapshot.initialSessionEvents {
                mergeEvents(initial)
            }
            if let events = snapshot.events {
                mergeEvents(events)
            }
            pendingInteraction = workbench.sessionViews[selectedSessionID]?.pendingInteraction
            reconcileRunModelSelection()
            renderTranscript()
            try? await bridge.subscribeEvents(hostDeviceID: hostID, sessionID: selectedSessionID.isEmpty ? nil : selectedSessionID)
            if !selectedSessionID.isEmpty {
                await loadLatestSessionEvents(sessionID: selectedSessionID, hostID: hostID)
            }
        } catch {
            if !silent { presentError(error) }
        }
    }

    private func loadLatestSessionEvents(sessionID: String, hostID: String) async {
        guard !sessionID.isEmpty, selectedHostID == hostID else { return }
        let loadKey = "\(hostID):\(sessionID)"
        guard !sessionEventLoads.contains(loadKey) else { return }
        sessionEventLoads.insert(loadKey)
        defer { sessionEventLoads.remove(loadKey) }
        do {
            let events = try await controlResult(
                [AstralEvent].self,
                capability: .capabilityCoreRead,
                action: .events,
                params: .object([
                    "session_id": .string(sessionID),
                    "limit": .number(1000)
                ])
            )
            guard selectedHostID == hostID else { return }
            mergeEvents(events, fallbackSessionID: sessionID)
            renderTranscript()
        } catch {
            presentError(error)
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
            selectedHostID = restoredHostID(from: hosts) ?? hosts.first?.deviceID ?? ""
        }
        for host in hosts {
            Task { await loadSnapshot(for: host.deviceID, silent: true) }
        }
    }

    private func setActiveHost(_ host: RemoteHostRecord) {
        let changingHost = selectedHostID != host.deviceID
        selectedHostID = host.deviceID
        if let cachedWorkbench = globalWorkbenches[host.deviceID] {
            workbench = cachedWorkbench
        } else if changingHost {
            workbench = .empty
        }
        if changingHost {
            selectedWorkspaceID = ""
            selectedSessionID = ""
            selectedTerminalID = ""
            pendingInteraction = nil
            eventsBySession = [:]
        }
    }

    private func clearCloudViewState() {
        cloudSession = nil
        mesh = nil
        hosts = []
        workbench = .empty
        globalWorkbenches = [:]
        selectedHostID = ""
        selectedWorkspaceID = ""
        selectedSessionID = ""
        selectedTerminalID = ""
        pendingInteraction = nil
        eventsBySession = [:]
    }

    private func reconcileSelection() {
        if selectedWorkspaceID.isEmpty || !workbench.workspaces.keys.contains(selectedWorkspaceID) {
            selectedWorkspaceID = restoredWorkspaceID() ?? workspaces.first?.id ?? ""
        }
        if selectedSessionID.isEmpty || !sessionBelongsToSelectedWorkspace(selectedSessionID) {
            selectedSessionID = restoredSessionID(for: selectedWorkspaceID) ?? sessions.first?.id ?? ""
        }
        if selectedTerminalID.isEmpty || !workbench.terminalTabs.keys.contains(selectedTerminalID) {
            selectedTerminalID = terminalTabs.first?.terminalID ?? ""
        }
        reconcileRunModelSelection()
        saveCurrentSelection()
    }

    private func reconcileRunModelSelection() {
        let selected = selectedRunModel.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !selected.isEmpty else { return }
        if !modelOptions.contains(where: { $0.id == selected }) {
            selectedRunModel = ""
        }
    }

    private func restoredHostID(from hosts: [RemoteHostRecord]) -> String? {
        guard let hostID = storedSelection.hostID, hosts.contains(where: { $0.deviceID == hostID }) else {
            return nil
        }
        return hostID
    }

    private func restoredWorkspaceID() -> String? {
        guard let selection = storedSelection.hosts[selectedHostID],
              let workspaceID = selection.workspaceID,
              workbench.workspaces.keys.contains(workspaceID)
        else { return nil }
        return workspaceID
    }

    private func restoredSessionID(for workspaceID: String) -> String? {
        guard !workspaceID.isEmpty else { return nil }
        let selection = storedSelection.hosts[selectedHostID]
        let sessionID = selection?.sessionsByWorkspace[workspaceID] ?? selection?.sessionID
        guard let sessionID, sessionBelongsToWorkspace(sessionID, workspaceID: workspaceID) else {
            return nil
        }
        return sessionID
    }

    private func sessionBelongsToSelectedWorkspace(_ sessionID: String) -> Bool {
        sessionBelongsToWorkspace(sessionID, workspaceID: selectedWorkspaceID)
    }

    private func sessionBelongsToWorkspace(_ sessionID: String, workspaceID: String) -> Bool {
        guard !sessionID.isEmpty, !workspaceID.isEmpty, let session = workbench.sessions[sessionID] else {
            return false
        }
        return session.workspaceID == workspaceID
    }

    private func saveCurrentSelection() {
        guard !selectedHostID.isEmpty else { return }
        storedSelection.hostID = selectedHostID
        var hostSelection = storedSelection.hosts[selectedHostID] ?? StoredHostSelection()
        if !selectedWorkspaceID.isEmpty {
            hostSelection.workspaceID = selectedWorkspaceID
        }
        if !selectedSessionID.isEmpty {
            hostSelection.sessionID = selectedSessionID
            if !selectedWorkspaceID.isEmpty {
                hostSelection.sessionsByWorkspace[selectedWorkspaceID] = selectedSessionID
            }
        }
        storedSelection.hosts[selectedHostID] = hostSelection
        if let data = try? JSONCoding.encode(storedSelection) {
            UserDefaults.standard.set(data, forKey: Self.storedSelectionKey)
        }
    }

    private func rememberSessionID(_ sessionID: String) {
        guard !sessionID.isEmpty else { return }
        if let session = workbench.sessions[sessionID] {
            selectedWorkspaceID = session.workspaceID
        }
        selectedSessionID = sessionID
        reconcileRunModelSelection()
        saveCurrentSelection()
    }

    private static func loadStoredSelection(key: String) -> StoredControllerSelection {
        guard let data = UserDefaults.standard.data(forKey: key),
              let selection = try? JSONCoding.decode(StoredControllerSelection.self, from: data)
        else {
            return StoredControllerSelection()
        }
        return selection
    }

    private func mergeEvents(_ events: [AstralEvent], fallbackSessionID: String? = nil) {
        for event in events {
            var nextEvent = event
            if nextEvent.sessionID == nil {
                nextEvent.sessionID = fallbackSessionID
            }
            guard let sessionID = nextEvent.sessionID else { continue }
            var current = eventsBySession[sessionID] ?? []
            if !current.contains(where: { $0.seq == nextEvent.seq }) {
                current.append(nextEvent)
                current.sort { $0.seq < $1.seq }
            }
            eventsBySession[sessionID] = current
        }
    }

    private func renderTranscript() {
        let session = workbench.sessions[selectedSessionID]
        let payload = TranscriptNativePayload(
            sessionKey: selectedSessionID,
            activeSession: session,
            editableUserMessage: selectedSessionView?.editableUserMessage,
            events: activeTranscriptEvents,
            sourceSessionExists: sourceSessionExists(for: session),
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

    private static func dedupedModelOptions(_ models: [ModelInfo], currentModel: String?) -> [ModelInfo] {
        var seen = Set<String>()
        var result: [ModelInfo] = []
        for model in models {
            let id = model.id.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !id.isEmpty, !seen.contains(id) else { continue }
            seen.insert(id)
            result.append(model)
        }
        let current = currentModel?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        if !current.isEmpty, !seen.contains(current) {
            result.insert(ModelInfo(id: current, label: nil), at: 0)
        }
        return result
    }
}

private struct EventEnvelope: Codable {
    var seq: Int
    var event: AstralEvent
}

private struct StoredControllerSelection: Codable {
    var hostID: String?
    var hosts: [String: StoredHostSelection] = [:]
}

private struct StoredHostSelection: Codable {
    var workspaceID: String?
    var sessionID: String?
    var sessionsByWorkspace: [String: String] = [:]
}

private struct TranscriptNativePayload: Codable {
    var sessionKey: String
    var activeSession: SessionRecord?
    var editableUserMessage: EditableUserMessageView?
    var events: [AstralEvent]
    var sourceSessionExists: Bool
    var empty: TranscriptEmptyState
}

private struct TranscriptEmptyState: Codable {
    var title: String
    var subtitle: String
}

private extension String {
    var nilIfEmpty: String? {
        let value = trimmingCharacters(in: .whitespacesAndNewlines)
        return value.isEmpty ? nil : value
    }

    var nonEmpty: String? {
        isEmpty ? nil : self
    }

    var lastPathComponent: String {
        (self as NSString).lastPathComponent
    }
}
