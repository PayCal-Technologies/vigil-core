# Homebrew Packaging

Every stable release deterministically generates `dist/vigil.rb` from the four
final archive digests after macOS signing. The release workflow installs,
styles, audits, and tests the formula on macOS, then publishes it to
`PayCal-Technologies/homebrew-tap` using a separately scoped
`HOMEBREW_TAP_TOKEN`. Before stable publication, both native macOS targets
install the exact formula against their matching downloaded draft archive
through a byte-identical, canonically named temporary local copy and URL. The
canonical `vigil-X.Y.Z.tar.gz` name preserves Homebrew's version inference.
After publication, both targets repeat install, online audit, and test through
the unchanged public URLs before the tap is updated.

Official references:

- Homebrew Formula Cookbook: https://docs.brew.sh/Formula-Cookbook
- Homebrew Acceptable Formulae: https://docs.brew.sh/Acceptable-Formulae

## Generated Formula

```bash
RELEASE_TAG=v0.5.0 VERSION=0.5.0 \
  scripts/generate-homebrew-formula.sh dist
```

The generated formula is emitted with current Ruby/Sorbet style headers and a
class description. It chooses the exact macOS or Linux amd64/arm64 archive,
pins its SHA-256 digest, and installs:

- `vigil`;
- `vigil-plugin-publisher`;
- the manpage;
- Bash, Zsh, and Fish completions.

Its tests exercise injected version metadata, embedded official packs, and the
publisher companion. The formula is included in `SHA256SUMS`, Sigstore signing,
and GitHub artifact attestations.

After the corresponding release assets are public:

```bash
ruby -c dist/vigil.rb
brew style dist/vigil.rb
brew tap-new --no-git vigil/release-candidate
cp dist/vigil.rb \
  "$(brew --repository vigil/release-candidate)/Formula/vigil.rb"
brew audit --strict vigil/release-candidate/vigil
brew install --build-from-source vigil/release-candidate/vigil
brew audit --strict --online --installed \
  vigil/release-candidate/vigil
brew test vigil/release-candidate/vigil
```

Current Homebrew requires formulae to live in a tap; raw formula-path installs
and audits are intentionally not part of the release contract. The workflow
also verifies the immutable release and exact `vigil.rb` asset before placing
the formula in its ephemeral test tap.

## Submission Path

Homebrew/core is appropriate only after Vigil has clear public utility, stable
versioned releases, and user-visible adoption. Until then, use a project tap or
recommend `go install`.

Expected path:

1. Publish signed or clearly versioned GitHub releases.
2. Keep release notes and checksums attached to each tag.
3. Let the stable release job publish the generated formula in the maintained
   project tap.
4. Preserve the successful install, audit, and test run as immutable release
   evidence.
5. After adoption and formula maturity, open a pull request to Homebrew/homebrew-core.

## Package Requirements

- The project license must be machine-detectable. This repo uses 0BSD.
- Release tags must be immutable.
- Formula tests must exercise the installed binary, not just `--version`.
- The binary must not self-update or write outside normal user-selected paths.
- Setup commands must remain explicit about mutations.
