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

                        Button {
                            beginCloudAuth(provider: "google")
                        } label: {
                            Label(authenticatingProvider == "google" ? "Signing in..." : "Sign in with Google", systemImage: "person.crop.circle.badge.checkmark")
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
                model.errorMessage = error.localizedDescription
            }
        }
    }
}
