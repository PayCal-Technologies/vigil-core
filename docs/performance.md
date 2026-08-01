# Performance Methodology

Vigil treats startup latency and bounded discovery as release contracts.

## Budgets

The quality matrix enforces median wall-clock budgets on both macOS and Linux:

| Operation | Budget |
| --- | ---: |
| `vigil version` | 20 ms |
| `vigil help` | 50 ms |
| `vigil list --json` | 100 ms |
| `vigil setup --dry-run --json` | 500 ms |

Run the same check locally:

```bash
scripts/check-performance.sh
```

The checker builds a release-like binary, creates an empty directory with empty
user pack and plugin roots, performs three warmups, then reports the median and
95th percentile from 21 new-process samples. The median is the enforced budget;
the p95 is retained as diagnostic evidence. Each process has a five-second hard
limit.

Use `--samples N` for a longer diagnostic run. Sample counts must be between 5
and 101.

## Cache Contract

Caches are process-local and memory-only. Read commands do not create hidden
cache files.

- Pack reports use a 32-entry LRU keyed by settings, roots, directory topology,
  and the SHA-256 digest of every relevant manifest.
- Plugin discovery uses a 64-entry LRU keyed by executable path, verified
  executable SHA-256, locked metadata SHA-256, and protocol version.
- Command registries use a 16-entry LRU keyed by the complete visible command
  contract and resolution context.

Every cache returns copies where its values contain mutable slices or maps.
Policy, trust, revocation, publisher threshold, executable mode, and executable
digest checks still run before a cached plugin handshake can be used.

Pack layers are limited to 1,024 entries and each manifest to 1 MiB. Plugin
protocol and executable bounds are documented in the
[plugin model](concepts/plugins.md).

## Interpreting Regressions

Re-run on an otherwise idle machine before changing a budget. If the regression
persists:

1. compare `version` with `list --json` to separate process startup from
   discovery;
2. run with empty `VIGIL_USER_PACK_ROOT` and `VIGIL_PLUGIN_ROOT`;
3. inspect pack count and plugin handshake time;
4. add a focused benchmark or fixture before optimizing;
5. change a published budget only through compatibility review.

Large-repository fixtures are measured separately because Git status and
content hashing scale with repository state rather than process startup. The
test matrix includes a 2,000-file dirty repository, a real linked worktree, a
dirty submodule, an oversized sparse untracked file, and a non-blocking special
file rejection.
