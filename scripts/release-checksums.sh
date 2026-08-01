#!/usr/bin/env bash
set -euo pipefail

dist="${1:-dist}"
cd "$dist"

checksum() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1"
  else
    shasum -a 256 "$1"
  fi
}

: > SHA256SUMS
while IFS= read -r file; do
  checksum "${file#./}" >> SHA256SUMS
done < <(find . -maxdepth 1 -type f \
  ! -name SHA256SUMS \
  ! -name '*.sigstore.json' \
  -print | LC_ALL=C sort)
