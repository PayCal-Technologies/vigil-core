---
name: Release checklist
about: Track a Vigil beta or stable release
labels: release
---

## Release

- Version:
- Channel: beta/stable
- Target commit:

## Pre-Tag Checks

- [ ] Permanent quality workflow is green on `main`.
- [ ] Release notes and `CHANGELOG.md` are updated.
- [ ] `docs/upgrading.md` and compatibility notes are current.
- [ ] GitHub release immutability is enabled.
- [ ] Reviewer-protected `release` environment exists.
- [ ] Required Apple signing and notary secrets are configured.
- [ ] `RELEASE_ADMIN_READ_TOKEN` is configured with read-only Administration
      permission for this repository.
- [ ] Stable only: `HOMEBREW_TAP_TOKEN` is configured for the project tap.
- [ ] `scripts/check-github-release-readiness.sh --tag vX.Y.Z` passes.
- [ ] Manual `Release Readiness` workflow passes and uploads JSON evidence.

## Local Candidate Smoke

- [ ] `RELEASE_TAG=vX.Y.Z scripts/build-release.sh`
- [ ] `RELEASE_TAG=vX.Y.Z scripts/check-release-reproducibility.sh dist`
- [ ] `scripts/smoke-release-archive.sh` passes for at least one local archive.
- [ ] `shasum -a 256 -c SHA256SUMS` passes in `dist`.
- [ ] Stable v1 only: `v1-acceptance-gate.json` reports `satisfied`.

## Tag

- [ ] Annotated tag created: `git tag -a vX.Y.Z -m "vX.Y.Z"`
- [ ] Tag pushed: `git push origin vX.Y.Z`

## GitHub Release Verification

- [ ] Draft release is created exactly once.
- [ ] Draft assets download and checksum verification passes.
- [ ] Sigstore checksum bundle verifies.
- [ ] GitHub attestations verify for all release assets.
- [ ] Native release smoke passes on Linux amd64, Linux arm64, macOS Intel, and
      macOS Apple Silicon.
- [ ] Homebrew draft formula styles, audits, installs, and tests on macOS Intel
      and Apple Silicon.
- [ ] Published release has the expected channel state.
- [ ] Published release reports immutable.

## Homebrew

- [ ] Stable only: public release formula installs, audits online, and tests on
      macOS Intel and Apple Silicon.
- [ ] Stable only: project tap commit is published.

## Evidence

- [ ] `v1-acceptance-gate.json` is attached to the release.
- [ ] Operational evidence artifact is uploaded by the release workflow.
- [ ] Stable v1 only: accepted operational and external evidence is committed to
      the ledger before tagging.
