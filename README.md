# Vigil Core

Vigil is a local-first release, configuration, and agent-safety tool for teams
that want repository automation to explain itself before it mutates anything.

This public repository contains the source-available core:

- versioned JSON configuration;
- guided config creation and validation;
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
vigil extensions:doctor --json
```

## Config

Vigil uses JSON for configuration. A repo-local `vigil.config.json` is the
default, and `--config PATH` can point Vigil at an explicit file.

```bash
vigil config:schema
vigil config:init --profile=go-tool --write
vigil --config ./candidate.vigil.config.json config:validate --json
```

The current schema version is `1`.

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
