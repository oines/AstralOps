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
