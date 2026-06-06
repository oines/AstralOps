import SwiftUI

struct SettingsSheet: View {
    @EnvironmentObject private var model: AppModel
    @StateObject private var oauth = CloudOAuthCoordinator()
    @State private var baseURL = "https://cloud-astralops.oines.dev"
    @State private var authenticatingProvider: String?

    var body: some View {
        NavigationStack {
            Form {
                Section("Cloud") {
                    if let cloud = model.cloudSession {
                        LabeledContent("Account", value: cloud.accountIDHash ?? "Connected")
                        LabeledContent("Relay", value: cloud.relayURL ?? "Not configured")
                        Button("Log out", role: .destructive) {
                            Task { await model.logout() }
                        }
                    } else {
                        TextField("Cloud URL", text: $baseURL)
                            .textInputAutocapitalization(.never)
                            .keyboardType(.URL)
                        Button {
                            beginCloudAuth(provider: "github")
                        } label: {
                            Label(authenticatingProvider == "github" ? "Signing in..." : "Sign in with GitHub", systemImage: "person.crop.circle.badge.checkmark")
                        }
                        .disabled(authenticatingProvider != nil)
                    }
                }

                Section("Device") {
                    LabeledContent("Device ID", value: model.identity?.deviceID ?? "Not started")
                    LabeledContent("Name", value: model.identity?.deviceName ?? "AstralOps iPhone")
                }

                Section("Transport") {
                    Toggle("Force Relay", isOn: Binding(
                        get: { model.forceRelayOnly },
                        set: { model.toggleForceRelayOnly($0) }
                    ))
                    LabeledContent("Relay status", value: model.mesh?.cloud?.relayURL ?? "Not connected")
                }

                Section("Host Management") {
                    Button {
                        Task { await model.loadHostManagement() }
                    } label: {
                        Label("Refresh Host management", systemImage: "arrow.clockwise")
                    }
                    .disabled(model.selectedHost == nil)

                    if !model.pairingRequests.isEmpty {
                        ForEach(model.pairingRequests) { request in
                            VStack(alignment: .leading, spacing: 8) {
                                Text(request.controllerDeviceName ?? request.controllerDeviceID)
                                    .font(.body.weight(.semibold))
                                Text([request.status, request.scope].compactMap { $0 }.joined(separator: " · "))
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                                HStack {
                                    Button {
                                        Task { await model.resolvePairingRequest(request, approve: true) }
                                    } label: {
                                        Label("Approve", systemImage: "checkmark.circle")
                                    }
                                    .buttonStyle(.borderedProminent)
                                    Button(role: .destructive) {
                                        Task { await model.resolvePairingRequest(request, approve: false) }
                                    } label: {
                                        Label("Deny", systemImage: "xmark.circle")
                                    }
                                    .buttonStyle(.bordered)
                                }
                                .font(.footnote.weight(.semibold))
                            }
                            .padding(.vertical, 4)
                        }
                    }

                    if !model.trustGrants.isEmpty {
                        ForEach(model.trustGrants) { grant in
                            VStack(alignment: .leading, spacing: 8) {
                                Text(grant.controllerDeviceName ?? grant.controllerDeviceID)
                                    .font(.body.weight(.semibold))
                                Text([grant.status, grant.scope].compactMap { $0 }.joined(separator: " · "))
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                                Button(role: .destructive) {
                                    Task { await model.revokeTrust(grant) }
                                } label: {
                                    Label("Revoke", systemImage: "person.crop.circle.badge.xmark")
                                }
                                .buttonStyle(.bordered)
                                .font(.footnote.weight(.semibold))
                            }
                            .padding(.vertical, 4)
                        }
                    }
                }

                Section("Discovered Hosts") {
                    if model.hosts.isEmpty {
                        Text("No hosts found")
                            .foregroundStyle(.secondary)
                    } else {
                        ForEach(model.hosts) { host in
                            HostListItem(host: host, selected: host.deviceID == model.selectedHostID)
                        }
                    }
                }

                Section("Diagnostics") {
                    LabeledContent("Hosts", value: "\(model.hosts.count)")
                    LabeledContent("Workspaces", value: "\(model.workspaces.count)")
                    LabeledContent("Sessions", value: "\(model.sessions.count)")
                }
            }
            .navigationTitle("Settings")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { model.settingsPresented = false }
                }
            }
        }
    }

    private func beginCloudAuth(provider: String) {
        authenticatingProvider = provider
        Task {
            defer { authenticatingProvider = nil }
            do {
                let code = try await oauth.requestLoginCode(provider: provider, baseURL: baseURL)
                await model.login(baseURL: code.baseURL, loginCode: code.loginCode)
            } catch {
                model.presentError(error)
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
