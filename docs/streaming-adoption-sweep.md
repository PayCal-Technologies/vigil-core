# Streaming Adoption Sweep

This sweep tracks commands that can use `internal/output.StreamReporter` for extension-friendly phase status.

## Should Adopt

- `setup` / `setup:wizard`: multi-step config write, hook install, doctor, and workflow dry-run phases should emit `phase_started`, `phase_finished`, and `phase_failed`.
- `support:bundle`: bundle collection, redaction, manifest writing, and archive output are phase-oriented and useful for support operators.
- `plugins:install`, `plugins:update`, `plugins:remove`, `plugins:trust-publisher`, and `plugins:index:verify`: acquisition, validation, trust, lockfile, and install phases should stream for plugin managers.
- `config:migrate` and `config:repair`: parsing, migration, write, and post-write validation can stream without changing the final JSON envelope.
- `release archive` helper: archive discovery, copy, manifest, and checksum phases should stream for release automation.

## Already Streams

- `workflow:local` already emits JSONL run and gate events. It should keep its current event names for compatibility; later work can adapt its internal emitter to share `StreamReporter` mechanics without renaming events.
- `checks` aggregation already emits JSONL `check_finished` and `run_finished` events. It can use `StreamReporter` for text mode, but JSONL check events should remain stable.

## Should Not Adopt Yet

- `files:iterate` is an item stream, not a phase stream. Its `file` JSONL event contract should stay as-is.
- Short read-only commands such as `version`, `help`, `list`, and simple catalog/status printers do not benefit from phase streaming.
