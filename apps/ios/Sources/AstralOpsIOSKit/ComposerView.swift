import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

struct ComposerBar: View {
    @EnvironmentObject private var model: AppModel
    @FocusState private var focused: Bool
    @State private var fileImporterPresented = false
    @State private var photosPickerPresented = false
    @State private var selectedPhotoItems: [PhotosPickerItem] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if model.composerUploadCount > 0 {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text(uploadStatusTitle)
                        .lineLimit(1)
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 10)
                .frame(minHeight: IOSMetrics.controlSize)
                .background(Color(.tertiarySystemFill), in: Capsule())
                .accessibilityLabel(uploadStatusTitle)
            }

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
                    Button {
                        photosPickerPresented = true
                    } label: {
                        Label("Photos", systemImage: "photo.on.rectangle")
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
                .disabled(!model.canSendComposerInput)
                .accessibilityLabel("Send")
            }
        }
        .frame(minHeight: IOSMetrics.controlSize)
        .fileImporter(isPresented: $fileImporterPresented, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            handleFileImport(result)
        }
        .photosPicker(isPresented: $photosPickerPresented, selection: $selectedPhotoItems, maxSelectionCount: 10, matching: .images)
        .onChange(of: selectedPhotoItems) { _, items in
            guard !items.isEmpty else { return }
            let pickedItems = items
            selectedPhotoItems = []
            Task { await uploadPhotos(pickedItems) }
        }
    }

    private var uploadStatusTitle: String {
        model.composerUploadCount == 1 ? "Uploading attachment" : "Uploading \(model.composerUploadCount) attachments"
    }

    private func handleFileImport(_ result: Result<[URL], Error>) {
        switch result {
        case .success(let urls):
            Task {
                for url in urls {
                    await uploadFile(url)
                }
            }
        case .failure(let error):
            model.presentError(error)
        }
    }

    private func uploadPhotos(_ items: [PhotosPickerItem]) async {
        let timestamp = Int(Date().timeIntervalSince1970)
        for (index, item) in items.enumerated() {
            model.beginComposerUpload()
            defer { model.finishComposerUpload() }
            do {
                guard let data = try await item.loadTransferable(type: Data.self) else {
                    throw AppModelError.attachmentUnavailable
                }
                let metadata = photoAttachmentMetadata(for: item, timestamp: timestamp, index: index)
                await model.addComposerAttachment(data: data, name: metadata.name, mimeType: metadata.mimeType, kind: "image", tracksUpload: false)
            } catch {
                model.presentError(error)
            }
        }
    }

    private func uploadFile(_ url: URL) async {
        model.beginComposerUpload()
        defer { model.finishComposerUpload() }
        let access = url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        do {
            let values = try url.resourceValues(forKeys: [.isDirectoryKey, .contentTypeKey])
            guard values.isDirectory != true else { throw AppModelError.attachmentUnavailable }
            let data = try Data(contentsOf: url)
            let type = values.contentType ?? UTType(filenameExtension: url.pathExtension)
            let mimeType = type?.preferredMIMEType ?? "application/octet-stream"
            let kind = mimeType.hasPrefix("image/") ? "image" : "file"
            await model.addComposerAttachment(data: data, name: url.lastPathComponent, mimeType: mimeType, kind: kind, tracksUpload: false)
        } catch {
            model.presentError(error)
        }
    }

    private func photoAttachmentMetadata(for item: PhotosPickerItem, timestamp: Int, index: Int) -> (name: String, mimeType: String) {
        let type = item.supportedContentTypes.first { $0.conforms(to: .image) }
        let mimeType = type?.preferredMIMEType ?? "image/jpeg"
        let fallbackExtension = mimeType == "image/png" ? "png" : "jpg"
        let fileExtension = type?.preferredFilenameExtension ?? fallbackExtension
        return ("photo-\(timestamp)-\(index + 1).\(fileExtension)", mimeType)
    }
}
