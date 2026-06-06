import SwiftUI
import UIKit

/// Baseline layout constants. Views needing Dynamic Type scaling should declare
/// their own `@ScaledMetric` properties using these values as defaults.
enum IOSMetrics {
    static let compactEdge: CGFloat = 12
    static let controlGap: CGFloat = 8
    /// Apple HIG minimum touch target: 44pt.
    static let controlSize: CGFloat = 44
    /// Input field height — meets 44pt minimum.
    static let fieldHeight: CGFloat = 44
    static let bottomVerticalPadding: CGFloat = 8
}

enum IOSColors {
    static let pageBackground = Color(uiColor: .systemBackground)
    static let groupedPageBackground = Color(uiColor: .systemGroupedBackground)
    static let terminalBackground = Color(uiColor: UIColor(red: 5 / 255, green: 6 / 255, blue: 7 / 255, alpha: 1))
}

enum IOSMotion {
    static let drawerSpring = Animation.interactiveSpring(response: 0.34, dampingFraction: 0.88, blendDuration: 0.12)
}

struct RootView: View {
    @EnvironmentObject private var model: AppModel
    private let sideMenuWidth: CGFloat = 280
    private let edgeGestureWidth: CGFloat = 72
    private let topGestureExclusionHeight: CGFloat = 220
    private let bottomGestureExclusionHeight: CGFloat = 260
    @State private var drawerInteractiveOffset: CGFloat = 0

    private var clampedProgress: CGFloat {
        guard sideMenuWidth > 0 else { return 0 }
        return drawerOffset / sideMenuWidth
    }

    private var drawerOffset: CGFloat {
        let baseOffset = model.showSideMenu ? sideMenuWidth : 0
        return max(0, min(sideMenuWidth, baseOffset + drawerInteractiveOffset))
    }

