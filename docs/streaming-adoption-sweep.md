# Streaming Adoption Sweep

This sweep tracks commands that can use `internal/output.StreamReporter` for extension-friendly phase status. Opt-in command phase streams write to stderr so stdout remains available for the command's normal result.

## Adopted

- `setup` / `setup:wizard`: non-interactive setup now streams deterministic selection, config write, and finisher phases. Interactive setup intentionally rejects streaming to avoid interleaving prompts and status events.
- `support:bundle`: bundle collection, Git status collection, bundle build, and output writing now stream phase status. `--dry-run` remains a JSON preview mode and rejects streaming to preserve parseable output.
- `plugins:install`, `plugins:update`, `plugins:remove`, `plugins:trust-publisher`, `plugins:revoke-publisher`, and `plugins:index:verify`: layout, policy, index, acquisition, trust, lockfile, and install/remove phases now stream for plugin managers.
- `config:migrate` and `config:repair`: parsing, default application, migration, write, and post-write validation now stream without changing normal command output.
- `vigil-release-archive`: archive writes now stream phase status for release automation.

## Already Streams

- `workflow:local` already emits JSONL run and gate events. It should keep its current event names for compatibility; later work can adapt its internal emitter to share `StreamReporter` mechanics without renaming events.
- `checks` aggregation already emits JSONL `check_finished` and `run_finished` events. It can use `StreamReporter` for text mode, but JSONL check events should remain stable.

## Should Not Adopt Yet

- `files:iterate` is an item stream, not a phase stream. Its `file` JSONL event contract should stay as-is.
- Short read-only commands such as `version`, `help`, `list`, and simple catalog/status printers do not benefit from phase streaming.
