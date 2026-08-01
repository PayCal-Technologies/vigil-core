#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

go_bin="${GO:-go}"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

binary="$temporary/vigil"
CGO_ENABLED=0 GOTOOLCHAIN=local "$go_bin" build -trimpath -o "$binary" ./cmd/vigil
GOTOOLCHAIN=local "$go_bin" run ./scripts/performance-check.go --binary "$binary" "$@"