    var body: some View {
        ZStack(alignment: .leading) {
            rootBackground
                .ignoresSafeArea()

            SideMenuView()
                .frame(width: sideMenuWidth, alignment: .leading)
                .offset(x: (clampedProgress - 1) * 28)
                .opacity(0.5 + 0.5 * clampedProgress)

            Group {
                switch model.page {
                case .transcript:
                    TranscriptPage()
                case .files:
                    FilesPage()
                case .terminal:
                    TerminalPage()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(rootBackground)
            .clipShape(RoundedRectangle(cornerRadius: 30 * clampedProgress, style: .continuous))
            .compositingGroup()
            .shadow(color: .black.opacity(0.2 * clampedProgress), radius: 24 * clampedProgress, x: -6 * clampedProgress, y: 8 * clampedProgress)
            .scaleEffect(1.0 - (0.045 * clampedProgress))
            .offset(x: drawerOffset)
            .allowsHitTesting(clampedProgress < 0.001)

            if clampedProgress > 0 {
                DrawerGestureOverlay(
                    capturesWholeView: true,
                    edgeWidth: edgeGestureWidth,
                    leadingExclusion: sideMenuWidth,
                    topExclusion: 0,
                    bottomExclusion: 0,
                    isOpen: model.showSideMenu,
                    onChanged: handleDrawerPanChanged(_:),
                    onEnded: handleDrawerPanEnded(translation:velocity:),
                    onCancelled: resetDrawerDrag,
                    onTap: { settleDrawer(open: false) }
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .ignoresSafeArea()
                .zIndex(2)
            }

            if !model.showSideMenu {
                DrawerGestureOverlay(
                    capturesWholeView: false,
                    edgeWidth: edgeGestureWidth,
                    leadingExclusion: 0,
                    topExclusion: topGestureExclusionHeight,
                    bottomExclusion: bottomGestureExclusionHeight,
                    isOpen: model.showSideMenu,
                    onChanged: handleDrawerPanChanged(_:),
                    onEnded: handleDrawerPanEnded(translation:velocity:),
                    onCancelled: resetDrawerDrag,
                    onTap: nil
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .ignoresSafeArea()
                .zIndex(3)
            }
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
        .sheet(isPresented: $model.workspaceCreatorPresented) {
            WorkspaceCreateSheet()
                .presentationDetents([.large])
                .presentationDragIndicator(.visible)
        }
        .sheet(isPresented: $model.sessionCreatorPresented) {
            SessionCreateSheet()
                .presentationDetents([.medium])
                .presentationDragIndicator(.visible)
        }
        .sheet(item: Binding(
            get: { model.mediaPreview.map(MediaPreviewItem.init) },
            set: { if $0 == nil { model.mediaPreview = nil } }
        )) { item in
            MediaPreviewSheet(media: item.media)
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

    private var rootBackground: Color {
        if clampedProgress > 0 {
            return IOSColors.pageBackground
        }
        switch model.page {
        case .terminal:
            return IOSColors.terminalBackground
        case .files:
            return IOSColors.groupedPageBackground
        case .transcript:
            return IOSColors.pageBackground
        }
    }

    private func handleDrawerPanChanged(_ translation: CGFloat) {
        let nextOffset = model.showSideMenu ? min(0, translation) : max(0, translation)
        setDrawerInteractiveOffset(nextOffset)
    }

    private func handleDrawerPanEnded(translation: CGFloat, velocity: CGFloat) {
        let baseOffset = model.showSideMenu ? sideMenuWidth : 0
        let interactiveOffset = model.showSideMenu ? min(0, translation) : max(0, translation)
        let currentOffset = max(0, min(sideMenuWidth, baseOffset + interactiveOffset))
        let projectedOffset = max(0, min(sideMenuWidth, currentOffset + velocity * 0.14))
        let shouldOpen: Bool
        if abs(velocity) > 650 {
            shouldOpen = velocity > 0
        } else {
            shouldOpen = projectedOffset > sideMenuWidth * 0.5
        }
        settleDrawer(open: shouldOpen)
    }

    private func resetDrawerDrag() {
        withAnimation(IOSMotion.drawerSpring) {
            drawerInteractiveOffset = 0
        }
    }

    private func settleDrawer(open: Bool) {
        if open != model.showSideMenu {
            UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        }
        withAnimation(IOSMotion.drawerSpring) {
            model.showSideMenu = open
            drawerInteractiveOffset = 0
        }
    }

    private func setDrawerInteractiveOffset(_ value: CGFloat) {
        var transaction = Transaction()
        transaction.disablesAnimations = true
        withTransaction(transaction) {
            drawerInteractiveOffset = max(-sideMenuWidth, min(sideMenuWidth, value))
        }
    }
}

private struct DrawerGestureOverlay: UIViewRepresentable {
    let capturesWholeView: Bool
    let edgeWidth: CGFloat
    let leadingExclusion: CGFloat
    let topExclusion: CGFloat
    let bottomExclusion: CGFloat
    let isOpen: Bool
    let onChanged: (CGFloat) -> Void
    let onEnded: (CGFloat, CGFloat) -> Void
    let onCancelled: () -> Void
    let onTap: (() -> Void)?

    func makeUIView(context: Context) -> DrawerGesturePassthroughView {
        let view = DrawerGesturePassthroughView()
        view.backgroundColor = .clear
        view.capturesWholeView = capturesWholeView
        view.edgeWidth = edgeWidth
        view.leadingExclusion = leadingExclusion
        view.topExclusion = topExclusion
        view.bottomExclusion = bottomExclusion

        let panRecognizer = UIPanGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handlePan(_:)))
        panRecognizer.cancelsTouchesInView = false
        panRecognizer.delaysTouchesBegan = false
        panRecognizer.delaysTouchesEnded = false
        panRecognizer.delegate = context.coordinator
        view.addGestureRecognizer(panRecognizer)

        let tapRecognizer = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleTap(_:)))
        tapRecognizer.cancelsTouchesInView = false
        tapRecognizer.delegate = context.coordinator
        view.addGestureRecognizer(tapRecognizer)

        return view
    }

    func updateUIView(_ view: DrawerGesturePassthroughView, context: Context) {
        view.capturesWholeView = capturesWholeView
        view.edgeWidth = edgeWidth
        view.leadingExclusion = leadingExclusion
        view.topExclusion = topExclusion
        view.bottomExclusion = bottomExclusion
        context.coordinator.isOpen = isOpen
        context.coordinator.onChanged = onChanged
        context.coordinator.onEnded = onEnded
        context.coordinator.onCancelled = onCancelled
        context.coordinator.onTap = onTap
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(isOpen: isOpen, onChanged: onChanged, onEnded: onEnded, onCancelled: onCancelled, onTap: onTap)
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var isOpen: Bool
        var onChanged: (CGFloat) -> Void
        var onEnded: (CGFloat, CGFloat) -> Void
        var onCancelled: () -> Void
        var onTap: (() -> Void)?

        init(
            isOpen: Bool,
            onChanged: @escaping (CGFloat) -> Void,
            onEnded: @escaping (CGFloat, CGFloat) -> Void,
            onCancelled: @escaping () -> Void,
            onTap: (() -> Void)?
        ) {
            self.isOpen = isOpen
            self.onChanged = onChanged
            self.onEnded = onEnded
            self.onCancelled = onCancelled
            self.onTap = onTap
        }

        @objc func handlePan(_ recognizer: UIPanGestureRecognizer) {
            let translation = recognizer.translation(in: recognizer.view).x
            let velocity = recognizer.velocity(in: recognizer.view).x
            switch recognizer.state {
            case .began, .changed:
                onChanged(translation)
            case .ended:
                onEnded(translation, velocity)
            case .cancelled, .failed:
                onCancelled()
            default:
                break
            }
        }

        @objc func handleTap(_ recognizer: UITapGestureRecognizer) {
            guard recognizer.state == .ended else { return }
            onTap?()
        }

        func gestureRecognizerShouldBegin(_ gestureRecognizer: UIGestureRecognizer) -> Bool {
            if gestureRecognizer is UITapGestureRecognizer {
                return onTap != nil
            }
            guard let panRecognizer = gestureRecognizer as? UIPanGestureRecognizer else {
                return true
            }
            let velocity = panRecognizer.velocity(in: panRecognizer.view)
            let horizontal = abs(velocity.x)
            let vertical = abs(velocity.y)
            guard horizontal > max(18, vertical * 1.15) else { return false }
            return isOpen ? velocity.x < 0 : velocity.x > 0
        }

        func gestureRecognizer(_ gestureRecognizer: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer) -> Bool {
            false
        }
    }
}

private final class DrawerGesturePassthroughView: UIView {
    var bottomExclusion: CGFloat = 0
    var capturesWholeView = false
    var edgeWidth: CGFloat = 0
    var leadingExclusion: CGFloat = 0
    var topExclusion: CGFloat = 0

    override func point(inside point: CGPoint, with event: UIEvent?) -> Bool {
        let maxY = bounds.height - bottomExclusion
        guard maxY > topExclusion else { return false }
        let inVerticalRange = point.y >= topExclusion && point.y <= maxY
        if capturesWholeView {
            return point.x >= leadingExclusion && inVerticalRange
        }
        return point.x >= leadingExclusion
            && point.x <= leadingExclusion + edgeWidth
            && inVerticalRange
    }
}
