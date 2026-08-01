# Machine Output

Vigil JSON output uses envelope schema `1`. The published schema is
[`schemas/vigil-output-v1.schema.json`](../../schemas/vigil-output-v1.schema.json).
JSONL streams use event schema `1`, published as
[`schemas/vigil-jsonl-event-v1.schema.json`](../../schemas/vigil-jsonl-event-v1.schema.json).

```json
{
  "schema_version": "1",
  "command": "verify",
  "status": "ok",
  "exit_code": 0,
  "started_at": "2026-07-31T18:00:00Z",
  "finished_at": "2026-07-31T18:00:00.125Z",
  "duration_ms": 125,
  "warnings": [],
  "errors": [],
  "data": {},
  "artifacts": []
}
```

All fields are present. `warnings`, `errors`, and `artifacts` are arrays rather
than `null`. Command-specific values live under `data`. `duration_ms` is the
millisecond delta between `started_at` and `finished_at`; validation rejects
payloads where those fields disagree.

## Status And Exit

| Exit | Status |
| ---: | --- |
| 0 | `ok` |
| 1 | `failed` |
| 2 | `invalid` |
| 3 | `blocked` |
| 4 | `dependency_missing` |
| 5 | `interrupted` |
| 6 | `mutation_violation` |
| 7 | `internal_error` |

Programs should branch on `exit_code` and use `status` for display. Diagnostic
codes beginning with `VIGIL_E_` and `VIGIL_W_` are stable identifiers; message
text may improve without a schema change. The JSON schema enforces the
one-to-one `exit_code` to `status` mapping above.

## Formats

`--json` is the compatibility alias for `--format=json`. Commands advertise
their supported formats in `vigil list --json`.

- `json`: one final versioned envelope.
- `jsonl`: ordered versioned events and one final envelope event.
- `junit`: test-suite XML for workflows and aggregate checks.
- `sarif`: SARIF 2.1.0 for file-oriented findings.
- `github`: escaped GitHub Actions workflow annotations.

Examples:

```bash
vigil workflow:local --format=jsonl
vigil verify --format=junit
vigil checks:staged-sensitive --format=sarif
vigil checks:workspace-hygiene --format=github
```

JSONL consumers must order events by `sequence`, not timestamps. Every JSONL
event has `schema_version`, positive `sequence`, `type`, `command`, `timestamp`,
and `data`. A final `run_finished`, `check_finished`, or `iteration_finished`
event contains the same envelope model used by JSON output.

## Compatibility

Envelope schema `1` fields are not removed, renamed, or retyped. Incompatible
changes require a new schema version. Golden success and failure fixtures are
checked in under `internal/output/testdata`.
