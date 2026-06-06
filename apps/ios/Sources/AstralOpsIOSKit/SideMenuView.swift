import SwiftUI

struct SideMenuView: View {
    @EnvironmentObject private var model: AppModel
    @State private var deleteTarget: SideMenuDeleteTarget?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header: Host Switcher
            Menu {
                ForEach(model.hosts) { host in
                    Button {
                        model.selectHost(host)
                    } label: {
                        Text(host.deviceName ?? host.deviceID)
                        if host.deviceID == model.selectedHostID {
                            Image(systemName: "checkmark")
                        }
                    }
                }
            } label: {
                HStack {
                    VStack(alignment: .leading, spacing: 4) {
                        Text("AstralOps")
                            .font(.title2.weight(.bold))
                            .foregroundStyle(.primary)
                        Text(model.hosts.first(where: { $0.deviceID == model.selectedHostID })?.deviceName ?? "No Host Selected")
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down")
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 24)
                .padding(.top, 80)
                .padding(.bottom, 20)
            }

            ScrollView {
                VStack(alignment: .leading, spacing: 24) {
                    // Quick Switch / High Frequency Content
                    if !model.allGlobalPending.isEmpty || !model.allGlobalTerminals.isEmpty {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("ACTIVE").font(.caption.weight(.semibold)).foregroundStyle(.secondary).padding(.horizontal, 24)
                            
                            ForEach(model.allGlobalPending, id: \.interaction.id) { item in
                                SideMenuRow(icon: "exclamationmark.shield.fill", title: "Approval Required", subtitle: "\(item.session.title ?? "Session") · \(item.host.deviceName ?? "")", isSelected: false) {
                                    model.selectSession(item.session, on: item.host)
                                    model.pendingInteraction = item.interaction
                                    selectPage(.transcript)
                                }
                            }
                            
                            ForEach(model.allGlobalTerminals, id: \.tab.terminalID) { item in
                                SideMenuRow(icon: "terminal", title: item.tab.cwd ?? "Terminal", subtitle: "\(item.tab.status == "live" ? "Live" : "Idle") · \(item.host.deviceName ?? "")", isSelected: false) {
                                    selectPage(.terminal)
                                    Task { await model.selectTerminal(item.tab, on: item.host) }
                                }
                            }
                        }
                    }

                    let globalActiveSessions = model.allGlobalSessions.filter { $0.session.status == "running" || $0.session.status == "requires_action" }
                    if !globalActiveSessions.isEmpty {
                        VStack(alignment: .leading, spacing: 8) {
                            Text("RUNNING").font(.caption.weight(.semibold)).foregroundStyle(.secondary).padding(.horizontal, 24)
                            ForEach(globalActiveSessions, id: \.session.id) { item in
                                SideMenuRow(icon: "bubble.left.and.text.bubble.right", title: item.session.title ?? "Session", subtitle: item.host.deviceName, isSelected: model.page == .transcript && model.selectedSessionID == item.session.id) {
                                    model.selectSession(item.session, on: item.host)
                                    selectPage(.transcript)
                                }
                            }
                        }
                    }

                    // Current Host Workspaces & Sessions
                    VStack(alignment: .leading, spacing: 8) {
                        Text("WORKSPACES").font(.caption.weight(.semibold)).foregroundStyle(.secondary).padding(.horizontal, 24)
                        
                        let w = model.workbench
                        let workspaces = w.workspaces.values.sorted { ($0.updatedAt ?? "") > ($1.updatedAt ?? "") }
                        
                        if workspaces.isEmpty {
                            SideMenuRow(icon: "folder", title: "No workspaces", isSelected: false, action: nil)
                                .disabled(true)
                                .opacity(0.5)
                        } else {
                            ForEach(workspaces) { workspace in
                                DisclosureGroup {
                                    VStack(spacing: 2) {
                                        let sessions = w.sessions.values.filter { $0.workspaceID == workspace.id }.sorted { ($0.updatedAt ?? "") > ($1.updatedAt ?? "") }
                                        ForEach(sessions) { session in
                                            SideMenuRow(
                                                icon: "bubble.left",
                                                title: session.title ?? "Untitled",
                                                isSelected: model.page == .transcript && model.selectedSessionID == session.id,
                                                trailingIcon: "trash",
                                                trailingAccessibilityLabel: "Delete session",
                                                trailingRole: .destructive,
                                                trailingAction: {
                                                    deleteTarget = .session(session)
                                                }
                                            ) {
                                                model.selectWorkspace(workspace)
                                                model.selectSession(session)
                                                selectPage(.transcript)
                                            }
                                        }
                                        SideMenuRow(icon: "plus", title: "New session", isSelected: false) {
                                            model.selectWorkspace(workspace)
                                            model.sessionCreatorPresented = true
                                        }
                                        SideMenuRow(
                                            icon: "trash",
                                            title: "Delete workspace",
                                            isSelected: false,
                                            role: .destructive
                                        ) {
                                            deleteTarget = .workspace(workspace)
                                        }
                                    }
                                    .padding(.leading, 12)
                                } label: {
                                    HStack(spacing: 16) {
                                        Image(systemName: "folder")
                                            .font(.system(size: 20))
                                            .frame(width: 24)
                                        Text(workspace.name)
                                            .font(model.selectedWorkspaceID == workspace.id ? .body.weight(.semibold) : .body)
                                            .lineLimit(1)
                                        Spacer()
                                    }
                                    .padding(.vertical, 10)
                                    .padding(.horizontal, 16)
                                    .background(model.selectedWorkspaceID == workspace.id ? Color.accentColor.opacity(0.15) : Color.clear)
                                    .foregroundStyle(model.selectedWorkspaceID == workspace.id ? Color.accentColor : Color.primary)
                                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                }
                                .padding(.horizontal, 8)
                                .tint(.secondary)
                            }
                        }
                        
                        SideMenuRow(icon: "plus", title: "New workspace", isSelected: false) {
                            model.workspaceCreatorPresented = true
                        }
                    }

                    // Main Menu Items
                    VStack(alignment: .leading, spacing: 8) {
                        Text("MENU").font(.caption.weight(.semibold)).foregroundStyle(.secondary).padding(.horizontal, 24)

                        SideMenuRow(icon: "bubble.left.and.text.bubble.right", title: "Transcript", isSelected: model.page == .transcript) {
                            selectPage(.transcript)
                        }
                        SideMenuRow(icon: "folder", title: "Files", isSelected: model.page == .files) {
                            selectPage(.files)
                        }
                        SideMenuRow(icon: "terminal", title: "Terminal", isSelected: model.page == .terminal) {
                            selectPage(.terminal)
                        }
                    }
                }
            }

