#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

baseline="${1:-dist}"
if [[ ! -d "$baseline" ]]; then
  printf 'baseline release directory does not exist: %s\n' "$baseline" >&2
  exit 2
fi
baseline="$(cd "$baseline" && pwd)"

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
candidate="$temporary/dist"

DIST_DIR="$candidate" "$repo_root/scripts/build-release.sh"

(
  cd "$baseline"
  find . -type f -print | LC_ALL=C sort
) > "$temporary/baseline-files"
(
  cd "$candidate"
  find . -type f -print | LC_ALL=C sort
) > "$temporary/candidate-files"

diff -u "$temporary/baseline-files" "$temporary/candidate-files"
while IFS= read -r relative; do
  cmp "$baseline/$relative" "$candidate/$relative"
done < "$temporary/baseline-files"

printf 'release artifacts are byte-identical across two builds\n'
