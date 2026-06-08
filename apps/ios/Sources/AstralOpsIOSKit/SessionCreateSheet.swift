import SwiftUI

struct SessionCreateSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var agent = "codex"

    var body: some View {
        NavigationStack {
            Form {
                Section("Session") {
                    Picker("Agent", selection: $agent) {
                        Text("Codex").tag("codex")
                        Text("Claude").tag("claude")
                    }
                }
            }
            .navigationTitle("New Session")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Create") {
                        Task { await model.createSession(agent: agent) }
                    }
                    .disabled(model.selectedWorkspaceID.isEmpty)
                }
            }
        }
    }
}