            // Footer
            VStack(spacing: 8) {
                Divider()
                    .padding(.horizontal, 24)
                SideMenuRow(icon: "gearshape", title: "Settings", isSelected: false) {
                    withAnimation(IOSMotion.drawerSpring) {
                        model.showSideMenu = false
                    }
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                        model.settingsPresented = true
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 40)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .alert(deleteTarget?.confirmationTitle ?? "Delete?", isPresented: deleteTargetBinding, presenting: deleteTarget) { target in
            Button("Delete", role: .destructive) {
                Task { await delete(target) }
            }
            Button("Cancel", role: .cancel) {
                deleteTarget = nil
            }
        } message: { target in
            Text(target.confirmationMessage)
        }
    }

    private func selectPage(_ page: AppModel.Page) {
        withAnimation(IOSMotion.drawerSpring) {
            model.page = page
            model.showSideMenu = false
        }
    }

    private var deleteTargetBinding: Binding<Bool> {
        Binding(
            get: { deleteTarget != nil },
            set: { if !$0 { deleteTarget = nil } }
        )
    }

    private func delete(_ target: SideMenuDeleteTarget) async {
        deleteTarget = nil
        switch target {
        case .workspace(let workspace):
            await model.deleteWorkspace(workspace)
        case .session(let session):
            await model.deleteSession(session)
        }
    }
}

private struct SideMenuRow: View {
    let icon: String
    let title: String
    var subtitle: String? = nil
    let isSelected: Bool
    var role: ButtonRole? = nil
    var trailingIcon: String? = nil
    var trailingAccessibilityLabel: String? = nil
    var trailingRole: ButtonRole? = nil
    var trailingAction: (() -> Void)? = nil
    let action: (() -> Void)?

    var body: some View {
        HStack(spacing: 0) {
            if let action = action {
                Button(role: role, action: action) { rowContent }
                    .buttonStyle(.plain)
            } else {
                rowContent
            }

            if let trailingIcon, let trailingAction {
                Button(role: trailingRole, action: trailingAction) {
                    Image(systemName: trailingIcon)
                        .font(.system(size: 15, weight: .semibold))
                        .foregroundStyle(.red)
                        .frame(width: 36, height: 36)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(trailingAccessibilityLabel ?? title)
            }
        }
        .padding(.horizontal, 8)
    }

    private var rowContent: some View {
        HStack(spacing: 16) {
            Image(systemName: icon)
                .font(.system(size: 20))
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(isSelected ? .body.weight(.semibold) : .body)
                    .lineLimit(1)
                if let subtitle = subtitle {
                    Text(subtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer()
        }
        .padding(.vertical, 10)
        .padding(.horizontal, 16)
        .background(isSelected ? Color.accentColor.opacity(0.15) : Color.clear)
        .foregroundStyle(role == .destructive ? Color.red : (isSelected ? Color.accentColor : Color.primary))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

private enum SideMenuDeleteTarget {
    case workspace(Workspace)
    case session(SessionRecord)

    var confirmationTitle: String {
        switch self {
        case .workspace:
            return "Delete workspace?"
        case .session:
            return "Delete session?"
        }
    }

    var confirmationMessage: String {
        switch self {
        case .workspace(let workspace):
            return "Delete \(workspace.name)? This removes it from the Host."
        case .session(let session):
            return "Delete \(session.title ?? "Untitled")? This removes the session from the Host."
        }
    }
}
