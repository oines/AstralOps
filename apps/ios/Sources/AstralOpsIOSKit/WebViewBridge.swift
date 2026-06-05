import Foundation
import ObjectiveC.runtime
import SwiftUI
import UIKit
import WebKit

struct WebViewMediaResponse {
    var data: Data
    var mimeType: String
}

@MainActor
final class WebViewBridge: ObservableObject {
    private weak var webView: WKWebView?
    private var isReady = false
    private var pendingScripts: [String] = []
    var onReady: (() -> Void)?

    func attach(_ webView: WKWebView) {
        self.webView = webView
        isReady = false
        pendingScripts.removeAll()
    }

    func markReady() {
        isReady = true
        flush()
        onReady?()
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
    var onTranscriptAction: ((JSONValue) -> Void)?
    var onTerminalInput: ((String) -> Void)?
    var onTerminalResize: ((String, Int, Int) -> Void)?
    var onTerminalHeartbeatAck: ((String, Int, Int) -> Void)?
    var onMediaRequest: ((URL) async throws -> WebViewMediaResponse)?

    func makeCoordinator() -> Coordinator {
        Coordinator(
            bridge: bridge,
            onTranscriptAction: onTranscriptAction,
            onTerminalInput: onTerminalInput,
            onTerminalResize: onTerminalResize,
            onTerminalHeartbeatAck: onTerminalHeartbeatAck,
            onMediaRequest: onMediaRequest
        )
    }

    func makeUIView(context: Context) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.allowsInlineMediaPlayback = true
        configuration.setURLSchemeHandler(context.coordinator.mediaSchemeHandler, forURLScheme: "astralmedia")
        configuration.userContentController.add(context.coordinator, name: "astralReady")
        configuration.userContentController.add(context.coordinator, name: "astralTranscriptAction")
        configuration.userContentController.add(context.coordinator, name: "astralTerminalInput")
        configuration.userContentController.add(context.coordinator, name: "astralTerminalResize")
        configuration.userContentController.add(context.coordinator, name: "astralTerminalHeartbeatAck")

        let webView = LocalHTMLWKWebView(frame: .zero, configuration: configuration)
        let backgroundColor = Self.backgroundColor(for: resourceName)
        let isTerminal = resourceName.hasPrefix("terminal")
        webView.hidesKeyboardAccessoryView = isTerminal
        webView.isOpaque = true
        webView.backgroundColor = backgroundColor
        webView.scrollView.isOpaque = true
        webView.scrollView.backgroundColor = backgroundColor
        webView.scrollView.contentInsetAdjustmentBehavior = .never
        webView.scrollView.isScrollEnabled = !isTerminal
        webView.scrollView.bounces = !isTerminal
        webView.scrollView.alwaysBounceVertical = false
        webView.navigationDelegate = context.coordinator
        bridge.attach(webView)
        context.coordinator.loadedResourceName = resourceName
        loadResource(into: webView)
        return webView
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        context.coordinator.onTranscriptAction = onTranscriptAction
        context.coordinator.onTerminalInput = onTerminalInput
        context.coordinator.onTerminalResize = onTerminalResize
        context.coordinator.onTerminalHeartbeatAck = onTerminalHeartbeatAck
        context.coordinator.mediaSchemeHandler.onMediaRequest = onMediaRequest
        if let localWebView = webView as? LocalHTMLWKWebView {
            localWebView.hidesKeyboardAccessoryView = resourceName.hasPrefix("terminal")
        }
        let isTerminal = resourceName.hasPrefix("terminal")
        webView.scrollView.isScrollEnabled = !isTerminal
        webView.scrollView.bounces = !isTerminal
        webView.scrollView.alwaysBounceVertical = false
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

    final class Coordinator: NSObject, WKNavigationDelegate, WKScriptMessageHandler {
        private let bridge: WebViewBridge
        let mediaSchemeHandler: MediaSchemeHandler
        var loadedResourceName: String?
        var onTranscriptAction: ((JSONValue) -> Void)?
        var onTerminalInput: ((String) -> Void)?
        var onTerminalResize: ((String, Int, Int) -> Void)?
        var onTerminalHeartbeatAck: ((String, Int, Int) -> Void)?

        init(
            bridge: WebViewBridge,
            onTranscriptAction: ((JSONValue) -> Void)?,
            onTerminalInput: ((String) -> Void)?,
            onTerminalResize: ((String, Int, Int) -> Void)?,
            onTerminalHeartbeatAck: ((String, Int, Int) -> Void)?,
            onMediaRequest: ((URL) async throws -> WebViewMediaResponse)?
        ) {
            self.bridge = bridge
            self.onTranscriptAction = onTranscriptAction
            self.onTerminalInput = onTerminalInput
            self.onTerminalResize = onTerminalResize
            self.onTerminalHeartbeatAck = onTerminalHeartbeatAck
            self.mediaSchemeHandler = MediaSchemeHandler(onMediaRequest: onMediaRequest)
        }

        func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
            if message.name == "astralReady" {
                Task { @MainActor in bridge.markReady() }
            } else if message.name == "astralTranscriptAction", let payload = Self.jsonValue(from: message.body) {
                onTranscriptAction?(payload)
            } else if message.name == "astralTerminalInput", let data = message.body as? String {
                onTerminalInput?(data)
            } else if message.name == "astralTerminalResize", let payload = Self.dictionary(from: message.body) {
                let terminalID = Self.string(payload["terminal_id"] ?? payload["terminalId"])
                let cols = Self.int(payload["cols"])
                let rows = Self.int(payload["rows"])
                if !terminalID.isEmpty, cols > 0, rows > 0 {
                    onTerminalResize?(terminalID, cols, rows)
                }
            } else if message.name == "astralTerminalHeartbeatAck", let payload = Self.dictionary(from: message.body) {
                let terminalID = Self.string(payload["terminal_id"] ?? payload["terminalId"])
                let heartbeatSeq = Self.int(payload["heartbeat_seq"] ?? payload["heartbeatSeq"])
                let renderedSeq = Self.int(payload["rendered_seq"] ?? payload["renderedSeq"])
                if !terminalID.isEmpty, heartbeatSeq > 0 {
                    onTerminalHeartbeatAck?(terminalID, heartbeatSeq, renderedSeq)
                }
            }
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            (webView as? LocalHTMLWKWebView)?.refreshKeyboardAccessoryPolicy()
        }

        private static func jsonValue(from body: Any) -> JSONValue? {
            guard JSONSerialization.isValidJSONObject(body),
                  let data = try? JSONSerialization.data(withJSONObject: body)
            else { return nil }
            return try? JSONCoding.decode(JSONValue.self, from: data)
        }

        private static func dictionary(from body: Any) -> [String: Any]? {
            body as? [String: Any]
        }

        private static func string(_ value: Any?) -> String {
            value as? String ?? ""
        }

        private static func int(_ value: Any?) -> Int {
            if let int = value as? Int { return int }
            if let number = value as? NSNumber { return number.intValue }
            if let string = value as? String { return Int(string) ?? 0 }
            return 0
        }
    }
}

