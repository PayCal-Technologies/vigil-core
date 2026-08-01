# Compatibility Policy

Vigil is pre-1.0. Compatibility changes are allowed only when they include a
migration, compatibility alias, or explicit release-note warning.

## Current Commitments

- Config schema upgrades are explicit and never downgrade future schemas.
- Unknown config fields are preserved during migration where structurally
  possible.
- Colon-style commands and `extensions:*` pack terminology remain compatibility
  aliases during the CLI simplification.
- Published exit meanings are not reassigned.
- Output envelope schema `1`, plan schema `1`, pack manifest schema `1`, plugin
  protocol schema `1`, conformance report schema `1`, plugin index schema `1`,
  publisher-store schema `1`, plugin lock schema `1`, and plugin trust schema
  `1` are published compatibility surfaces with checked-in contract tests.
- A plan never applies after a binary, config, repository, registry, or pack
  digest mismatch.
- Pack loading precedence is deterministic.
- Plugin command bindings include exact semantic version and executable digest.
- Protocol `1` plugins use host API `v1`; incompatible handshakes fail closed.
- Repository plugin locks never substitute for local capability approval.
- Signed indexes require an exact version, a non-expired threshold-valid
  Ed25519 signature set, and HTTPS or confined relative artifacts.
- Signed acquisition provenance cannot be silently downgraded to local.
- Release tags must match the version reported by the binary.

## Stability Levels

- `stable`: intended to remain compatible through the current pre-1.0 series.
- `experimental`: may change with release-note notice.
- `deprecated`: remains available for a documented transition period.

## v1.0 Gate

Vigil will not claim 1.0 until configuration, JSON envelope, exit taxonomy,
plan integrity, plugin protocol, supported platforms, release verification, and
deprecation periods have stable documented contracts and compatibility
fixtures.

Operational upgrade, binary rollback, config backup restoration, plan
invalidation, and exact plugin rollback are documented in
[Upgrade and Rollback](upgrading.md).

The candidate compatibility baseline is defined in
[v1 Contract Freeze](v1-contracts.md). Its local, operational, and independent
review evidence is tracked separately in the
[v1 Acceptance Matrix](v1-acceptance-matrix.md).
