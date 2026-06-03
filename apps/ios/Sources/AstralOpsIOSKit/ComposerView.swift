import SwiftUI

struct ComposerBar: View {
    @EnvironmentObject private var model: AppModel
    @FocusState private var focused: Bool

    var body: some View {
        HStack(spacing: IOSMetrics.controlGap) {
            TextField("Message", text: $model.composerText, axis: .horizontal)
                .textFieldStyle(.plain)
                .focused($focused)
                .submitLabel(.send)
                .onSubmit {
                    Task { await model.sendComposerText() }
                }
                .padding(.horizontal, 12)
                .frame(minHeight: IOSMetrics.fieldHeight)
                .background(Color(.tertiarySystemFill), in: Capsule())

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
        .frame(minHeight: IOSMetrics.controlSize)
    }
}
