import SwiftUI

struct TerminalPage: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        NavigationStack {
            ZStack {
                Color.black.ignoresSafeArea()
                LocalHTMLWebView(
                    resourceName: colorScheme == .dark ? "terminal-dark" : "terminal-light",
                    bridge: model.terminalBridge,
                    onTerminalInput: { model.sendTerminalInput($0) }
                )
            }
            .navigationTitle("Terminal")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        Task { await model.openTerminal() }
                    } label: {
                        Image(systemName: "plus")
                    }
                    .accessibilityLabel("Open terminal")
                }
            }
            .safeAreaInset(edge: .bottom, spacing: 0) {
                TerminalShortcutBar()
            }
        }
    }
}

struct TerminalShortcutBar: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            Divider()
                .overlay(Color.white.opacity(0.16))
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    shortcutButton("Esc", value: "\u{1b}")
                    shortcutButton("Ctrl+C", value: "\u{03}")
                    shortcutButton("Tab", value: "\t")
                    shortcutButton("Up", value: "\u{1b}[A")
                    shortcutButton("Down", value: "\u{1b}[B")
                    shortcutButton("Left", value: "\u{1b}[D")
                    shortcutButton("Right", value: "\u{1b}[C")
                    shortcutButton("Enter", value: "\n")
                }
                .padding(.horizontal, IOSMetrics.compactEdge)
                .padding(.vertical, IOSMetrics.bottomVerticalPadding)
            }
        }
        .frame(maxWidth: .infinity)
        .background(Color.black)
    }

    private func shortcutButton(_ title: String, value: String) -> some View {
        Button {
            model.terminalShortcut(value)
        } label: {
            Text(title)
                .font(.callout.weight(.semibold))
                .foregroundStyle(.white)
                .padding(.horizontal, 12)
                .frame(height: IOSMetrics.fieldHeight)
                .background(Color.white.opacity(0.12), in: Capsule())
        }
        .buttonStyle(.plain)
    }
}
