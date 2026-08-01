# Releasing Vigil

Tagged releases are built by `.github/workflows/release.yml`. Do not upload
locally built binaries as official assets.

Stable tags use `vX.Y.Z`. Prerelease tags such as `vX.Y.Z-beta.N` produce beta
releases without replacing the latest stable release. Scheduled and manually
dispatched nightlies use `.github/workflows/nightly.yml`; see
[`release-channels.md`](release-channels.md).

## Artifacts

Each stable or beta release publishes:

- Developer ID-signed and Apple-notarized static macOS amd64 and arm64
  archives;
- static Linux amd64 and arm64 archives;
- the offline `vigil-plugin-publisher` key and threshold-index utility;
- a release-specific `vigil.rb` formula with all four archive digests;
- README, license, manpage, completions, the v1 acceptance gate report, and
  public JSON Schemas inside every archive;
- `v1-acceptance-gate.json`;
- `SHA256SUMS`;
- a keyless Sigstore bundle for `SHA256SUMS`;
- Apple notary submission results and complete notary logs for both macOS
  architectures;
- an SPDX JSON SBOM;
- GitHub build-provenance attestations.

The workflow first builds the v1 acceptance checker, records the
schema-versioned gate report, and proves byte-for-byte reproducibility of the
unsigned candidate before artifact generation. An ephemeral macOS keychain then
signs both executables in
each macOS archive with hardened runtime and a secure timestamp, submits a ZIP
of each archive root to Apple's notary service, performs Gatekeeper assessment,
retrieves the complete notary log, and repacks the accepted signed bytes.
Checksums, formula, SBOM, Sigstore bundle, and attestations are generated only
after those signed archives and notarization records replace the unsigned
candidates. Blocked stable attempts still upload the gate report as workflow
evidence even though release artifacts are not generated.

The workflow uploads final assets to a draft release, downloads them again, and
verifies checksums, the Sigstore identity, and GitHub attestations while the
release is still unpublished. A second four-target native matrix then verifies
each matching archive on Linux amd64, Linux arm64, macOS Intel, and macOS Apple
Silicon. macOS jobs repeat `codesign` and online Gatekeeper assessment before
exercising both binaries, the command catalogue, config schema, and all
embedded official packs from empty user and repository state.

Only after the native matrix passes does Vigil publish a beta candidate in its
final prerelease state. Stable candidates remain drafts for an additional
Intel and Apple Silicon Homebrew test: the workflow styles and audits the exact
formula, rewrites only the matching archive URL in a temporary copy to the
already verified local draft asset, then installs and tests it. The original
formula is never changed.

Vigil first proves through GitHub's repository API that release immutability is
enabled, then publishes a draft exactly once in its final channel state: beta
as prerelease, stable as latest. The workflow requires the resulting release
to report `isImmutable=true` and verifies its GitHub release attestation.
Stable publication is followed by a second dual-architecture install and
online audit through the public release URLs, then project-tap publication.
Failure in those downstream distribution checks leaves the immutable release
assets intact but keeps the workflow and Homebrew acceptance criterion open.

## Release Checklist

1. Ensure the permanent quality workflow is green.
2. Update release notes and migration documentation.
3. Run the release builder locally with a candidate version:

   ```bash
   RELEASE_TAG=v0.2.0 scripts/build-release.sh
   ```

4. Confirm every unsigned candidate file is reproducible:

   ```bash
   RELEASE_TAG=v0.2.0 scripts/check-release-reproducibility.sh dist
   ```
5. Create and push an annotated semantic-version tag:

   ```bash
   git tag -a v0.2.0 -m "v0.2.0"
   git push origin v0.2.0
   ```

6. Wait for draft verification, native smoke, candidate Homebrew validation,
   final channel publication, public-URL Homebrew validation, and tap
   publication to complete.
7. Collect the operational v1 evidence report from the live public release:

   ```bash
   go run ./scripts/collect-v1-operational-evidence.go \
     --repo PayCal-Technologies/vigil-public \
     --tag v0.2.0 \
     --tap-repo PayCal-Technologies/homebrew-tap \
     --workflow-run-id "$GITHUB_RUN_ID" \
     --require-release-proof \
     --output docs/reviews/v1-operational-evidence-v0.2.0.json
   ```

   Add `--plugin-index`, `--plugin-ceremony-url`, and repeated
   `--publisher-key` flags after the production publisher key ceremony. Use
   `--require-plugin-index-proof` only when the ceremony and live index are
   expected to close VIGIL-AC-18.

8. Verify one downloaded artifact independently:

   ```bash
   sha256sum -c SHA256SUMS
   gh attestation verify vigil_0.2.0_linux_amd64.tar.gz \
     --repo PayCal-Technologies/vigil-public
   cosign verify-blob SHA256SUMS \
     --bundle SHA256SUMS.sigstore.json \
     --certificate-identity \
       https://github.com/PayCal-Technologies/vigil-public/.github/workflows/release.yml@refs/tags/v0.2.0 \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```

## Injected Metadata

The build script injects version, commit, commit timestamp, and `dirty=false`
into `internal/buildinfo`, uses `CGO_ENABLED=0`, disables ambient VCS metadata,
and sets `SOURCE_DATE_EPOCH` for generated and archived content. The build
fails unless the semantic-version tag resolves exactly to the selected commit,
the worktree is clean, and `v<version>` exactly matches the reported binary
version. CI rebuilds the unsigned candidate directory and compares every file
byte-for-byte before signing. Apple secure timestamps intentionally make final
signed Mach-O bytes non-reproducible; their identity is instead bound by final
checksums, keyless Sigstore identity, GitHub attestations, notarization, and
native pre-publication verification.

