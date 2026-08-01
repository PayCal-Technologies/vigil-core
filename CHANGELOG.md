# Changelog

All notable public changes are documented here.

This project uses semantic version tags. Prerelease tags such as
`v0.2.0-beta.1` are public candidates; stable v1 tags are blocked until the v1
acceptance ledger is complete.

## Unreleased

- Polish release, Homebrew, and GitHub repository metadata.

## v0.2.0-beta.1 - Planned

This beta is intended to validate Vigil's new release and distribution contract
before any stable v1 claim.

### Added

- Typed command registry used by help, command discovery, completions, manpages,
  and machine-readable command metadata.
- Versioned configuration, output, plan, run-artifact, plugin protocol, plugin
  index, publisher, lock, and trust schemas.
- Digest-bound reviewed plans that fail closed when execution inputs change.
- Cancellable direct-argv command execution with explicit shell opt-in.
- Git-visible mutation detection for read-only gates, including workflow graph
  execution with bounded parallel read-only groups.
- Embedded official packs that work without a Vigil source checkout.
- Digest-bound subprocess plugin execution with explicit local capability
  approval and conformance testing.
- Release automation for signed macOS archives, Linux archives, checksums, SPDX
  SBOMs, Sigstore checksum bundles, GitHub attestations, native archive smoke,
  and generated Homebrew formula validation.
- v1 acceptance gate reporting through `v1-acceptance-gate.json`.

### Changed

- `extensions:*` commands remain as compatibility aliases for declarative packs;
  executable plugins now have their own plugin lifecycle and protocol.
- Workflow execution now exposes stable execution states and versioned machine
  output envelopes.
- Release builds require exact semantic-version metadata and annotated tags
  unless local development overrides are explicitly set.

### Known Gaps

- Stable `v1.0.0` remains blocked until external and operational acceptance
  criteria are independently verified.
- Apple signing/notarization credentials and `RELEASE_ADMIN_READ_TOKEN` still
  need to be configured in the GitHub `release` environment before the planned
  beta tag is pushed. Apple setup is intentionally deferred for now.
- Public Homebrew install instructions should remain disabled until the project
  tap publishes and validates a formula from public release URLs.

## v0.1.0

- Initial public source release.
