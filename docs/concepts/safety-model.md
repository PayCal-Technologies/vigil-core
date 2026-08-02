# Safety Model

Vigil is a policy and evidence layer for local automation. It helps a person or
agent see what a command is allowed to do, confirm file-changing work, and catch
unexpected Git-visible changes. It is not an operating-system sandbox.

Use this document when you need the whole safety story in one place. The
narrower concept documents cover the individual mechanisms in more depth:

- [Mutation Model](mutation-model.md) explains access classes and repository
  fingerprinting.
- [Reviewed Plans](plans.md) explains stale-input detection.
- [Workflow Graph](workflow-graph.md) explains gate ordering, parallelism,
  retries, artifacts, and mutation evidence.
- [Packs](packs.md) and [Plugins](plugins.md) explain extension boundaries.
- [Acceptance Evidence](evidence.md) explains how release criteria are proved.

## Safety Flow

```text
argv and global flags
        |
typed command registry
        |
access classification
        |
mutation confirmation
        |
reviewed-plan input checks
        |
workflow gate execution
        |
repository fingerprint comparison
        |
structured output and evidence
```

The command registry is the first safety boundary. Every core, pack, and plugin
command must register a typed command contract before it can execute. Help,
completion, machine output, mutation checks, and plugin wrapping all depend on
that same contract. Unknown access values, missing handlers, duplicate names,
duplicate aliases, unsupported bindings, and invalid output contracts fail
before dispatch.

## Access And Confirmation

Commands declare one of three access classes:

- `read`: no declared repository or external mutation.
- `write`: mutation is the normal operation.
- `conditional-write`: mutation is activated by registered write flags.

Write and conditional-write paths require explicit authorization such as
`--allow-mutation`. Read-only overrides are explicit command contract fields;
Vigil does not infer them from flag names. Unknown access is treated as
mutating.

This is a review contract, not containment. Once a command is approved, the
subprocess still runs with the user's operating-system identity.

## Repository Mutation Detection

Read-only workflow gates are checked against Git-visible workspace evidence.
Before execution, Vigil fingerprints:

- `git status --porcelain`;
- unstaged binary diff;
- staged binary diff;
- untracked, non-ignored file names and content digests.

The same fingerprint is collected afterward. If it changes, the gate result is
reported as `mutation_detected` with exit code `6`, even if the process exited
successfully.

Fingerprinting fails closed. If Vigil cannot find the repository, read Git
state, read a complete bounded diff, or safely account for untracked content,
the read-only gate is blocked rather than treated as clean. That keeps
incomplete evidence from becoming a false pass.

## Reviewed Plans

Reviewed plans bind execution to the inputs that were reviewed. Applying a plan
does not reinterpret the current configuration. Vigil validates the plan,
recomputes the inputs, and blocks execution when any reviewed input changed:

- Vigil executable digest;
- config path and content digest;
- repository root and `HEAD`;
- Git-visible workspace digest;
- active command-registry digest;
- active pack-registry digest.

There is intentionally no stale-plan override. The user must generate and
review a new plan.

## Workflow Gate Rules

Workflow execution keeps mutation attribution conservative:

- Mutating gates run exclusively.
- A batch with any write-capable gate cannot run concurrently.
- A write-capable gate without mutation authorization is policy-blocked.
- Read-only gates in an explicit `parallel_group` may run together.
- Parallel read-only batches fingerprint once around the whole batch.

If a parallel read-only batch mutates the workspace, every gate in the batch is
marked `mutation_detected`. Vigil reports group-level evidence because assigning
the mutation to one process would be guesswork.

Declared artifacts verify files after execution. They do not authorize
mutation. A read-only gate that produces a Git-visible file still fails mutation
detection unless the artifact path is outside the repository or otherwise
outside the Git-visible fingerprint boundary. In-repository plan and artifact
paths are expected to be ignored so Vigil's own evidence does not invalidate a
read-only check.

## Packs And Plugins

Packs and plugins both contribute command metadata, but they are different
safety surfaces.

Packs are declarative manifests. They do not make arbitrary code executable.
They can add command contracts, policy metadata, and built-in bindings, but
core commands cannot be replaced by pack metadata.

Plugins are separately installed executables. A locked plugin command enters
the registry only after repository policy, local trust, digest checks,
publisher rules, bounded handshake, command contract validation, and
command-name collision checks pass. Plugin commands are then wrapped into the
same `internal/cli` command contract as built-in commands, including access,
capabilities, write flags, read-only flags, output formats, timeout, and
network declaration. A blocked locked plugin is reported as unavailable with
its underlying issue instead of silently becoming an unknown command.

Capability approval records what the plugin declares and what the user
approved. It does not sandbox the plugin executable.

## Evidence Outputs

Vigil's safety decisions should be observable in structured output. Workflow
results, JSON envelopes, JSONL events, mutation evidence, run artifacts, plugin
diagnostics, and exit codes are part of the public review surface. Shared output
helpers therefore have high blast radius. Changes to envelope shape, status
labels, truncation flags, artifact fields, or exit classification should be
treated as contract changes.

## High-Blast-Radius Surfaces

Graft analysis of the repository highlights these coupling points:

- `cmd/vigil/app.go`: dispatch, registry resolution, and mutation confirmation.
- `internal/cli/registry.go`: command contract validation and access logic.
- `cmd/vigil/workflow.go`: plan creation, apply, verification, artifacts, and
  output conversion.
- `cmd/vigil/workflow_execution.go`: gate scheduling and mutation enforcement.
- `internal/git/repository.go`: repository fingerprinting.
- `cmd/vigil/plugins_commands.go`: plugin discovery and command wrapping.
- `internal/plugins/errors.go`: plugin error classification and reporting.
- `cmd/vigil/util.go`: shared JSON and terminal output helpers.

Changes to these areas should include contract-oriented tests and should be
reviewed for effects on help, JSON output, exit codes, mutation behavior,
plugin availability, and reviewed-plan compatibility.

## Known Limits

Vigil detects Git-visible workspace changes. It does not prevent or reliably
detect:

- ignored-file changes;
- files outside the repository;
- caches, databases, sockets, and temporary directories;
- network and external-service state;
- environment changes;
- credential-store changes;
- operating-system side effects from approved subprocesses.

The intended guarantee is narrower: commands and plugins declare their access,
mutating work requires explicit approval, reviewed plans bind to stable inputs,
and read-only workflow gates fail when they change the Git-visible workspace.

## Documentation Work Still Worth Doing

The main safety concepts now have a single entry point. These supporting areas
would still benefit from focused contributor-facing writeups:

- plugin conformance internals and how each check maps to install-time safety;
- output envelope stability across JSON, JSONL, SARIF, JUnit, and GitHub
  adapters;
- pack versus plugin decision guidance for third-party authors;
- performance and behavior of mutation fingerprinting on very large dirty
  repositories.
