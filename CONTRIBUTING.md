# Contributing to Vigil

Vigil is an open-source CLI under the 0BSD license. Contributions are welcome
when they keep the tool local-first, explicit about mutations, and useful for
both humans and automation.

## Development

```bash
go test ./...
go build -o bin/vigil ./cmd/vigil
./bin/vigil verify --json
```

Commands that write files or hooks should require explicit confirmation through
`--allow-mutation`, `--write`, or another documented write flag.

## Pull Requests

- Keep changes focused.
- Add or update tests for command behavior, JSON contracts, setup flow, and
  mutation boundaries.
- Preserve deterministic JSON output for script-friendly commands.
- Do not introduce private deployment assumptions into this public repository.

## Feedback and Ideas

Use GitHub issues for bugs, feature requests, and packaging feedback. Security
reports should follow `SECURITY.md`.

Changes to product scope, safety invariants, stable contracts, plugin trust,
mutation policy, or supported distribution commitments follow the
[RFC process](docs/rfcs/README.md) and
[deprecation policy](docs/deprecations.md).
