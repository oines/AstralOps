import AuthenticationServices
import Foundation
import UIKit

struct CloudOAuthLoginCode: Equatable {
    var baseURL: String
    var loginCode: String
}

@MainActor
final class CloudOAuthCoordinator: NSObject, ObservableObject, ASWebAuthenticationPresentationContextProviding {
    private var session: ASWebAuthenticationSession?

    func requestLoginCode(provider: String, baseURL: String) async throws -> CloudOAuthLoginCode {
        let normalizedBaseURL = normalizeBaseURL(baseURL)
        let state = try randomState()
        let redirectURI = "astralops://cloud-auth/callback"
        let authURL = try authStartURL(provider: provider, baseURL: normalizedBaseURL, redirectURI: redirectURI, state: state)

        return try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(url: authURL, callbackURLScheme: "astralops") { callbackURL, error in
                Task { @MainActor in
                    self.session = nil
                    if let error {
                        continuation.resume(throwing: error)
                        return
                    }
                    guard let callbackURL else {
                        continuation.resume(throwing: CloudOAuthError.missingCallback)
                        return
                    }
                    do {
                        let loginCode = try self.parseLoginCode(from: callbackURL, expectedState: state)
                        continuation.resume(returning: CloudOAuthLoginCode(baseURL: normalizedBaseURL, loginCode: loginCode))
                    } catch {
                        continuation.resume(throwing: error)
                    }
                }
            }
            session.presentationContextProvider = self
            session.prefersEphemeralWebBrowserSession = false
            self.session = session
            if !session.start() {
                self.session = nil
                continuation.resume(throwing: CloudOAuthError.couldNotStart)
            }
        }
    }

    nonisolated func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        MainActor.assumeIsolated {
            let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
            return scenes.flatMap { $0.windows }.first { $0.isKeyWindow } ?? ASPresentationAnchor()
        }
    }

    private func authStartURL(provider: String, baseURL: String, redirectURI: String, state: String) throws -> URL {
        guard var components = URLComponents(string: "\(baseURL)/v1/auth/\(provider)/start") else {
            throw CloudOAuthError.invalidBaseURL
        }
        components.queryItems = [
            URLQueryItem(name: "redirect_uri", value: redirectURI),
            URLQueryItem(name: "state", value: state)
        ]
        guard let url = components.url else { throw CloudOAuthError.invalidBaseURL }
        return url
    }

    private func parseLoginCode(from url: URL, expectedState: String) throws -> String {
        guard let components = URLComponents(url: url, resolvingAgainstBaseURL: false) else {
            throw CloudOAuthError.invalidCallback
        }
        let query = Dictionary(uniqueKeysWithValues: (components.queryItems ?? []).compactMap { item in
            item.value.map { (item.name, $0) }
        })
        if let error = query["error"], !error.isEmpty {
            throw CloudOAuthError.providerError(error)
        }
        guard query["state"] == expectedState else {
            throw CloudOAuthError.stateMismatch
        }
        guard let loginCode = query["login_code"], !loginCode.isEmpty else {
            throw CloudOAuthError.missingLoginCode
        }
        return loginCode
    }

    private func normalizeBaseURL(_ value: String) -> String {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        let withoutTrailingSlash = trimmed.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        return withoutTrailingSlash.isEmpty ? "https://cloud-astralops.oines.dev" : withoutTrailingSlash
    }

    private func randomState() throws -> String {
        var bytes = [UInt8](repeating: 0, count: 24)
        let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        guard status == errSecSuccess else { throw CloudOAuthError.randomFailed }
        return Data(bytes).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}

enum CloudOAuthError: LocalizedError {
    case couldNotStart
    case invalidBaseURL
    case invalidCallback
    case missingCallback
    case missingLoginCode
    case providerError(String)
    case randomFailed
    case stateMismatch

    var errorDescription: String? {
        switch self {
        case .couldNotStart:
            return "Cloud sign-in could not be started."
        case .invalidBaseURL:
            return "Cloud URL is invalid."
        case .invalidCallback:
            return "Cloud sign-in returned an invalid callback."
        case .missingCallback:
            return "Cloud sign-in did not return to AstralOps."
        case .missingLoginCode:
            return "Cloud sign-in did not return a login code."
        case .providerError(let message):
            return message
        case .randomFailed:
            return "Cloud sign-in state could not be generated."
        case .stateMismatch:
            return "Cloud sign-in state mismatch."
        }
    }
}
