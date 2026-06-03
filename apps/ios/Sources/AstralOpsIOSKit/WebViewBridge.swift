import Foundation
import SwiftUI
import UIKit
import WebKit

@MainActor
final class WebViewBridge: ObservableObject {
    private weak var webView: WKWebView?
    private var isReady = false
    private var pendingScripts: [String] = []

    func attach(_ webView: WKWebView) {
        self.webView = webView
        isReady = false
        pendingScripts.removeAll()
    }

    func markReady() {
        isReady = true
        flush()
    }

    func postNative(type: String, payload: Data) {
        guard let json = String(data: payload, encoding: .utf8) else { return }
        enqueue("window.__ASTRAL_RECEIVE_NATIVE__ && window.__ASTRAL_RECEIVE_NATIVE__({\"type\":\(quote(type)),\"payload\":\(json)}); true;")
    }

    func postNative(type: String, payload: JSONValue) {
        guard let data = try? JSONCoding.encode(payload) else { return }
        postNative(type: type, payload: data)
    }

    private func enqueue(_ script: String) {
        if isReady, let webView {
            webView.evaluateJavaScript(script)
            return
        }
        pendingScripts.append(script)
    }

    private func flush() {
        guard let webView else { return }
        let scripts = pendingScripts
        pendingScripts.removeAll()
        scripts.forEach { webView.evaluateJavaScript($0) }
    }

    private func quote(_ value: String) -> String {
        let data = try? JSONEncoder().encode(value)
        return data.flatMap { String(data: $0, encoding: .utf8) } ?? "\"\""
    }
}

struct LocalHTMLWebView: UIViewRepresentable {
    let resourceName: String
    let bridge: WebViewBridge
    var onTerminalInput: ((String) -> Void)?

    func makeCoordinator() -> Coordinator {
        Coordinator(bridge: bridge, onTerminalInput: onTerminalInput)
    }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.allowsInlineMediaPlayback = true
        configuration.userContentController.add(context.coordinator, name: "astralReady")
        configuration.userContentController.add(context.coordinator, name: "astralTerminalInput")

        let webView = LocalHTMLWKWebView(frame: .zero, configuration: configuration)
        let backgroundColor = Self.backgroundColor(for: resourceName)
        webView.isOpaque = true
        webView.backgroundColor = backgroundColor
        webView.scrollView.isOpaque = true
        webView.scrollView.backgroundColor = backgroundColor
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        bridge.attach(webView)
        context.coordinator.loadedResourceName = resourceName
        loadResource(into: webView)
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.onTerminalInput = onTerminalInput
        if context.coordinator.loadedResourceName != resourceName {
            context.coordinator.loadedResourceName = resourceName
            let backgroundColor = Self.backgroundColor(for: resourceName)
            webView.backgroundColor = backgroundColor
            webView.scrollView.backgroundColor = backgroundColor
            bridge.attach(webView)
            loadResource(into: webView)
        }
    }

    private func loadResource(into webView: WKWebView) {
        guard let url = resourceURL() else {
            webView.loadHTMLString(Self.missingResourceHTML(resourceName), baseURL: nil)
            return
        }
        webView.loadFileURL(url, allowingReadAccessTo: url.deletingLastPathComponent())
    }

    private func resourceURL() -> URL? {
        if let url = Bundle.main.url(forResource: resourceName, withExtension: "html") {
            return url
        }
        return Bundle.main.resourceURL?
            .appendingPathComponent("Resources", isDirectory: true)
            .appendingPathComponent("\(resourceName).html")
    }

    private static func backgroundColor(for resourceName: String) -> UIColor {
        if resourceName.hasPrefix("terminal-dark") {
            return UIColor(red: 5 / 255, green: 6 / 255, blue: 7 / 255, alpha: 1)
        }
        if resourceName.hasPrefix("terminal") {
            return UIColor(red: 16 / 255, green: 18 / 255, blue: 20 / 255, alpha: 1)
        }
        return .systemBackground
    }

    private static func missingResourceHTML(_ resourceName: String) -> String {
        """
        <!doctype html>
        <html>
        <head>
          <meta name="viewport" content="width=device-width, initial-scale=1">
          <style>
            body {
              margin: 0;
              min-height: 100vh;
              display: grid;
              place-items: center;
              background: transparent;
              color: -apple-system-secondary-label;
              font: -apple-system-body;
              text-align: center;
            }
          </style>
        </head>
        <body>Resource unavailable: \(resourceName).html</body>
        </html>
        """
    }

    final class Coordinator: NSObject, WKScriptMessageHandler {
        private let bridge: WebViewBridge
        var loadedResourceName: String?
        var onTerminalInput: ((String) -> Void)?

        init(bridge: WebViewBridge, onTerminalInput: ((String) -> Void)?) {
            self.bridge = bridge
            self.onTerminalInput = onTerminalInput
        }

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            if message.name == "astralReady" {
                Task { @MainActor in bridge.markReady() }
            } else if message.name == "astralTerminalInput", let data = message.body as? String {
                onTerminalInput?(data)
            }
        }
    }
}

final class LocalHTMLWKWebView: WKWebView {
    override var inputAssistantItem: UITextInputAssistantItem {
        let item = super.inputAssistantItem
        item.leadingBarButtonGroups = []
        item.trailingBarButtonGroups = []
        return item
    }
}
