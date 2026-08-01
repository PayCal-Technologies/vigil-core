# Workflow Graph

Vigil schema 3 gates form a validated directed acyclic graph. Reviewed plans
copy the complete graph and bind it into the plan digest.

## Ordering

- `depends_on` names prerequisite gates.
- Declaration order is the deterministic tie-breaker for ready gates.
- Gates without `parallel_group` run one at a time, preserving the behavior of
  existing configurations.
- Ready, read-only gates with the same non-empty `parallel_group` may run
  together. `--jobs` bounds the batch from 1 through 32 and defaults to 4.
- Mutating gates cannot declare `parallel_group` and always run exclusively.
- `--tag` selects matching gates plus their transitive dependencies.

Results are returned in declaration order even when completion order differs.
JSONL start and finish events are emitted in deterministic batch order.

## Failure

The default remains fail-fast. `continue_on_error: true` allows independent
gates to continue after an ordinary failure, missing required tool, or timeout.
Dependents of a failed gate are reported as `skipped`; unrelated ready gates
can still run. Policy blocks, cancellation, internal failures, and mutation
violations always halt scheduling.
If Vigil detects an impossible scheduler transition, it returns an internal
failure instead of panicking.

`required` defaults to `true`. Setting it to `false` changes only missing-tool
behavior: an unavailable executable becomes a successful `skipped` result so
dependents may proceed. It does not turn command failures into successes.

## Retry

Retries are intentionally narrow:

- the gate must be read-only and carry the explicit `network` tag;
- `max_attempts` is between 2 and 5;
- `on` may contain `failed` and `timed_out`;
- `delay` is at most five minutes;
- blocked, cancelled, missing-tool, mutation, and internal states are never
  retried.

The gate timeout applies to each attempt. The reported duration includes retry
delay. Private stdout and stderr logs retain up to 64 MiB per stream and append
`[truncated]` when the bound is reached. A run shares a 512 MiB log budget and
fails closed if another stream cannot reserve its marker; result summaries
expose truncation flags rather than implying that bounded evidence is complete.

## Execution Context

`cwd` is relative to the repository root. Vigil resolves symlinks before
execution and blocks a directory that escapes the repository.

In-repository plan and run-artifact paths must be Git-ignored. Vigil resolves
their nearest existing ancestor, rejects symlink redirection outside the
repository, and never writes inside Git metadata. Explicit paths that are
already outside the repository remain supported.

`environment` contains literal repository-owned values. Variable names use the
portable `[A-Za-z_][A-Za-z0-9_]*` form. Repository configuration cannot
override `VIGIL_*`; Vigil supplies:

```text
VIGIL_GATE_NAME
VIGIL_PLAN_ID
VIGIL_REPOSITORY_ROOT
VIGIL_RUN_ID
```

The process otherwise inherits the invoking environment. Secrets should not be
stored in repository configuration.

## Declared Artifacts

Each artifact declaration has a repository-confined path relative to the gate
`cwd`, an optional kind and media type, and `required` semantics that default to
true. After execution Vigil requires each mandatory artifact to be a regular,
non-symlink file beneath the repository, computes its SHA-256 digest, and adds
it to the common output envelope. Missing optional artifacts become warnings.

Artifact declarations verify outputs; they do not authorize mutation. A
read-only gate that changes Git-visible files still fails mutation detection.

## Parallel Mutation Evidence

Vigil fingerprints the Git-visible workspace once before and once after an
explicit parallel batch. If the fingerprint changes, every gate in that batch
is marked `mutation_detected` because Vigil cannot safely attribute the change
to one process. The diagnostic reports that a command in the named parallel
group changed the fingerprint and that individual attribution is unavailable.
This is fail-closed and preserves exit code `6`.

`read_only` remains repository verification, not an operating-system sandbox.
Ignored files, caches, network state, and external services are outside the
fingerprint boundary.
