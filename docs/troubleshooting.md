# Troubleshooting

Start with read-only diagnostics:

```bash
vigil version
vigil status --json
vigil doctor --json
vigil config:validate --json
vigil extensions:doctor --json
vigil plugins:doctor --json
```

## Config Is Rejected

Run `vigil config:migrate --json` to preview. A future schema is blocked and is
never downgraded. For current or older schemas, inspect structured issue codes
before authorizing `config:migrate --write` or `config:repair`.

Migration and repair create exact timestamped `.bak-*` files. Follow
[Upgrade and Rollback](upgrading.md); do not edit the backup in place.

## Reviewed Plan Is Stale

`apply` compares the binary, config, repository root and `HEAD`, Git-visible
workspace, command registry, and pack digests. Any mismatch intentionally
invalidates the plan. Restore the reviewed inputs or generate and review a new
plan; never patch `plan_id`.

## Read-Only Gate Reports Mutation

Inspect `manifest.json`, the gate's stdout/stderr logs, and optional mutation
diff under the private run directory. Parallel batches mark every peer because
attribution is unsafe. Generated JUnit/SARIF files must be intentionally ignored
or the gate must be classified as mutating.

Remember that ignored files, user caches, network calls, databases, and external
services are outside the Git-visible fingerprint.

## Fingerprint Is Unavailable

Confirm the command is inside a valid Git worktree and that Git status/diff
commands succeed. Fingerprinting blocks above 100,000 untracked files or 2 GiB
of untracked content and rejects non-regular paths returned by Git. Clean or
ignore generated content after review; do not weaken the gate to bypass a
limit.

## Tool or Shell Is Missing

A required executable returns exit code `4`. Install the declared tool or mark
the gate `required: false` only when skipping it is an accepted repository
policy. Optional status applies only to a missing executable; command failures
still fail.

Shell gates explicitly require Bash. Prefer argv gates where shell syntax is
not needed.

## Pack Is Missing or Broken

Run `extensions:list --json` and `extensions:doctor --json`. Inspect layer
origin, override evidence, host API, command contract completeness, allowed
kinds, enabled/disabled IDs, and repository confinement. A layer is limited to
1,024 entries and a manifest to 1 MiB.

Released binaries always contain the official embedded packs. Empty-directory
output should therefore still list those packs.

## Plugin Is Blocked

Use `plugins:list`, `plugins:doctor`, and `plugins:publishers`. Check the exact
digest, metadata digest, local capability approval, revocation state,
repository policy, publisher set, and signature threshold.

Do not delete trust/revocation state or lower policy to force execution. For a
new build, use the explicit update path. For an old build, follow exact
[plugin rollback](upgrading.md#plugin-rollback).

## Startup Is Slow

Run `scripts/check-performance.sh` with empty user roots. Compare `version` with
`list --json` to isolate process startup from discovery. See
[Performance Methodology](performance.md). Caches are memory-only, so there is
no cache directory to delete.

## Support Evidence

Preview before writing:

```bash
vigil support:bundle --dry-run
vigil --allow-mutation support:bundle
```

Bundles are local, redacted, and private by default. Diagnostic text redacts
absolute paths and common secret patterns, but an explicitly included full
config is not content-scanned. Review every file before sharing. Security
reports follow [SECURITY.md](../SECURITY.md).
