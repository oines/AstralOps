import SwiftUI

struct TranscriptPage: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        NavigationStack {
            LocalHTMLWebView(
                resourceName: colorScheme == .dark ? "transcript-dark" : "transcript-light",
                bridge: model.transcriptBridge
            )
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .safeAreaInset(edge: .bottom, spacing: 0) {
                VStack(spacing: 0) {
                    Divider()
                    ComposerBar()
                        .padding(.horizontal, IOSMetrics.compactEdge)
                        .padding(.vertical, IOSMetrics.bottomVerticalPadding)
                }
                .background(Color(.systemBackground))
            }
        }
    }

    private var title: String {
        model.sessions.first { $0.id == model.selectedSessionID }?.title ?? "Transcript"
    }
}
