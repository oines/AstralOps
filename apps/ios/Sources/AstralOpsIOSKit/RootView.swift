import SwiftUI

enum IOSMetrics {
    static let compactEdge: CGFloat = 12
    static let controlGap: CGFloat = 8
    static let controlSize: CGFloat = 44
    static let fieldHeight: CGFloat = 38
    static let bottomVerticalPadding: CGFloat = 8
}

struct RootView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        TabView(selection: $model.page) {
            NavigatorView()
                .tabItem { Label("Devices", systemImage: "desktopcomputer") }
                .tag(AppModel.Page.navigator)

            TranscriptPage()
                .tabItem { Label("Transcript", systemImage: "bubble.left.and.text.bubble.right") }
                .tag(AppModel.Page.transcript)

            TerminalPage()
                .tabItem { Label("Terminal", systemImage: "terminal") }
                .tag(AppModel.Page.terminal)
        }
        .overlay {
            if model.isBusy {
                ProgressView()
                    .controlSize(.regular)
                    .padding(14)
                    .background(.regularMaterial, in: Circle())
            }
        }
        .sheet(isPresented: $model.settingsPresented) {
            SettingsSheet()
                .presentationDetents([.medium, .large])
                .presentationDragIndicator(.visible)
        }
        .sheet(item: $model.pendingInteraction) { interaction in
            PendingInteractionSheet(interaction: interaction)
                .presentationDetents([.medium, .large])
                .presentationDragIndicator(.visible)
        }
        .alert("AstralOps", isPresented: Binding(
            get: { !model.errorMessage.isEmpty },
            set: { if !$0 { model.errorMessage = "" } }
        )) {
            Button("OK", role: .cancel) { model.errorMessage = "" }
        } message: {
            Text(model.errorMessage)
        }
    }
}
