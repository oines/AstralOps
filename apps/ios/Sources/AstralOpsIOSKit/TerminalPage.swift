import SwiftUI

struct TerminalPage: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.colorScheme) private var colorScheme

    private let terminalBackground = IOSColors.terminalBackground

    var body: some View {
        ZStack {
            terminalBackground
                .ignoresSafeArea()

            NavigationStack {
                ZStack {
                    terminalBackground.ignoresSafeArea()
                    LocalHTMLWebView(
                        resourceName: colorScheme == .dark ? "terminal-dark" : "terminal-light",
                        bridge: model.terminalBridge,
                        onTerminalInput: { model.sendTerminalInput($0) },
                        onTerminalResize: { terminalID, cols, rows in
                            model.terminalResize(terminalID: terminalID, cols: cols, rows: rows)
                        },
                        onTerminalHeartbeatAck: { terminalID, heartbeatSeq, renderedSeq in
                            model.terminalHeartbeatAck(terminalID: terminalID, heartbeatSeq: heartbeatSeq, renderedSeq: renderedSeq)
                        }
                    )
                }
                .navigationTitle("Terminal")
                .navigationBarTitleDisplayMode(.inline)
                .toolbarBackground(terminalBackground, for: .navigationBar)
                .toolbarBackground(.visible, for: .navigationBar)
                .toolbarColorScheme(.dark, for: .navigationBar)
                .toolbar {
                    ToolbarItem(placement: .principal) {
                        Text("Terminal")
                            .font(.headline.weight(.semibold))
                            .foregroundStyle(.white)
                            .frame(minWidth: 180, minHeight: IOSMetrics.controlSize)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                model.dismissTerminalKeyboard()
                            }
                            .accessibilityLabel("Dismiss keyboard")
                    }
                    ToolbarItemGroup(placement: .topBarLeading) {
                        Button {
                            withAnimation(IOSMotion.drawerSpring) {
                                model.showSideMenu.toggle()
                            }
                        } label: {
                            Image(systemName: "line.3.horizontal")
                        }
                        .accessibilityLabel("Menu")

                        Menu {
                            Button {
                                Task { await model.refreshTerminals() }
                            } label: {
                                Label("Refresh", systemImage: "arrow.clockwise")
                            }
                            if !model.terminalTabs.isEmpty {
                                Divider()
                            }
                            ForEach(model.terminalTabs) { tab in
                                Button {
                                    Task { await model.attachTerminal(tab) }
                                } label: {
                                    Label(tab.cwd ?? tab.terminalID, systemImage: "terminal")
                                }
                            }
                        } label: {
                            Image(systemName: "list.bullet")
                        }
                        .accessibilityLabel("Terminals")
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        Menu {
                            Button {
                                Task { await model.openTerminal() }
                            } label: {
                                Label("Open terminal", systemImage: "plus")
                            }
                            Button {
                                Task { await model.detachSelectedTerminal() }
                            } label: {
                                Label("Detach", systemImage: "rectangle.portrait.and.arrow.right")
                            }
                            .disabled(model.selectedTerminalID.isEmpty)
                            Button(role: .destructive) {
                                Task { await model.closeSelectedTerminal() }
                            } label: {
                                Label("Close", systemImage: "xmark.circle")
                            }
                            .disabled(model.selectedTerminalID.isEmpty)
                        } label: {
                            Image(systemName: "ellipsis.circle")
                        }
                        .accessibilityLabel("Terminal actions")
                    }
                }
                .safeAreaInset(edge: .bottom, spacing: 0) {
                    TerminalShortcutBar()
                }
                .task(id: model.selectedTerminalID) {
                    await model.activateTerminalPage()
                }
            }
            .background(terminalBackground.ignoresSafeArea())
        }
    }
}

struct TerminalShortcutBar: View {
    @EnvironmentObject private var model: AppModel

    /// Terminal area uses a persistent dark background regardless of system color scheme.
    private let barBackground = IOSColors.terminalBackground
    private let buttonBackground = Color(uiColor: UIColor(white: 1, alpha: 0.12))
    private let buttonForeground = Color(uiColor: .white)
    private let dividerColor = Color(uiColor: UIColor(white: 1, alpha: 0.16))

    var body: some View {
        VStack(spacing: 0) {
            Divider()
                .overlay(dividerColor)
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
        .background(barBackground.ignoresSafeArea(edges: .bottom))
    }

    private func shortcutButton(_ title: String, value: String) -> some View {
        Button {
            model.terminalShortcut(value)
        } label: {
            Text(title)
                .font(.callout.weight(.semibold))
                .foregroundStyle(buttonForeground)
                .padding(.horizontal, 12)
                .frame(minHeight: IOSMetrics.controlSize)
                .background(buttonBackground, in: Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(title)
    }
}
