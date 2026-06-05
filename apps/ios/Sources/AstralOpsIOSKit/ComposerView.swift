import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

struct ComposerBar: View {
    @EnvironmentObject private var model: AppModel
    @FocusState private var focused: Bool
    @State private var fileImporterPresented = false
    @State private var selectedPhotoItem: PhotosPickerItem?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !model.composerAttachments.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(model.composerAttachments) { attachment in
                            HStack(spacing: 6) {
                                Image(systemName: attachment.kind == "image" ? "photo" : "paperclip")
                                Text(attachment.name)
                                    .lineLimit(1)
                                Button {
                                    model.removeComposerAttachment(attachment)
                                } label: {
                                    Image(systemName: "xmark.circle.fill")
                                        .frame(minWidth: IOSMetrics.controlSize, minHeight: IOSMetrics.controlSize)
                                        .contentShape(Rectangle())
                                }
                                .buttonStyle(.plain)
                                .accessibilityLabel("Remove attachment")
                            }
                            .font(.caption.weight(.semibold))
                            .padding(.horizontal, 10)
                            .frame(minHeight: IOSMetrics.controlSize)
                            .background(Color(.tertiarySystemFill), in: Capsule())
                        }
                    }
                }
            }

            HStack(spacing: IOSMetrics.controlGap) {
                Menu {
                    Menu {
                        Picker("Model", selection: $model.selectedRunModel) {
                            Text(model.defaultModelMenuTitle).tag("")
                            ForEach(model.modelOptions, id: \.id) { option in
                                Text(option.displayName).tag(option.id)
                            }
                        }
                    } label: {
                        Label("Model", systemImage: "cpu")
                    }
                    Menu {
                        Picker("Reasoning", selection: $model.selectedReasoningEffort) {
                            Text("Host default").tag("")
                            Text("Low").tag("low")
                            Text("Medium").tag("medium")
                            Text("High").tag("high")
                        }
                    } label: {
                        Label("Reasoning", systemImage: "brain.head.profile")
                    }
                    Menu {
                        Picker("Permission mode", selection: $model.selectedPermissionMode) {
                            Text("Default").tag("default")
                            Text("Read only").tag("read-only")
                            Text("On request").tag("on-request")
                            Text("Full access").tag("full-access")
                        }
                    } label: {
                        Label("Permission mode", systemImage: "lock.shield")
                    }
                    Button {
                        model.resetRunOptions()
                    } label: {
                        Label("Use host defaults", systemImage: "arrow.counterclockwise")
                    }
                } label: {
                    Image(systemName: "slider.horizontal.3")
                        .frame(width: IOSMetrics.controlSize, height: IOSMetrics.controlSize)
                }
                .accessibilityLabel("Run options")

                Menu {
                    Button {
                        fileImporterPresented = true
                    } label: {
                        Label("Files", systemImage: "doc.badge.plus")
                    }
                    PhotosPicker(selection: $selectedPhotoItem, matching: .images) {
                        Label("Photos", systemImage: "photo")
                    }
                } label: {
                    Image(systemName: "paperclip")
                        .frame(width: IOSMetrics.controlSize, height: IOSMetrics.controlSize)
                }
                .accessibilityLabel("Add attachment")

                TextField("Message", text: $model.composerText, axis: .vertical)
                    .textFieldStyle(.plain)
                    .focused($focused)
                    .lineLimit(1...6)
                    .submitLabel(.send)
                    .onSubmit {
                        Task { await model.sendComposerText() }
                    }
                    .padding(.horizontal, 12)
                    .frame(minHeight: IOSMetrics.fieldHeight)
                    .background(Color(.tertiarySystemFill), in: RoundedRectangle(cornerRadius: 20, style: .continuous))

                Button {
                    Task { await model.sendComposerText() }
                } label: {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.title)
                        .frame(width: IOSMetrics.controlSize, height: IOSMetrics.controlSize)
                }
                .disabled(model.composerText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityLabel("Send")
            }
        }
        .frame(minHeight: IOSMetrics.controlSize)
        .fileImporter(isPresented: $fileImporterPresented, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            handleFileImport(result)
        }
        .onChange(of: selectedPhotoItem) { _, item in
            guard let item else { return }
            Task {
                if let data = try? await item.loadTransferable(type: Data.self) {
                    await model.addComposerAttachment(data: data, name: "photo.jpg", mimeType: "image/jpeg", kind: "image")
                }
                selectedPhotoItem = nil
            }
        }
    }

    private func handleFileImport(_ result: Result<[URL], Error>) {
        switch result {
        case .success(let urls):
            for url in urls {
                Task {
                    let access = url.startAccessingSecurityScopedResource()
                    defer { if access { url.stopAccessingSecurityScopedResource() } }
                    do {
                        let data = try Data(contentsOf: url)
                        let type = UTType(filenameExtension: url.pathExtension)
                        let mimeType = type?.preferredMIMEType ?? "application/octet-stream"
                        let kind = mimeType.hasPrefix("image/") ? "image" : "file"
                        await model.addComposerAttachment(data: data, name: url.lastPathComponent, mimeType: mimeType, kind: kind)
                    } catch {
                        model.presentError(error)
                    }
                }
            }
        case .failure(let error):
            model.presentError(error)
        }
    }
}
