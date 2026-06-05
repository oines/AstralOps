import SwiftUI

struct NavigatorView: View {
    @EnvironmentObject private var model: AppModel
    private let pageBackground = IOSColors.groupedPageBackground

    var body: some View {
        NavigationStack {
            ZStack {
                pageBackground
                    .ignoresSafeArea()

                List {
                Section("Devices") {
                    if model.hosts.isEmpty {
                        Label {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("No Hosts")
                                Text(model.cloudSession == nil
                                     ? "Sign in from Settings to view devices."
                                     : "No controllable Desktop Hosts are visible.")
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                            }
                        } icon: {
                            Image(systemName: "desktopcomputer")
                                .foregroundStyle(.secondary)
                        }
                    } else {
                        ForEach(model.hosts) { host in
                            HostListItem(host: host, selected: host.deviceID == model.selectedHostID)
                        }
                    }
                }

                Section("Workspaces") {
                    Button {
                        model.workspaceCreatorPresented = true
                    } label: {
                        Label("New workspace", systemImage: "folder.badge.plus")
                    }
                    if model.workspaces.isEmpty {
                        Label("No workspaces", systemImage: "folder")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(model.workspaces) { workspace in
                            Button {
                                model.selectWorkspace(workspace)
                            } label: {
                                WorkspaceRow(
                                    workspace: workspace,
                                    selected: workspace.id == model.selectedWorkspaceID,
                                    connection: model.workbench.workspaceConnections?[workspace.id]
                                )
                            }
                            .swipeActions(edge: .trailing) {
                                Button(role: .destructive) {
                                    model.selectWorkspace(workspace)
                                    Task { await model.deleteSelectedWorkspace() }
                                } label: {
                                    Label("Delete", systemImage: "trash")
                                }
                            }
                            .swipeActions(edge: .leading) {
                                Button {
                                    model.selectWorkspace(workspace)
                                    Task { await model.connectSelectedWorkspace() }
                                } label: {
                                    Label("Connect", systemImage: "bolt.horizontal")
                                }
                                .tint(.green)
                            }
                            .contextMenu {
                                Button {
                                    model.selectWorkspace(workspace)
                                    Task { await model.connectSelectedWorkspace() }
                                } label: {
                                    Label("Connect", systemImage: "bolt.horizontal")
                                }
                                Button {
                                    model.selectWorkspace(workspace)
                                    Task { await model.disconnectSelectedWorkspace() }
                                } label: {
                                    Label("Disconnect", systemImage: "bolt.slash")
                                }
                                Button {
                                    model.selectWorkspace(workspace)
                                    model.page = .files
                                    Task { await model.loadWorkspaceFiles() }
                                } label: {
                                    Label("Files", systemImage: "folder")
                                }
                                Button(role: .destructive) {
                                    model.selectWorkspace(workspace)
                                    Task { await model.deleteSelectedWorkspace() }
                                } label: {
                                    Label("Delete", systemImage: "trash")
                                }
                            }
                    }
                }
                }

                Section("Sessions") {
                    Button {
                        model.sessionCreatorPresented = true
                    } label: {
                        Label("New session", systemImage: "plus.bubble")
                    }
                    .disabled(model.selectedWorkspaceID.isEmpty)
                    if model.sessions.isEmpty {
                        Label("No sessions", systemImage: "bubble.left.and.text.bubble.right")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(model.sessions) { session in
                            Button {
                                model.selectSession(session)
                                model.page = .transcript
                            } label: {
                                SessionRow(
                                    session: session,
                                    selected: session.id == model.selectedSessionID
                                )
                            }
                            .swipeActions(edge: .trailing) {
                                Button(role: .destructive) {
                                    Task { await model.deleteSession(session) }
                                } label: {
                                    Label("Delete", systemImage: "trash")
                                }
                            }
                            .swipeActions(edge: .leading) {
                                Button {
                                    model.selectSession(session)
                                    Task { await model.interruptSelectedSession() }
                                } label: {
                                    Label("Interrupt", systemImage: "stop.circle")
                                }
                                .tint(.orange)
                            }
                            .contextMenu {
                                Button {
                                    model.selectSession(session)
                                    Task { await model.interruptSelectedSession() }
                                } label: {
                                    Label("Interrupt", systemImage: "stop.circle")
                                }
                                Button(role: .destructive) {
                                    Task { await model.deleteSession(session) }
                                } label: {
                                    Label("Delete", systemImage: "trash")
                                }
                            }
                        }
                    }
                }

                if let view = model.selectedSessionView, let queued = view.queuedInputs, !queued.isEmpty {
                    Section("Queued Inputs") {
                        ForEach(queued) { item in
                            VStack(alignment: .leading, spacing: 6) {
                                Text(item.text)
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                            }
                            .swipeActions(edge: .trailing) {
                                Button(role: .destructive) {
                                    Task { await model.cancelQueuedInput(item) }
                                } label: {
                                    Label("Cancel", systemImage: "xmark.circle")
                                }
                            }
                            .swipeActions(edge: .leading) {
                                Button {
                                    Task { await model.steerQueuedInput(item) }
                                } label: {
                                    Label("Steer", systemImage: "arrow.triangle.turn.up.right.circle")
                                }
                                .tint(.blue)
                            }
                            .contextMenu {
                                Button {
                                    Task { await model.steerQueuedInput(item) }
                                } label: {
                                    Label("Steer", systemImage: "arrow.triangle.turn.up.right.circle")
                                }
                                Button(role: .destructive) {
                                    Task { await model.cancelQueuedInput(item) }
                                } label: {
                                    Label("Cancel", systemImage: "xmark.circle")
                                }
                            }
                        }
                    }
                }
                }
                .listStyle(.insetGrouped)
                .scrollContentBackground(.hidden)
            }
            .navigationTitle("AstralOps")
            .toolbarBackground(pageBackground, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button {
                        withAnimation(IOSMotion.drawerSpring) {
                            model.showSideMenu.toggle()
                        }
                    } label: {
                        Image(systemName: "line.3.horizontal")
                    }
                    .accessibilityLabel("Menu")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await model.refreshMesh() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .accessibilityLabel("Refresh")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        model.workspaceCreatorPresented = true
                    } label: {
                        Image(systemName: "plus")
                    }
                    .accessibilityLabel("New workspace")
                }
            }
        }
    }
}

