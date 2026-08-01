#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

tag="${RELEASE_TAG:-${GITHUB_REF_NAME:-}}"
version="${VERSION:-${tag#v}}"
if [[ -z "$tag" || -z "$version" || "$tag" != "v$version" || ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  printf 'release tag must have the exact form v<version>; got tag=%q version=%q\n' "$tag" "$version" >&2
  exit 2
fi

commit="${COMMIT:-$(git rev-parse HEAD)}"
if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]] || ! git cat-file -e "${commit}^{commit}" 2>/dev/null; then
  printf 'release commit must resolve to a full Git commit: %q\n' "$commit" >&2
  exit 2
fi
if [[ "${ALLOW_UNTAGGED_RELEASE:-0}" != "1" ]]; then
  tag_type="$(git cat-file -t "$tag" 2>/dev/null || true)"
  if [[ "$tag_type" != "tag" ]]; then
    printf 'official releases require an annotated tag; %s is type %s\n' "$tag" "${tag_type:-missing}" >&2
    exit 2
  fi
  tag_commit="$(git rev-parse "${tag}^{commit}" 2>/dev/null || true)"
  if [[ "$tag_commit" != "$commit" ]]; then
    printf 'release tag %s must resolve exactly to commit %s; got %s\n' "$tag" "$commit" "${tag_commit:-missing}" >&2
    exit 2
  fi
fi
if [[ "${ALLOW_DIRTY_RELEASE:-0}" != "1" ]] && [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
  printf 'release builds require a clean Git worktree\n' >&2
  exit 2
fi
source_date_epoch="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct "$commit")}"
build_date="${BUILD_DATE:-$(git show -s --format=%cI "$commit")}"
if [[ ! "$source_date_epoch" =~ ^[0-9]+$ ]] || (( source_date_epoch <= 0 )); then
  printf 'SOURCE_DATE_EPOCH must be a positive integer; got %q\n' "$source_date_epoch" >&2
  exit 2
fi
dist_input="${DIST_DIR:-$repo_root/dist}"
dist_name="$(basename "$dist_input")"
if [[ -z "$dist_input" || "$dist_input" == "/" || "$dist_name" == "." || "$dist_name" == ".." ]]; then
  printf 'refusing unsafe release directory: %q\n' "$dist_input" >&2
  exit 2
fi
dist_parent_input="$(dirname "$dist_input")"
mkdir -p "$dist_parent_input"
dist_parent="$(cd "$dist_parent_input" && pwd -P)"
dist="$dist_parent/$dist_name"
repo_root="$(cd "$repo_root" && pwd -P)"
case "$dist/" in
  "$repo_root/"|"$repo_root/.git/"*)
    printf 'refusing unsafe release directory: %q\n' "$dist" >&2
    exit 2
    ;;
esac
if [[ -L "$dist" || ( -e "$dist" && ! -d "$dist" ) ]]; then
  printf 'release directory must be a directory, not a file or symlink: %q\n' "$dist" >&2
  exit 2
fi
buildinfo_package="github.com/PayCal-Technologies/vigil-public/internal/buildinfo"
ldflags="-s -w -X ${buildinfo_package}.Version=${version} -X ${buildinfo_package}.Commit=${commit} -X ${buildinfo_package}.BuildDate=${build_date} -X ${buildinfo_package}.Dirty=false"

export SOURCE_DATE_EPOCH="$source_date_epoch"
export CGO_ENABLED=0

gate_tmp="$(mktemp -d)"
trap 'rm -rf "$gate_tmp"' EXIT
gate_checker="$gate_tmp/v1-acceptance-check"
go build -mod=readonly -buildvcs=false -trimpath -o "$gate_checker" ./scripts/v1-acceptance-check.go

rm -rf -- "$dist"
mkdir -p "$dist"
gate_report="$dist/v1-acceptance-gate.json"
set +e
"$gate_checker" --version "$version" --json > "$gate_report"
gate_status=$?
set -e
if [[ "$gate_status" != "0" ]]; then
  cat "$gate_report" >&2
  exit "$gate_status"
fi
"$gate_checker" --version "$version"
mkdir -p "$dist/assets/completions" "$dist/staging"

host_binary="$dist/.host-vigil"
go build -mod=readonly -buildvcs=false -trimpath -ldflags="$ldflags" -o "$host_binary" ./cmd/vigil

reported_version="$("$host_binary" version --json | sed -n 's/.*"version": "\([^"]*\)".*/\1/p' | head -n 1)"
if [[ "$reported_version" != "$version" ]]; then
  printf 'binary version mismatch: tag=%s binary=%s\n' "$version" "$reported_version" >&2
  exit 1
fi

"$host_binary" manpage > "$dist/assets/vigil.1"
"$host_binary" completion bash > "$dist/assets/completions/vigil.bash"
"$host_binary" completion zsh > "$dist/assets/completions/_vigil"
"$host_binary" completion fish > "$dist/assets/completions/vigil.fish"

for target in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
  os="${target%/*}"
  arch="${target#*/}"
  archive_root="vigil_${version}_${os}_${arch}"
  stage="$dist/staging/$archive_root"
  mkdir -p "$stage/completions" "$stage/schemas"

  GOOS="$os" GOARCH="$arch" go build \
    -mod=readonly \
    -buildvcs=false \
    -trimpath \
    -ldflags="$ldflags" \
    -o "$stage/vigil" \
    ./cmd/vigil
  chmod 0755 "$stage/vigil"
  GOOS="$os" GOARCH="$arch" go build \
    -mod=readonly \
    -buildvcs=false \
    -trimpath \
    -ldflags="$ldflags" \
    -o "$stage/vigil-plugin-publisher" \
    ./cmd/vigil-plugin-publisher
  chmod 0755 "$stage/vigil-plugin-publisher"
  cp README.md LICENSE "$stage/"
  cp "$dist/assets/vigil.1" "$stage/"
  cp "$dist/v1-acceptance-gate.json" "$stage/"
  cp "$dist/assets/completions/"* "$stage/completions/"
  cp schemas/*.schema.json "$stage/schemas/"

  go run ./cmd/vigil-release-archive \
    -source "$stage" \
    -root "$archive_root" \
    -output "$dist/${archive_root}.tar.gz" \
    -epoch "$source_date_epoch"
done

rm -rf "$dist/staging" "$dist/.host-vigil"
if [[ "${SKIP_HOMEBREW_FORMULA:-0}" != "1" ]]; then
  "$repo_root/scripts/generate-homebrew-formula.sh" "$dist"
fi
"$repo_root/scripts/release-checksums.sh" "$dist"
