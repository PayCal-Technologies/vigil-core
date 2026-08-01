#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dist="${1:-$repo_root/dist}"
tag="${RELEASE_TAG:-${GITHUB_REF_NAME:-}}"
version="${VERSION:-${tag#v}}"

if [[ -z "$tag" || "$tag" != "v$version" || ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  printf 'Homebrew formula generation requires RELEASE_TAG=v<semantic-version>\n' >&2
  exit 2
fi
if [[ ! -d "$dist" ]]; then
  printf 'release directory does not exist: %s\n' "$dist" >&2
  exit 2
fi

checksum() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    printf 'release archive does not exist: %s\n' "$file" >&2
    exit 2
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

darwin_arm64="vigil_${version}_darwin_arm64.tar.gz"
darwin_amd64="vigil_${version}_darwin_amd64.tar.gz"
linux_arm64="vigil_${version}_linux_arm64.tar.gz"
linux_amd64="vigil_${version}_linux_amd64.tar.gz"

darwin_arm64_sha="$(checksum "$dist/$darwin_arm64")"
darwin_amd64_sha="$(checksum "$dist/$darwin_amd64")"
linux_arm64_sha="$(checksum "$dist/$linux_arm64")"
linux_amd64_sha="$(checksum "$dist/$linux_amd64")"

output="$dist/vigil.rb"
temporary="$(mktemp "$dist/.vigil.rb.XXXXXX")"
trap 'rm -f "$temporary"' EXIT

cat > "$temporary" <<EOF
# typed: strict
# frozen_string_literal: true

# Vigil is a policy-aware repository preflight engine.
class Vigil < Formula
  desc "Policy-aware repository preflight engine"
  homepage "https://github.com/PayCal-Technologies/vigil-public"
  license "0BSD"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/PayCal-Technologies/vigil-public/releases/download/$tag/$darwin_arm64"
      sha256 "$darwin_arm64_sha"
    else
      url "https://github.com/PayCal-Technologies/vigil-public/releases/download/$tag/$darwin_amd64"
      sha256 "$darwin_amd64_sha"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/PayCal-Technologies/vigil-public/releases/download/$tag/$linux_arm64"
      sha256 "$linux_arm64_sha"
    else
      url "https://github.com/PayCal-Technologies/vigil-public/releases/download/$tag/$linux_amd64"
      sha256 "$linux_amd64_sha"
    end
  end

  def install
    bin.install "vigil"
    bin.install "vigil-plugin-publisher"
    man1.install "vigil.1"
    bash_completion.install "completions/vigil.bash" => "vigil"
    zsh_completion.install "completions/_vigil"
    fish_completion.install "completions/vigil.fish"
  end

  test do
    assert_match "\\"version\\": \\"#{version}\\"", shell_output("#{bin}/vigil version --json")
    assert_match "\\"count\\": 10", shell_output("#{bin}/vigil extensions:list --json")
    assert_match "\\"version\\": \\"#{version}\\"", shell_output("#{bin}/vigil-plugin-publisher version --json")
  end
end
EOF

chmod 0644 "$temporary"
mv "$temporary" "$output"
trap - EXIT
printf 'generated Homebrew formula: %s\n' "$output"
