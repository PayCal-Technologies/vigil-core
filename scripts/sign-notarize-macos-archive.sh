#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 ARCHIVE VERSION GOARCH SOURCE_DATE_EPOCH" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

archive="$1"
version="$2"
goarch="$3"
source_date_epoch="$4"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS signing requires a Darwin host" >&2
  exit 2
fi
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid semantic version: $version" >&2
  exit 2
fi
case "$goarch" in
  amd64) macho_arch="x86_64" ;;
  arm64) macho_arch="arm64" ;;
  *)
    echo "unsupported macOS architecture: $goarch" >&2
    exit 2
    ;;
esac
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]] || (( source_date_epoch <= 0 )); then
  echo "SOURCE_DATE_EPOCH must be a positive integer" >&2
  exit 2
fi
if [[ ! -f "$archive" || -L "$archive" ]]; then
  echo "release archive must be a regular file: $archive" >&2
  exit 2
fi
archive="$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")"

required_environment=(
  APPLE_SIGNING_IDENTITY
  APPLE_NOTARY_KEY_PATH
  APPLE_NOTARY_KEY_ID
  APPLE_NOTARY_ISSUER_ID
)
for name in "${required_environment[@]}"; do
  if [[ -z "${!name:-}" ]]; then
    echo "required signing environment is empty: $name" >&2
    exit 2
  fi
done
if [[ ! -f "$APPLE_NOTARY_KEY_PATH" || -L "$APPLE_NOTARY_KEY_PATH" ]]; then
  echo "notary private key must be a regular file" >&2
  exit 2
fi
for tool in codesign ditto find lipo plutil spctl tar xcrun; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required macOS signing tool not found: $tool" >&2
    exit 4
  fi
done

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
release_root_name="vigil_${version}_darwin_${goarch}"
release_root="$temporary/$release_root_name"

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
if [[ ! -d "$release_root" ]]; then
  echo "archive root is missing: $release_root_name" >&2
  exit 2
fi
if [[ -n "$(find "$release_root" -type l -print -quit)" ]]; then
  echo "macOS signing input must not contain symlinks" >&2
  exit 2
fi

executables=(
  "$release_root/vigil"
  "$release_root/vigil-plugin-publisher"
)
for executable in "${executables[@]}"; do
  if [[ ! -f "$executable" || ! -x "$executable" ]]; then
    echo "archive executable is missing: $executable" >&2
    exit 2
  fi
  case " $(lipo -archs "$executable") " in
    *" $macho_arch "*) ;;
    *)
      echo "unexpected Mach-O architecture for $executable" >&2
      exit 2
      ;;
  esac
  codesign \
    --force \
    --options runtime \
    --timestamp \
    --sign "$APPLE_SIGNING_IDENTITY" \
    "$executable"
  codesign --verify --strict --verbose=2 "$executable"
done

notary_zip="$temporary/${release_root_name}.zip"
ditto -c -k --keepParent "$release_root" "$notary_zip"
notary_result="$temporary/${release_root_name}.notary-result.json"
xcrun notarytool submit "$notary_zip" \
  --key "$APPLE_NOTARY_KEY_PATH" \
  --key-id "$APPLE_NOTARY_KEY_ID" \
  --issuer "$APPLE_NOTARY_ISSUER_ID" \
  --wait \
  --output-format json > "$notary_result"
submission_id="$(plutil -extract id raw -o - "$notary_result")"
submission_status="$(plutil -extract status raw -o - "$notary_result")"
if [[ -z "$submission_id" || "$submission_status" != "Accepted" ]]; then
  cat "$notary_result" >&2
  echo "notarization was not accepted" >&2
  exit 1
fi
notary_log="$temporary/${release_root_name}.notary-log.json"
xcrun notarytool log "$submission_id" \
  --key "$APPLE_NOTARY_KEY_PATH" \
  --key-id "$APPLE_NOTARY_KEY_ID" \
  --issuer "$APPLE_NOTARY_ISSUER_ID" \
  "$notary_log"
plutil -lint "$notary_log"
cat "$notary_log"

if [[ -n "${VIGIL_NOTARY_LOG_DIR:-}" ]]; then
  if [[ -L "$VIGIL_NOTARY_LOG_DIR" ]]; then
    echo "notary evidence directory must not be a symlink" >&2
    exit 2
  fi
  mkdir -p "$VIGIL_NOTARY_LOG_DIR"
  cp "$notary_result" \
    "$VIGIL_NOTARY_LOG_DIR/${release_root_name}.notary-result.json"
  cp "$notary_log" \
    "$VIGIL_NOTARY_LOG_DIR/${release_root_name}.notary-log.json"
  chmod 0644 \
    "$VIGIL_NOTARY_LOG_DIR/${release_root_name}.notary-result.json" \
    "$VIGIL_NOTARY_LOG_DIR/${release_root_name}.notary-log.json"
fi

for executable in "${executables[@]}"; do
  codesign --verify --strict --verbose=2 "$executable"
  spctl --assess --type execute --verbose=4 "$executable"
done

signed_archive="$temporary/$(basename "$archive")"
go run ./cmd/vigil-release-archive \
  -source "$release_root" \
  -root "$release_root_name" \
  -output "$signed_archive" \
  -epoch "$source_date_epoch"
chmod 0644 "$signed_archive"
mv "$signed_archive" "$archive"

echo "signed and notarized macOS archive: $(basename "$archive")"