`ALLOW_DIRTY_RELEASE=1` and `ALLOW_UNTAGGED_RELEASE=1` are local development
overrides. Official release automation never sets them.
If the v1 acceptance gate blocks a local stable build, the build stops before
compilation but still writes `dist/v1-acceptance-gate.json`; that file is the
durable evidence for why no release artifacts were produced.

## Release Credentials

Stable and beta tags fail closed unless these GitHub Actions secrets exist:

- `APPLE_DEVELOPER_ID_CERTIFICATE_BASE64`;
- `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`;
- `APPLE_SIGNING_IDENTITY`;
- `APPLE_NOTARY_PRIVATE_KEY_BASE64`;
- `APPLE_NOTARY_KEY_ID`;
- `APPLE_NOTARY_ISSUER_ID`;
- `RELEASE_ADMIN_READ_TOKEN`.

Store these as secrets on a reviewer-protected GitHub `release` environment.
`RELEASE_ADMIN_READ_TOKEN` must be a fine-grained token scoped only to this
repository with read-only `Administration` permission. It is used only to call
GitHub's immutable-releases status endpoint before signing. Enable GitHub
release immutability before the first operational release; the workflow fails
closed when the policy cannot be proven. Its draft-first, single-publication
sequence is designed for that repository policy.
The certificate and App Store Connect notary key are written only beneath
`RUNNER_TEMP`; the workflow deletes the ephemeral keychain and key files in an
`always()` cleanup step. Standalone command-line binaries cannot carry a
stapled ticket directly, so Vigil submits the containing ZIP and relies on the
notary service ticket plus online Gatekeeper assessment after download.

Stable tags also require `HOMEBREW_TAP_TOKEN`, scoped to update
`PayCal-Technologies/homebrew-tap`. No channel or install path is considered
operational until a real immutable run supplies the acceptance evidence.

## v1 Evidence Report

`scripts/collect-v1-operational-evidence.go` is the canonical maintainer-side
collector for VIGIL-AC-09 and VIGIL-AC-11 through VIGIL-AC-13. It verifies the
repository immutable-release policy, release immutability, `gh release verify`,
downloaded asset checksums, Sigstore checksum identity, GitHub attestations,
the successful release workflow jobs, and the published tap formula. The report
uses `schemas/vigil-operational-evidence-v1.schema.json` and is intentionally
separate from release assets because public immutable releases cannot be
amended after publication.
Required textual report fields, criterion details, command records, and
publisher-key provenance must contain non-whitespace content.
The immutable-release policy check uses `RELEASE_ADMIN_READ_TOKEN` by default;
other release, workflow, attestation, and tap checks use the normal `GH_TOKEN`
available to `gh`.
When the collector runs inside the release workflow, pass
`--workflow-run-id "$GITHUB_RUN_ID"` so it can verify already-completed
prerequisite jobs before the current workflow has reached a terminal success
state. When run later from a maintainer machine, omit the flag and the
collector searches for a completed successful `release.yml` run at the
immutable release commit.

The same collector can close VIGIL-AC-18 when maintainers pass the production
plugin index, the immutable ceremony record URL, and enough reviewed publisher
public keys to satisfy the threshold.
It records independent-review criteria as pending unless those have already
been committed in the acceptance ledger; maintainers must not use this
operational report as a substitute for third-party integration, RFC, security,
or usability evidence.
When moving an operational criterion to `verified`, reference the committed
report in `docs/v1-acceptance.json` with evidence kind `operational_report`.
The ledger itself follows `schemas/vigil-v1-acceptance-ledger-v1.schema.json`.
The acceptance gate validates the report and requires the same criterion to be
`verified` inside the report before a stable v1 build can proceed. Verified
criterion evidence entries in the report must be public HTTPS records; digests
and checksums belong in the typed release, download, and plugin-index fields.
Reports cited from the ledger must match the candidate being checked:
operational reports must match the requested version and, for release-backed
criteria, the release commit; external reports must match both the repository
HEAD and the requested version. Workflow-backed operational reports cited by
the stable gate must also point to a completed successful workflow run.
Verified operational claims are criterion-aware: AC09 must carry native
workflow smoke jobs, AC11 must carry the exact release/download asset set,
AC12 must carry stable Homebrew workflow proof, AC13 must carry macOS
signing/notary assets and jobs, and AC18 must carry a threshold-signed plugin
index with verified artifacts. Operational reports can only verify those five
criteria; AC22 is proven by the acceptance gate report and ledger validation,
not by a collector-local success claim.
Maintainers can run `go run ./scripts/v1-acceptance-check.go --version 1.0.0
--json` to capture the schema-versioned gate report described by
`schemas/vigil-v1-acceptance-gate-v1.schema.json`. Successful release
candidates include `v1-acceptance-gate.json` both as a top-level release asset
and inside every archive; the release smoke test and operational collector
verify that it remains present. The collector also parses that report with
strict unknown-field rejection and requires `not_required` for pre-v1 or
prerelease artifacts and `satisfied` for stable v1-or-newer artifacts.
