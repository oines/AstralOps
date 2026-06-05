import SwiftUI

struct FilesPage: View {
    @EnvironmentObject private var model: AppModel
    @State private var replaceOld = ""
    @State private var replaceNew = ""
    @State private var replaceAll = false
    @State private var moveDestination = ""
    @State private var deleteRecursive = false
    private let pageBackground = IOSColors.groupedPageBackground

    var body: some View {
        NavigationStack {
            ZStack {
                pageBackground
                    .ignoresSafeArea()

                VStack(spacing: 0) {
                    if let files = model.workspaceFiles, files.kind == "file" {
                        fileEditor(files)
                    } else {
                        directoryBrowser
                    }
                    Divider()
                    execPanel
                }
            }
            .navigationTitle("Files")
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(pageBackground, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .toolbar {
                ToolbarItemGroup(placement: .topBarLeading) {
                    Button {
                        withAnimation(IOSMotion.drawerSpring) {
                            model.showSideMenu.toggle()
                        }
                    } label: {
                        Image(systemName: "line.3.horizontal")
                    }
                    .accessibilityLabel("Menu")

                    Button {
                        Task { await model.loadWorkspaceFiles(path: parentPath(model.workspaceFiles?.path ?? "")) }
                    } label: {
                        Image(systemName: "chevron.up")
                    }
                    .disabled(model.workspaceFiles?.path.isEmpty ?? true)
                    .accessibilityLabel("Parent folder")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        Button {
                            Task { await model.loadWorkspaceFiles(path: model.workspaceFiles?.path ?? "") }
                        } label: {
                            Label("Refresh", systemImage: "arrow.clockwise")
                        }
                        Button {
                            Task { await model.saveSelectedFile() }
                        } label: {
                            Label("Save", systemImage: "square.and.arrow.down")
                        }
                        .disabled(model.selectedFilePath.isEmpty)
                        Button(role: .destructive) {
                            Task { await model.deleteWorkspacePath(path: activePath, recursive: deleteRecursive) }
                        } label: {
                            Label("Delete", systemImage: "trash")
                        }
                        .disabled(activePath.isEmpty)
                    } label: {
                        Image(systemName: "ellipsis.circle")
                    }
                    .accessibilityLabel("File actions")
                }
            }
            .task(id: model.selectedWorkspaceID) {
                if model.workspaceFiles == nil, !model.selectedWorkspaceID.isEmpty {
                    await model.loadWorkspaceFiles()
                }
            }
        }
    }

    private var activePath: String {
        model.selectedFilePath.isEmpty ? (model.workspaceFiles?.path ?? "") : model.selectedFilePath
    }

    @ViewBuilder
    private var directoryBrowser: some View {
        List {
            if let files = model.workspaceFiles {
                Section(files.path.isEmpty ? "." : files.path) {
                    ForEach(files.entries ?? []) { entry in
                        Button {
                            Task { await model.openWorkspaceEntry(entry) }
                        } label: {
                            HStack(spacing: 12) {
                                Image(systemName: entry.kind == "directory" ? "folder" : "doc.text")
                                    .frame(width: 24)
                                    .foregroundStyle(.secondary)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(entry.name)
                                        .foregroundStyle(.primary)
                                    if let size = entry.size {
                                        Text("\(size) bytes")
                                            .font(.footnote)
                                            .foregroundStyle(.secondary)
                                    }
                                }
                                Spacer()
                            }
                        }
                        .accessibilityLabel(entry.name)
                        .accessibilityHint(entry.kind == "directory" ? "Opens folder" : "Opens file")
                    }
                }
            } else {
                ContentUnavailableView("No workspace files", systemImage: "folder", description: Text("Select a workspace, then refresh."))
            }
        }
        .listStyle(.insetGrouped)
        .scrollContentBackground(.hidden)
        .background(pageBackground)
    }

    private func fileEditor(_ files: WorkspaceFilesReadResult) -> some View {
        VStack(spacing: 0) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(files.name ?? files.path)
                        .font(.body.weight(.semibold))
                    Text(files.path)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Button {
                    Task { await model.saveSelectedFile() }
                } label: {
                    Label("Save", systemImage: "square.and.arrow.down")
                }
                .buttonStyle(.borderedProminent)
            }
            .padding(.horizontal, IOSMetrics.compactEdge)
            .padding(.vertical, 10)

            TextEditor(text: $model.fileEditorText)
                .font(.system(.body, design: .monospaced))
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)

            DisclosureGroup {
                VStack(alignment: .leading, spacing: 10) {
                    TextField("Old string", text: $replaceOld, axis: .vertical)
                        .lineLimit(1...4)
                        .textFieldStyle(.roundedBorder)
                    TextField("New string", text: $replaceNew, axis: .vertical)
                        .lineLimit(1...4)
                        .textFieldStyle(.roundedBorder)
                    Toggle("Replace all", isOn: $replaceAll)
                    Button {
                        Task { await model.applyReplace(oldString: replaceOld, newString: replaceNew, replaceAll: replaceAll) }
                    } label: {
                        Label("Apply replace", systemImage: "text.badge.checkmark")
                    }
                    .buttonStyle(.bordered)
                    .disabled(replaceOld.isEmpty)
                }
                .padding(.top, 10)
            } label: {
                Label("Replace", systemImage: "text.cursor")
            }
            .padding(.horizontal, IOSMetrics.compactEdge)
            .padding(.vertical, 10)

            DisclosureGroup {
                VStack(alignment: .leading, spacing: 10) {
                    TextField("Destination path", text: $moveDestination)
                        .textFieldStyle(.roundedBorder)
                    Toggle("Recursive delete", isOn: $deleteRecursive)
                    HStack {
                        Button {
                            Task { await model.moveWorkspacePath(path: files.path, destinationPath: moveDestination) }
                        } label: {
                            Label("Move", systemImage: "arrow.right.doc.on.clipboard")
                        }
                        .buttonStyle(.bordered)
                        .disabled(moveDestination.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                        Button(role: .destructive) {
                            Task { await model.deleteWorkspacePath(path: files.path, recursive: deleteRecursive) }
                        } label: {
                            Label("Delete", systemImage: "trash")
                        }
                        .buttonStyle(.bordered)
                    }
                }
                .padding(.top, 10)
            } label: {
                Label("Move or delete", systemImage: "folder.badge.gearshape")
            }
            .padding(.horizontal, IOSMetrics.compactEdge)
            .padding(.bottom, 10)
        }
    }

    private var execPanel: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                TextField("Command", text: $model.execCommand)
                    .textFieldStyle(.roundedBorder)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .submitLabel(.go)
                    .onSubmit {
                        Task { await model.runWorkspaceExec() }
                    }
                Button {
                    Task { await model.runWorkspaceExec() }
                } label: {
                    Image(systemName: "play.fill")
                        .frame(width: IOSMetrics.controlSize, height: IOSMetrics.fieldHeight)
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.execCommand.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityLabel("Run command")
            }
            if let result = model.execResult {
                Text([result.stdout, result.stderr, result.output, result.failure].compactMap { value in
                    let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                    return trimmed.isEmpty ? nil : trimmed
                }.joined(separator: "\n"))
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(10)
                    .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            }
        }
        .padding(IOSMetrics.compactEdge)
        .background(IOSColors.pageBackground.ignoresSafeArea(edges: .bottom))
    }

    private func parentPath(_ path: String) -> String {
        let parent = (path as NSString).deletingLastPathComponent
        return parent == "." ? "" : parent
    }
}
