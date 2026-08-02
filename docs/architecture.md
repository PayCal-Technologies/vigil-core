# Architecture

Vigil is organized around a small policy and execution kernel. The command
binary should parse input, resolve a registered command, and delegate domain
work; it should not be the owner of persistence, process, or pack semantics.

## Current Components

```text
cmd/vigil/
  main.go                 process startup and signal wiring
  app.go                  invocation parsing and command dispatch
  registry.go             authoritative command registration
  workflow.go             workflow orchestration
  setup.go                setup and wizard presentation
  *_commands.go           domain-specific CLI adapters

cmd/vigil-plugin-publisher/
  main.go                 offline publisher key and signed-index operations

internal/atomicfile/      durable mode-preserving writes
internal/buildinfo/       injected and runtime build metadata
internal/cache/           bounded process-local LRU primitives
internal/cli/             command contracts and registry validation
internal/config/          schema, defaults, migration, and validation
internal/completion/      registry-driven Bash, Zsh, and Fish completions
internal/contracts/       public schema compatibility gates
internal/git/             repository discovery and mutation evidence
internal/hooks/           Git hook plans, atomic apply, and restoration
internal/output/          deterministic JSON and terminal formatting
internal/packs/           layered manifest discovery and policy
internal/plan/            canonical reviewed plans and stale-input detection
internal/plugins/         subprocess protocol, policy, signed acquisition,
                          publisher trust, lock, and lifecycle
internal/releasearchive/  deterministic release archives
internal/runner/          argv/shell execution, timeout, and cancellation
internal/runartifact/     private workflow plans, results, and stream logs
internal/support/         support-bundle construction and redaction
internal/workflow/        deterministic DAG selection and bounded scheduling
extensions/               embedded official pack manifests
```

The CLI files retain presentation and orchestration code while reusable
behavior lives in `internal/`. Further extraction is driven by ownership and
testability, not file length alone: setup state, policy planning, result
formats, and checks move behind package boundaries as their contracts stabilize.

## Invocation Flow

```text
argv and global flags
        |
typed command registry
        |
access and mutation policy
        |
handler
        |
config / packs / plugins / Git / runner
        |
text or structured result
```

The registry is the authoritative source for command identity, aliases, access,
capabilities, binding, stability, timeout, required tools, network behavior,
and output formats. Help, command listing, completions, and manpages derive
from it.

The safety model is documented separately in
[Safety Model](concepts/safety-model.md). That document is the contributor
entry point for command access classification, mutation confirmation,
reviewed-plan drift detection, workflow fingerprinting, plugin wrapping, and
known non-sandbox boundaries.

## Dependency Rules

- `internal/*` packages do not import `cmd/vigil`.
- Pack metadata does not make arbitrary code executable.
- Plugin executables enter the registry only after lock, digest, trust,
  handshake, capability, compatibility, and command-collision checks.
- Filesystem mutation uses atomic-write or explicit transactional APIs.
- Process creation goes through `internal/runner` for timeout and cancellation.
- A command with an unknown access value, binding, or handler is not registered.
- User and repository inputs are confined before files are opened.

## Target State

Before v1.0, `cmd/vigil/main.go` should contain only process startup and
signal wiring; that boundary is now in place. Policy, plan integrity, output
envelopes, Git state, wizard state, checks, and plugin protocol code belong
under `internal/`.
