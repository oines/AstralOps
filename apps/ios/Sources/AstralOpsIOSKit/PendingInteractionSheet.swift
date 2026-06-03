import SwiftUI

struct PendingInteractionSheet: View {
    @EnvironmentObject private var model: AppModel
    let interaction: PendingInteractionView
    @State private var feedback = ""

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    VStack(alignment: .leading, spacing: 8) {
                        Text(interaction.kind.uppercased())
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                        Text(interaction.title)
                            .font(.title3.weight(.semibold))
                    }

                    if let rows = interaction.detailRows, !rows.isEmpty {
                        VStack(spacing: 10) {
                            ForEach(rows) { row in
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(row.key ?? row.label)
                                        .font(.caption.weight(.semibold))
                                        .foregroundStyle(.secondary)
                                    Text(row.value)
                                        .font(row.mono == true ? .system(.callout, design: .monospaced) : .callout)
                                        .textSelection(.enabled)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .padding(12)
                                .background(.thinMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                            }
                        }
                    }

                    TextField("Feedback", text: $feedback, axis: .vertical)
                        .lineLimit(2...5)
                        .textFieldStyle(.roundedBorder)

                    VStack(spacing: 10) {
                        ForEach(interaction.actions) { action in
                            Button {
                                Task { await model.respond(to: interaction, action: action, feedback: feedback) }
                            } label: {
                                HStack {
                                    Text(action.label)
                                        .font(.body.weight(.semibold))
                                    Spacer()
                                    Image(systemName: "arrow.right")
                                }
                                .padding(14)
                                .background(action.role == "destructive" ? Color.red.opacity(0.14) : Color.accentColor.opacity(0.14), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                .padding(20)
            }
            .navigationTitle("Action Required")
            .navigationBarTitleDisplayMode(.inline)
        }
    }
}
