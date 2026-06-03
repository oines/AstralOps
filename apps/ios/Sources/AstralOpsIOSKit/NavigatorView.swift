import SwiftUI

struct NavigatorView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        NavigationStack {
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
                            Button {
                                model.selectHost(host)
                            } label: {
                                HostRow(host: host, selected: host.deviceID == model.selectedHostID)
                            }
                        }
                    }
                }

                Section("Workspaces") {
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
                                    selected: workspace.id == model.selectedWorkspaceID
                                )
                            }
                        }
                    }
                }

                Section("Sessions") {
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
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle("AstralOps")
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button {
                        model.settingsPresented = true
                    } label: {
                        Image(systemName: "gearshape")
                    }
                    .accessibilityLabel("Settings")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await model.refreshMesh() }
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .accessibilityLabel("Refresh")
                }
            }
        }
    }
}

private struct HostRow: View {
    let host: RemoteHostRecord
    let selected: Bool

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
                Text([host.connection, host.authorizationState, host.status].compactMap { $0 }.joined(separator: " · "))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            if selected {
                Image(systemName: "checkmark")
                    .foregroundStyle(.tint)
            }
        }
    }
}

private struct WorkspaceRow: View {
    let workspace: Workspace
    let selected: Bool

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "folder")
                .frame(width: 24)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(workspace.name)
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                let subtitle = [workspace.agent, workspace.target].compactMap { $0 }.joined(separator: " · ")
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
    }
}

private struct SessionRow: View {
    let session: SessionRecord
    let selected: Bool

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "bubble.left.and.text.bubble.right")
                .frame(width: 24)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(session.title ?? "Untitled session")
                    .font(.body.weight(.semibold))
                    .foregroundStyle(.primary)
                let subtitle = [session.agent, session.status].compactMap { $0 }.joined(separator: " · ")
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
    }
}
