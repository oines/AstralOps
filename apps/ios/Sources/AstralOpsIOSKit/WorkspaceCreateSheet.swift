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
    @State private var browseErrorMessage = ""

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
                    .onChange(of: sshEndpoint) { _, _ in resetBrowsePreview(keepPath: true) }
                    .onChange(of: sshPort) { _, _ in resetBrowsePreview(keepPath: true) }
                    .onChange(of: sshRemoteCWD) { _, value in
                        if browsePath != value {
                            browsePath = value
                            resetBrowsePreview(keepPath: true)
                        }
                    }
                }

                Section(target == "ssh" ? "Remote folder" : "Host folder") {
                    VStack(alignment: .leading, spacing: 12) {
                        TextField(target == "ssh" ? "Remote path" : "Path", text: $browsePath)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .submitLabel(.go)
                            .onSubmit {
                                Task { await browse() }
                            }
                        HStack {
                            Button {
                                Task { await browse() }
                            } label: {
                                Label("Preview", systemImage: "folder.badge.questionmark")
                            }
                            .buttonStyle(.borderedProminent)
                            .disabled(!canBrowse || isBrowsing)

                            if isBrowsing {
                                ProgressView()
                            }
                        }
                        if !canBrowse {
                            Text(target == "ssh" ? "Enter an SSH endpoint before previewing a remote directory." : "Enter a Host path before previewing.")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                        if !browseErrorMessage.isEmpty {
                            Text(browseErrorMessage)
                                .font(.footnote)
                                .foregroundStyle(.red)
                        }
                    }

                    if let browseResult {
                        DirectoryPreview(
                            result: browseResult,
                            selectedPath: selectedFolderPath,
                            onOpen: { path in
                                Task { await openDirectory(path) }
                            },
                            onSelect: { path in
                                selectFolder(path)
                            }
                        )
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
                    browsePath = target == "ssh" ? (sshRemoteCWD.isEmpty ? "/" : sshRemoteCWD) : localCWD
                    await browse()
                }
            }
            .onChange(of: target) { _, value in
                browseResult = nil
                browseErrorMessage = ""
                browsePath = value == "ssh" ? (sshRemoteCWD.isEmpty ? "/" : sshRemoteCWD) : localCWD
            }
        }
    }

    private var canBrowse: Bool {
        if target == "ssh" {
            return !sshEndpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return true
    }

    private var selectedFolderPath: String {
        target == "ssh" ? sshRemoteCWD : localCWD
    }

    private var createDisabled: Bool {
        if target == "ssh" {
            return sshEndpoint.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || sshRemoteCWD.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return localCWD.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && browsePath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func browse() async {
        guard canBrowse else { return }
        isBrowsing = true
        browseErrorMessage = ""
        defer { isBrowsing = false }
        do {
            let ssh = target == "ssh" ? sshConfig() : nil
            let result = try await model.browseHostFileSystem(target: target, path: browsePath, ssh: ssh)
            browseResult = result
            browsePath = result.path
            selectFolder(result.path)
        } catch {
            browseResult = nil
            browseErrorMessage = AppModel.errorDisplayMessage(for: error)
        }
    }

    private func openDirectory(_ path: String) async {
        browsePath = path
        await browse()
    }

    private func selectFolder(_ path: String) {
        if target == "ssh" {
            sshRemoteCWD = path
        } else {
            localCWD = path
        }
    }

    private func resetBrowsePreview(keepPath: Bool = false) {
        browseResult = nil
        browseErrorMessage = ""
        if !keepPath {
            browsePath = target == "ssh" ? (sshRemoteCWD.isEmpty ? "/" : sshRemoteCWD) : localCWD
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

private struct DirectoryPreview: View {
    let result: HostFileSystemBrowseResult
    let selectedPath: String
    let onOpen: (String) -> Void
    let onSelect: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 4) {
                Label(result.path, systemImage: "folder")
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(2)
                    .textSelection(.enabled)
                Text(summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Button {
                onSelect(result.path)
            } label: {
                Label(result.path == selectedPath ? "Selected" : "Use this folder", systemImage: result.path == selectedPath ? "checkmark.circle.fill" : "checkmark.circle")
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .buttonStyle(.bordered)
            .disabled(result.path == selectedPath)

            if !result.roots.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(result.roots) { root in
                            Button {
                                onOpen(root.path)
                            } label: {
                                Label(root.label, systemImage: root.kind == "home" ? "house" : "externaldrive")
                            }
                            .buttonStyle(.bordered)
                        }
                    }
                }
            }

            if let parent = result.parentPath, !parent.isEmpty {
                Button {
                    onOpen(parent)
                } label: {
                    Label("Parent", systemImage: "chevron.up")
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }

            if result.entries.isEmpty {
                Text("Empty directory")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.vertical, 8)
            } else {
                ForEach(result.entries) { entry in
                    DirectoryPreviewEntry(entry: entry, onOpen: onOpen)
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var summary: String {
        let directoryCount = result.entries.filter(\.isDirectory).count
        let fileCount = result.entries.count - directoryCount
        var parts = ["\(directoryCount) folders", "\(fileCount) files"]
        if result.truncated == true {
            parts.append("truncated")
        }
        return parts.joined(separator: " · ")
    }
}

private struct DirectoryPreviewEntry: View {
    let entry: HostFileSystemEntry
    let onOpen: (String) -> Void

    var body: some View {
        if entry.isDirectory {
            Button {
                onOpen(entry.path)
            } label: {
                row(icon: "folder", tint: .accentColor)
            }
        } else {
            row(icon: "doc.text", tint: .secondary)
                .foregroundStyle(.secondary)
        }
    }

    private func row(icon: String, tint: Color) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .frame(width: 22)
                .foregroundStyle(tint)
            VStack(alignment: .leading, spacing: 2) {
                Text(entry.name)
                    .lineLimit(1)
                Text(entryDetail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            if entry.isDirectory {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .contentShape(Rectangle())
    }

    private var entryDetail: String {
        if entry.isDirectory {
            return entry.path
        }
        if let size = entry.size {
            return "\(size) bytes"
        }
        return entry.path
    }
}
