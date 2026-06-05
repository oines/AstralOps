# AstralOps iOS

iOS-first native Controller shell for AstralOps.

The iOS app is intentionally thin:

- Swift owns iOS UX, Keychain, lifecycle, JSON bridge calls, and WebView plumbing.
- Go `pkg/mobilecore` owns Mesh, E2EE control, Host routing, pairing, session input, event streams, and terminal streams.
- Transcript semantics stay in shared TypeScript and run inside the transcript `WKWebView`.
- Terminal rendering uses locally bundled xterm.js inside a `WKWebView`.

Swift must not derive Mesh state, implement a terminal state machine, semantically map events, or infer pending interaction outcomes.

## Generate Local Web Assets

```bash
node apps/ios/scripts/build-web-assets.mjs
```

Generated `apps/ios/Resources/*.html` WebView bundles are local build artifacts and are not committed.

## Build Go Mobile Framework

```bash
apps/ios/scripts/build-mobilecore-framework.sh
```

The framework output is intentionally not committed.
