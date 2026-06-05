import SwiftUI

struct WorkspaceCreateSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var target = "local"
    @State private var localCWD = ""
    @State private var sshEndpoint = ""
    @State private var sshPort = "22"
    @State private var sshRemoteCWD = ""
    @State private var browsePath = ""
    @State private var browseResult: HostFileSystemBrowseResult?
    @State private var isBrowsing = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Workspace") {
                    TextField("Name", text: $name)
                    Picker("Target", selection: $target) {
                        Text("Local").tag("local")
                        Text("SSH").tag("ssh")
                    }
                }

                if target == "ssh" {
                    Section("SSH") {
                        TextField("Endpoint", text: $sshEndpoint)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        TextField("Port", text: $sshPort)
                            .keyboardType(.numberPad)
                        TextField("Remote cwd", text: $sshRemoteCWD)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                }

                Section(target == "ssh" ? "Remote folder" : "Host folder") {
                    HStack {
                        TextField(target == "ssh" ? "Remote path" : "Path", text: $browsePath)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        if isBrowsing {
                            ProgressView()
                        } else {
                            Button {
                                Task { await browse() }
                            } label: {
                                Image(systemName: "folder")
                            }
                            .accessibilityLabel("Browse")
                        }
                    }
                    if let browseResult {
                        ForEach(browseResult.roots) { root in
                            Button(root.label) {
                                browsePath = root.path
                                Task { await browse() }
                            }
                        }
                        if let parent = browseResult.parentPath, !parent.isEmpty {
                            Button {
                                browsePath = parent
                                Task { await browse() }
                            } label: {
                                Label("Parent", systemImage: "chevron.up")
                            }
                        }
                        ForEach(browseResult.entries) { entry in
                            if entry.kind == "directory" {
                                Button {
                                    browsePath = entry.path
                                    if target == "ssh" {
                                        sshRemoteCWD = entry.path
                                    } else {
                                        localCWD = entry.path
                                    }
                                    Task { await browse() }
                                } label: {
                                    Label(entry.name, systemImage: "folder")
                                }
                            }
                        }
                    }
                }
            }
            .navigationTitle("New Workspace")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Create") {
                        Task { await create() }
                    }
                    .disabled(createDisabled)
                }
            }
            .task {
                if browsePath.isEmpty {
                    await browse()
                }
            }
        }
    }

    private var createDisabled: Bool {
        if target == "ssh" {
            return sshEndpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || sshRemoteCWD.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return localCWD.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && browsePath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func browse() async {
        isBrowsing = true
        defer { isBrowsing = false }
        do {
            let ssh = target == "ssh" ? sshConfig() : nil
            let result = try await model.browseHostFileSystem(target: target, path: browsePath, ssh: ssh)
            browseResult = result
            browsePath = result.path
            if target == "ssh" {
                sshRemoteCWD = result.path
            } else {
                localCWD = result.path
            }
        } catch {
            model.presentError(error)
        }
    }

    private func create() async {
        let ssh = target == "ssh" ? sshConfig() : nil
        await model.createWorkspace(
            name: name,
            target: target,
            localCWD: target == "ssh" ? "" : (localCWD.isEmpty ? browsePath : localCWD),
            ssh: ssh
        )
    }

    private func sshConfig() -> SSHConfig {
        SSHConfig(
            endpoint: sshEndpoint.trimmingCharacters(in: .whitespacesAndNewlines),
            port: Int(sshPort) ?? 22,
            remoteCWD: sshRemoteCWD.trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }
}
