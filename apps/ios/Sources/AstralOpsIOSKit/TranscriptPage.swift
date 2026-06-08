import SwiftUI

struct TranscriptPage: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.colorScheme) private var colorScheme
    private let pageBackground = IOSColors.pageBackground

    var body: some View {
        NavigationStack {
            LocalHTMLWebView(
                resourceName: colorScheme == .dark ? "transcript-dark" : "transcript-light",
                bridge: model.transcriptBridge,
                onTranscriptAction: { model.handleTranscriptAction($0) },
                onMediaRequest: { try await model.mediaResponse(for: $0) }
            )
            .background(pageBackground)
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbarBackground(pageBackground, for: .navigationBar)
            .toolbarBackground(.visible, for: .navigationBar)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button {
                        withAnimation(IOSMotion.drawerSpring) {
                            model.showSideMenu.toggle()
                        }
                    } label: {
                        Image(systemName: "line.3.horizontal")
                    }
                    .accessibilityLabel("Menu")
                }
            }
            .safeAreaInset(edge: .bottom, spacing: 0) {
                VStack(spacing: 0) {
                    Divider()
                    ComposerBar()
                        .padding(.horizontal, IOSMetrics.compactEdge)
                        .padding(.vertical, IOSMetrics.bottomVerticalPadding)
                }
                .background(pageBackground.ignoresSafeArea(edges: .bottom))
            }
        }
        .background(pageBackground.ignoresSafeArea())
    }

    private var title: String {
        model.sessions.first { $0.id == model.selectedSessionID }?.title ?? "Transcript"
    }
}
