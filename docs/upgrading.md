# Upgrade and Rollback

Treat a Vigil upgrade as a contract change review, even within the pre-1.0
series.

## Before Upgrade

1. Record `vigil version`.
2. Keep the current release archive and `SHA256SUMS`.
3. Commit or otherwise preserve `vigil.config.json` and
   `vigil.plugins.lock.json`.
4. Run `vigil verify --json`.
5. Preview configuration migration:

```bash
vigil config:migrate --json
```

Do not reuse a reviewed plan across a binary change. Plans bind the exact Vigil
executable digest and will fail closed after either upgrade or rollback.

## Upgrade

Install a checksum-verified stable or beta archive through the documented
channel, then run:

```bash
vigil version
vigil config:validate --json
vigil extensions:doctor --json
vigil plugins:doctor --json
vigil verify --json
vigil workflow:local --dry-run --json
```

If migration is required, review first and authorize the write separately:

```bash
vigil config:migrate --json
vigil --allow-mutation config:migrate --write --json
```

The write is atomic and creates one timestamped
`vigil.config.json.bak-*` file containing the exact pre-migration bytes. Unknown
fields are preserved where structurally possible. A future schema is never
downgraded.

Generate a new reviewed plan only after the upgraded binary, config, packs,
plugins, repository `HEAD`, and workspace have all been accepted.

## Binary Rollback

Reinstall the retained immutable archive for the previous version and verify it
against that release's checksums and provenance. Then run the same validation
sequence above.

If the old binary does not understand the migrated config, restore the exact
`.bak-*` file through the repository's normal reviewed file-change process.
Do not edit a backup in place. Preserve the newer config separately until the
rollback is accepted.

Any plan produced by the newer binary is intentionally unusable after rollback;
generate and review a new plan.

## Plugin Rollback

Plugin lock records bind exact ID, semantic version, executable SHA-256,
metadata SHA-256, capabilities, acquisition provenance, publisher set, and
signature threshold.

To roll back, acquire the exact previous artifact from its original signed
index and update explicitly:

```bash
vigil plugins:index:verify --index ./index-v1.json --json
vigil --allow-mutation plugins:update \
  --index ./index-v1.json \
  --id example \
  --version 1.2.2 \
  --approve-all
vigil plugins:doctor --json
```

There is no implicit latest selection. A semantic version cannot be replaced
with different bytes, signed provenance cannot silently become local
provenance, and a locally revoked digest requires explicit trust restoration.

## Recovery Rules

- Stop on any policy, digest, trust, schema, or mutation error.
- Do not weaken plugin policy or publisher thresholds to make rollback pass.
- Do not copy a lockfile without the matching locally approved trust state.
- Keep support bundles and private run artifacts out of commits.
- Record the accepted binary version, config digest, plugin lock digest, and
  verification result in release or incident evidence.