final class MediaSchemeHandler: NSObject, WKURLSchemeHandler {
    var onMediaRequest: ((URL) async throws -> WebViewMediaResponse)?

    init(onMediaRequest: ((URL) async throws -> WebViewMediaResponse)?) {
        self.onMediaRequest = onMediaRequest
    }

    func webView(_ webView: WKWebView, start urlSchemeTask: WKURLSchemeTask) {
        guard let url = urlSchemeTask.request.url, let onMediaRequest else {
            urlSchemeTask.didFailWithError(NSError(domain: "AstralOpsIOS", code: 404, userInfo: [NSLocalizedDescriptionKey: "Media is unavailable."]))
            return
        }
        Task {
            do {
                let media = try await onMediaRequest(url)
                let response = URLResponse(
                    url: url,
                    mimeType: media.mimeType,
                    expectedContentLength: media.data.count,
                    textEncodingName: nil
                )
                urlSchemeTask.didReceive(response)
                urlSchemeTask.didReceive(media.data)
                urlSchemeTask.didFinish()
            } catch {
                urlSchemeTask.didFailWithError(error)
            }
        }
    }

    func webView(_ webView: WKWebView, stop urlSchemeTask: WKURLSchemeTask) {}
}

final class LocalHTMLWKWebView: WKWebView {
    var hidesKeyboardAccessoryView = false {
        didSet {
            guard hidesKeyboardAccessoryView != oldValue else { return }
            refreshKeyboardAccessoryPolicy()
        }
    }

    override var inputAccessoryView: UIView? {
        hidesKeyboardAccessoryView ? nil : super.inputAccessoryView
    }

    override var inputAssistantItem: UITextInputAssistantItem {
        let item = super.inputAssistantItem
        item.leadingBarButtonGroups = []
        item.trailingBarButtonGroups = []
        return item
    }

    override func didMoveToWindow() {
        super.didMoveToWindow()
        refreshKeyboardAccessoryPolicy()
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        refreshKeyboardAccessoryPolicy()
    }

    func refreshKeyboardAccessoryPolicy() {
        guard hidesKeyboardAccessoryView else { return }
        hideKeyboardAccessoryView(in: self)
    }

    private func hideKeyboardAccessoryView(in view: UIView) {
        let className = NSStringFromClass(type(of: view))
        if className.contains("WKContent"),
           !className.contains("_AstralNoInputAccessory"),
           let subclass = Self.accessoryFreeSubclass(for: type(of: view)) {
            object_setClass(view, subclass)
            view.reloadInputViews()
        }
        view.subviews.forEach { hideKeyboardAccessoryView(in: $0) }
    }

    private static func accessoryFreeSubclass(for baseClass: AnyClass) -> AnyClass? {
        let subclassName = "\(NSStringFromClass(baseClass))_AstralNoInputAccessory"
        if let existing = NSClassFromString(subclassName) {
            return existing
        }
        guard let subclass = objc_allocateClassPair(baseClass, subclassName, 0) else {
            return nil
        }
        let block: @convention(block) (AnyObject) -> UIView? = { _ in nil }
        let implementation = imp_implementationWithBlock(block)
        class_addMethod(subclass, Selector(("inputAccessoryView")), implementation, "@@:")
        objc_registerClassPair(subclass)
        return subclass
    }
}
