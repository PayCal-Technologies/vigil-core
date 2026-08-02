# Mutation Model

Vigil separates a command's declared access from the evidence observed after it
runs.

For the full safety flow, including command registration, reviewed plans,
plugins, and known non-sandbox limits, see [Safety Model](safety-model.md).

## Access

- `read`: no declared repository or external mutation.
- `write`: mutation is the normal operation.
- `conditional-write`: registered flags activate a mutation path.

Write paths require explicit authorization such as `--allow-mutation`.
Read-only overrides such as `--dry-run` are part of the command contract, not
inferred from flag names.

Workflow artifacts are opt-in with `--artifacts` because evidence files are
writes. In-repository artifact roots must be Git-ignored so Vigil's own logs do
not invalidate a read-only gate fingerprint. Each run directory starts with a
private `manifest.json` that records the run ID, standard artifact file names,
and enforced log budgets. Run IDs are generated as single path-safe segments;
artifact construction rejects whitespace, separators, traversal, and unsupported
characters before creating a run directory.

## Repository Verification

Before a read-only workflow gate, Vigil fingerprints:

- Git porcelain status;
- unstaged binary diff;
- staged binary diff;
- names and content digests of untracked, non-ignored files.

It repeats the fingerprint afterward. A difference becomes
`mutation_detected` and exit code `6`, even if the underlying process exited
successfully.

Ready read-only gates may execute concurrently only when they share an explicit
`parallel_group`. Vigil fingerprints once around the whole batch. If the
workspace changes, every gate in that batch receives `mutation_detected`
because attribution to one process would be unsafe. The result diagnostic names
the parallel group and states that individual attribution is unavailable.

Fingerprinting is fail-closed at 100,000 untracked files or 2 GiB of total
untracked content. Non-regular paths returned by Git, including device nodes,
sockets, and named pipes, are rejected without opening them. Exceeding a bound
makes the fingerprint unavailable and blocks a read-only gate rather than
silently omitting state.

Git helper output is bounded at 32 MiB per command. The runner reports
truncation separately from process success, and Vigil converts truncated Git
output into an internal failure. An oversized status or diff therefore cannot
be hashed as if it were complete.

## Boundary

This detects Git-visible workspace changes. It does not prevent or reliably
detect changes to ignored files, files outside the repository, caches,
databases, external services, network state, environment state, or credential
stores. `read_only` is a policy assertion with repository verification, not an
operating-system sandbox.

## Fail-Closed Cases

Execution is blocked when:

- a mutating command lacks confirmation;
- a command contract has unknown access or no implementation binding;
- a read-only gate cannot obtain its before or after fingerprint;
- a required tool is unavailable;
- pack policy or path confinement fails.
