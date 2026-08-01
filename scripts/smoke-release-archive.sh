#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 ARCHIVE VERSION GOOS GOARCH" >&2
  exit 2
fi

archive="$1"
version="$2"
expected_os="$3"
expected_arch="$4"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"

for tool in jq tar; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool not found: $tool" >&2
    exit 4
  fi
done
if [[ ! -f "$archive" || -L "$archive" ]]; then
  echo "release archive must be a regular file: $archive" >&2
  exit 2
fi

case "$(uname -s)" in
  Linux) actual_os="linux" ;;
  Darwin) actual_os="darwin" ;;
  *)
    echo "unsupported smoke-test operating system: $(uname -s)" >&2
    exit 2
    ;;
esac
case "$(uname -m)" in
  x86_64|amd64) actual_arch="amd64" ;;
  arm64|aarch64) actual_arch="arm64" ;;
  *)
    echo "unsupported smoke-test architecture: $(uname -m)" >&2
    exit 2
    ;;
esac
if [[ "$actual_os" != "$expected_os" || "$actual_arch" != "$expected_arch" ]]; then
  echo "native runner mismatch: expected ${expected_os}/${expected_arch}, got ${actual_os}/${actual_arch}" >&2
  exit 2
fi
if [[ "$actual_os" == "darwin" && "${ALLOW_UNSIGNED_MACOS_SMOKE:-0}" != "1" ]]; then
  for tool in codesign spctl; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "required macOS verification tool not found: $tool" >&2
      exit 4
    fi
  done
fi

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
release_root_name="vigil_${version}_${expected_os}_${expected_arch}"
while IFS= read -r entry; do
  if [[ "$entry" == /* ]]; then
    echo "archive contains an absolute path: $entry" >&2
    exit 2
  fi
  case "/$entry/" in
    *"/../"*|*"/./"*)
      echo "archive contains path traversal: $entry" >&2
      exit 2
      ;;
  esac
  case "$entry" in
    "$release_root_name"|"$release_root_name/"|"$release_root_name/"*) ;;
    *)
      echo "archive entry escapes expected root: $entry" >&2
      exit 2
      ;;
  esac
done < <(tar -tzf "$archive")
while read -r mode _; do
  case "${mode:0:1}" in
    -|d) ;;
    *)
      echo "archive contains a non-regular entry: $mode" >&2
      exit 2
      ;;
  esac
done < <(tar -tvzf "$archive")
tar -xzf "$archive" -C "$temporary"

release_root="$temporary/$release_root_name"
binary="$release_root/vigil"
publisher="$release_root/vigil-plugin-publisher"
schema_dir="$release_root/schemas"
for executable in "$binary" "$publisher"; do
  if [[ ! -f "$executable" || -L "$executable" || ! -x "$executable" ]]; then
    echo "archive executable is missing or unsafe: $executable" >&2
    exit 2
  fi
done
if [[ ! -d "$schema_dir" || -L "$schema_dir" ]]; then
  echo "archive schema directory is missing or unsafe: $schema_dir" >&2
  exit 2
fi
expected_schema_count=0
while IFS= read -r schema_path; do
  schema_name="$(basename "$schema_path")"
  packaged_schema="$schema_dir/$schema_name"
  if [[ ! -f "$packaged_schema" || -L "$packaged_schema" ]]; then
    echo "archive schema is missing or unsafe: $schema_name" >&2
    exit 2
  fi
  jq empty "$packaged_schema" >/dev/null
  expected_schema_count=$((expected_schema_count + 1))
done < <(find "$repo_root/schemas" -maxdepth 1 -type f -name '*.schema.json' -print | sort)
actual_schema_count="$(find "$schema_dir" -maxdepth 1 -type f -name '*.schema.json' -print | wc -l | tr -d '[:space:]')"
if [[ "$actual_schema_count" != "$expected_schema_count" ]]; then
  echo "archive schema count mismatch: expected $expected_schema_count, got $actual_schema_count" >&2
  exit 2
fi
gate_report="$release_root/v1-acceptance-gate.json"
if [[ ! -f "$gate_report" || -L "$gate_report" ]]; then
  echo "archive acceptance gate report is missing or unsafe: $gate_report" >&2
  exit 2
fi
if [[ "$actual_os" == "darwin" && "${ALLOW_UNSIGNED_MACOS_SMOKE:-0}" != "1" ]]; then
  for executable in "$binary" "$publisher"; do
    codesign --verify --strict --verbose=2 "$executable"
    spctl --assess --type execute --verbose=4 "$executable"
  done
fi

"$binary" version --json > "$temporary/version.json"
jq -e \
  --arg version "$version" \
  --arg os "$expected_os" \
  --arg arch "$expected_arch" \
  '.schema_version == "1" and
   .status == "ok" and
   .data.build.version == $version and
   .data.build.dirty == "false" and
   .data.build.os == $os and
   .data.build.arch == $arch' \
  "$temporary/version.json" >/dev/null

jq -e \
  --arg version "$version" \
  '.schema_version == "1" and
   .target == "v1.0" and
   .version == $version and
   .acceptance_ledger == "docs/v1-acceptance.json" and
   (.status == "not_required" or .status == "satisfied") and
   (.pending_count == 0)' \
  "$gate_report" >/dev/null

"$publisher" version --json > "$temporary/publisher-version.json"
jq -e \
  --arg version "$version" \
  '.schema_version == "1" and .status == "ok" and .data.build.version == $version and .data.build.dirty == "false"' \
  "$temporary/publisher-version.json" >/dev/null

mkdir -p "$temporary/empty-home" "$temporary/empty-repository"
(
  cd "$temporary/empty-repository"
  HOME="$temporary/empty-home" \
    VIGIL_PLUGIN_ROOT="$temporary/plugins" \
    VIGIL_USER_PACK_ROOT="$temporary/user-packs" \
    "$binary" extensions:list --json > packs.json
  HOME="$temporary/empty-home" \
    VIGIL_PLUGIN_ROOT="$temporary/plugins" \
    VIGIL_USER_PACK_ROOT="$temporary/user-packs" \
    "$binary" list --json > commands.json
  HOME="$temporary/empty-home" \
    VIGIL_PLUGIN_ROOT="$temporary/plugins" \
    VIGIL_USER_PACK_ROOT="$temporary/user-packs" \
    "$binary" config:schema --json > config-schema.json
)

jq -e \
  '.status == "ok" and .data.count == 10 and all(.data.extensions[]; .origin == "embedded-official")' \
  "$temporary/empty-repository/packs.json" >/dev/null
jq -e \
  '.status == "ok" and (.data | length) >= 1 and any(.data[]; .command == "workflow:local")' \
  "$temporary/empty-repository/commands.json" >/dev/null
jq -e \
  '.status == "ok" and .data.schema_version == "3" and
   (.data.gate_execution.graph_fields | index("depends_on")) != null' \
  "$temporary/empty-repository/config-schema.json" >/dev/null

echo "release archive smoke passed: ${expected_os}/${expected_arch} ${version}"
