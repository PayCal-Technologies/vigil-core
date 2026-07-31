# Vigil Core

Vigil is a local-first release, configuration, and agent-safety tool for teams
that want repository automation to explain itself before it mutates anything.

This public repository contains the source-available core:

- versioned JSON configuration;
- guided config creation and validation;
- local `doctor`, `status`, `plan`, `verify`, and support-bundle diagnostics;
- config-driven local workflow gates;
- git hook shims for pre-commit and pre-push;
- generic workspace hygiene, staged-secret, dependency inventory, and command
  catalog checks;
- extension manifest discovery and diagnostics;
- a small CLI surface that can be embedded into deployment-specific workflows.

Extension implementations can be added by deployments that need project-specific
commands or policy surfaces.

## Install

```bash
go install github.com/PayCal-Technologies/vigil-core/cmd/vigil@latest
```

Or build locally:

```bash
go build -o bin/vigil ./cmd/vigil
```

## Quick Start

```bash
vigil config:init --write
vigil config:validate
vigil config:repair
vigil doctor
vigil workflow:local --dry-run
vigil workflow:local
vigil extensions:doctor --json
```

## CI/CD Use

Vigil Core runs local CI/CD from `vigil.config.json`. Each gate is a named shell
command with metadata that tells humans and automation whether it is read-only.

```json
{
  "name": "go test",
  "command": "go test ./...",
  "read_only": true,
  "tags": ["test"]
}
```

Common commands:

```bash
vigil status --json
vigil plan
vigil workflow:local --dry-run
vigil workflow:local --json
vigil verify --json
vigil support:bundle --dry-run
```

Git hooks:

```bash
vigil hooks:install
```

This installs `pre-commit` and `pre-push` hook shims that run the configured
Vigil workflow.

Built-in public checks:

```bash
vigil checks:staged-sensitive
vigil checks:workspace-hygiene
vigil checks:command-catalog --json
vigil checks:public-assumptions --json
vigil deps:inventory --json
```

## Config

Vigil uses JSON for configuration. A repo-local `vigil.config.json` is the
default. Vigil searches upward from the current directory for that file, and
`--config PATH` can point Vigil at an explicit file.

```bash
vigil config:schema
vigil config:init --profile=go-tool --write
vigil --config ./candidate.vigil.config.json config:validate --json
vigil config:repair
vigil config:repair --yes
```

The current schema version is `1`.

`config:validate --json` returns machine-readable `structured_issues` and a
repair hint. `config:repair` uses classic command-line stdin prompts and shows
`[default: value]` for answers that can be accepted by pressing enter.
`--yes` applies the default repair without prompting.

`public_assumption_patterns` is the config-owned deny-list used by
`checks:public-assumptions`. Keep project-specific terms in config rather than
hardcoding them into Vigil.

Terminal output uses `[OK]`, `[FAIL]`, and `[WARN]` status labels. Labels are
colorized on interactive terminals and stay plain when `NO_COLOR` or `CI` is
set.

## Extensions

Extensions are described by `extensions/<id>/extension.json`. The public core
validates extension manifests and reports which extensions are available.

```json
{
  "schema_version": "1",
  "id": "example",
  "name": "Example Extension",
  "kind": "custom",
  "status": "local",
  "private": false,
  "public_core": true,
  "description": "Example extension.",
  "source_root": "extensions/example",
  "packages": [],
  "commands": []
}
```

Use:

```bash
vigil extensions:list
vigil extensions:doctor --json
```

## Golden Extension Example

The public repository includes `extensions/file-iterator/` as the canonical
example extension. It models a read-only CLI command, `files:iterate`, that would
walk matching files and emit structured output.

```bash
vigil extensions:list
vigil files:iterate --root=. --glob='**/*.go' --jsonl
```

## Ethos

Vigil treats automation as an accountable system. It should make authority,
configuration, risk, and mutation boundaries explicit enough for both a human
maintainer and an AI agent to understand what is safe, what is missing, and what
needs confirmation.