private struct HostListItem: View {
    @EnvironmentObject private var model: AppModel
    let host: RemoteHostRecord
    let selected: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                model.selectHost(host)
            } label: {
                HostRow(host: host, selected: selected)
            }
            .buttonStyle(.plain)

            if host.authorizationState == "needs_pairing" {
                if host.pairingStatus == "pending" {
                    Label("Pairing requested", systemImage: "clock")
                        .font(.footnote.weight(.semibold))
                        .foregroundStyle(.secondary)
                } else {
                    Button {
                        Task { await model.requestPairing(host) }
                    } label: {
                        Label("Request pairing", systemImage: "person.badge.plus")
                    }
                    .buttonStyle(.bordered)
                }
            }
        }
        .padding(.vertical, 4)
    }
}

private struct HostRow: View {
    let host: RemoteHostRecord
    let selected: Bool

    private var statusText: String {
        [host.connection, host.authorizationState, host.status].compactMap { $0 }.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "desktopcomputer")
                .foregroundStyle(selected ? .white : .primary)
                .frame(width: 30, height: 30)
                .background(selected ? Color.accentColor : Color.secondary.opacity(0.16), in: Circle())
            VStack(alignment: .leading, spacing: 3) {
                Text(host.deviceName ?? "Desktop Host")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                Text(statusText)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if selected {
                Image(systemName: "checkmark")
                    .foregroundStyle(.tint)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(host.deviceName ?? "Desktop Host")
        .accessibilityValue(selected ? "Selected, \(statusText)" : statusText)
    }
}

private struct WorkspaceRow: View {
    let workspace: Workspace
    let selected: Bool
    let connection: WorkspaceConnection?

    private var subtitle: String {
        workspace.target ?? ""
    }

    private var connectionText: String? {
        guard let status = connection?.status else { return nil }
        return [status, connection?.displayCWD].compactMap { $0 }.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "folder")
                .frame(width: 24)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(workspace.name)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                if let connectionText {
                    Text(connectionText)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            if selected {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(.tint)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(workspace.name)
        .accessibilityValue([
            selected ? "Selected" : nil,
            connectionText ?? subtitle
        ].compactMap { $0 }.joined(separator: ", "))
    }
}

struct SessionCreateSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var agent = "codex"

    var body: some View {
        NavigationStack {
            Form {
                Section("Session") {
                    Picker("Agent", selection: $agent) {
                        Text("Codex").tag("codex")
                        Text("Claude").tag("claude")
                    }
                }
            }
            .navigationTitle("New Session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Create") {
                        Task { await model.createSession(agent: agent) }
                    }
                    .disabled(model.selectedWorkspaceID.isEmpty)
                }
            }
        }
    }
}

private struct SessionRow: View {
    let session: SessionRecord
    let selected: Bool

    private var subtitle: String {
        [session.agent, session.status].compactMap { $0 }.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "bubble.left.and.text.bubble.right")
                .frame(width: 24)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(session.title ?? "Untitled session")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            if selected {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(.tint)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(session.title ?? "Untitled session")
        .accessibilityValue([
            selected ? "Selected" : nil,
            subtitle.isEmpty ? nil : subtitle
        ].compactMap { $0 }.joined(separator: ", "))
    }
}
