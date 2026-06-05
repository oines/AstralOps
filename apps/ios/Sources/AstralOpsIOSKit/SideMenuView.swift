import SwiftUI

struct SideMenuView: View {
    @EnvironmentObject private var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Header
            VStack(alignment: .leading, spacing: 4) {
                Text("AstralOps")
                    .font(.title2.weight(.bold))
                if let identity = model.identity {
                    Text(identity.deviceName ?? "AstralOps iPhone")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 80)
            .padding(.bottom, 40)

            // Menu Items
            VStack(spacing: 8) {
                SideMenuRow(icon: "desktopcomputer", title: "Devices", isSelected: model.page == .navigator) {
                    selectPage(.navigator)
                }
                SideMenuRow(icon: "bubble.left.and.text.bubble.right", title: "Transcript", isSelected: model.page == .transcript) {
                    selectPage(.transcript)
                }
                SideMenuRow(icon: "folder", title: "Files", isSelected: model.page == .files) {
                    selectPage(.files)
                }
                SideMenuRow(icon: "terminal", title: "Terminal", isSelected: model.page == .terminal) {
                    selectPage(.terminal)
                }
            }
            .padding(.horizontal, 16)

            Spacer()

            // Footer
            VStack(spacing: 8) {
                Divider()
                    .padding(.horizontal, 24)
                SideMenuRow(icon: "gearshape", title: "Settings", isSelected: false) {
                    withAnimation(IOSMotion.drawerSpring) {
                        model.showSideMenu = false
                    }
                    DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) {
                        model.settingsPresented = true
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 40)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func selectPage(_ page: AppModel.Page) {
        withAnimation(IOSMotion.drawerSpring) {
            model.page = page
            model.showSideMenu = false
        }
    }
}

private struct SideMenuRow: View {
    let icon: String
    let title: String
    let isSelected: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 16) {
                Image(systemName: icon)
                    .font(.system(size: 20))
                    .frame(width: 24)
                Text(title)
                    .font(isSelected ? .body.weight(.semibold) : .body)
                Spacer()
            }
            .padding(.vertical, 12)
            .padding(.horizontal, 16)
            .background(isSelected ? Color.accentColor.opacity(0.15) : Color.clear)
            .foregroundStyle(isSelected ? Color.accentColor : Color.primary)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
        .buttonStyle(.plain)
    }
}
