#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUT_DIR="$ROOT_DIR/apps/ios/Frameworks"
export PATH="$(go env GOPATH)/bin:$PATH"

if ! command -v gomobile >/dev/null 2>&1; then
  echo "gomobile is not installed. Run: go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"
cd "$ROOT_DIR"

gomobile bind \
  -target=ios \
  -o "$OUT_DIR/Mobilecore.xcframework" \
  ./pkg/mobilecore

echo "Built $OUT_DIR/Mobilecore.xcframework"
