#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

fuzz_time="${FUZZ_TIME:-2s}"
targets=(
  "./internal/config:FuzzConfigDocumentRoundTrip"
  "./internal/packs:FuzzPackManifestParsing"
  "./internal/packs:FuzzPathConfinement"
  "./internal/runner:FuzzCommandLineParsing"
  "./internal/atomicfile:FuzzAtomicWriteRoundTrip"
  "./internal/plugins:FuzzStrictPluginHandshakeJSON"
  "./internal/support:FuzzSupportRedaction"
)

for target in "${targets[@]}"; do
  package="${target%%:*}"
  fuzz="${target#*:}"
  go test "$package" -run='^$' -fuzz="^${fuzz}$" -fuzztime="$fuzz_time" -parallel=2
done
