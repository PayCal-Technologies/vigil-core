# Homebrew Packaging

This repository is being prepared as a Homebrew candidate. No tap is included in
this repository yet.

Official references:

- Homebrew Formula Cookbook: https://docs.brew.sh/Formula-Cookbook
- Homebrew Acceptable Formulae: https://docs.brew.sh/Acceptable-Formulae

## Candidate Formula

```ruby
class Vigil < Formula
  desc "Repository preflight, setup, and agent-safety CLI"
  homepage "https://github.com/PayCal-Technologies/vigil-public"
  url "https://github.com/PayCal-Technologies/vigil-public/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_RELEASE_TARBALL_SHA256"
  license "0BSD"
  head "https://github.com/PayCal-Technologies/vigil-public.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/vigil"
    generate_completions_from_executable(bin/"vigil", "completion")
    man1.install Utils.safe_popen_read(bin/"vigil", "manpage") => "vigil.1"
  end

  test do
    assert_match "vigil", shell_output("#{bin}/vigil version")
    assert_match "schema_version", shell_output("#{bin}/vigil config:template --json")
  end
end
```

## Submission Path

Homebrew/core is appropriate only after Vigil has clear public utility, stable
versioned releases, and user-visible adoption. Until then, use a project tap or
recommend `go install`.

Expected path:

1. Publish signed or clearly versioned GitHub releases.
2. Keep release notes and checksums attached to each tag.
3. Add or maintain a formula in a future tap.
4. Run `brew audit --strict --online vigil` and `brew test vigil`.
5. After adoption and formula maturity, open a pull request to Homebrew/homebrew-core.

## Package Requirements

- The project license must be machine-detectable. This repo uses 0BSD.
- Release tags must be immutable.
- Formula tests must exercise the installed binary, not just `--version`.
- The binary must not self-update or write outside normal user-selected paths.
- Setup commands must remain explicit about mutations.
