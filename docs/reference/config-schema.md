# Config Schema

The current configuration schema is `3`. Vigil reads JSON from
`vigil.config.json` unless `--config PATH` selects another file.
The published machine schema is
[`schemas/vigil-config-v3.schema.json`](../../schemas/vigil-config-v3.schema.json).
Configuration must be a regular, non-symlink file no larger than 4 MiB.
Validation, setup, migration, repair, pack settings, and command startup use
the same bounded reader.

## Top-Level Fields

| Field | Required | Meaning |
| --- | --- | --- |
| `schema_version` | yes | Must be `"3"` after migration. |
| `profile` | yes | Setup and default-gate profile. |
| `project` | yes | Human-readable project identity. |
| `coordination` | yes | Authority and mutation requirements. |
| `gates` | yes | Ordered workflow gate definitions. |
| `extensions` | yes | Pack discovery and policy settings. |
| `plugins` | no | Executable plugin acquisition, identity, publisher, and capability policy. |
| `public_assumption_patterns` | no | Repository-owned deny-list patterns. |
| `metadata` | no | Repository-owned string metadata. |

## Gate

```json
{
  "name": "go test",
  "command": "go",
  "args": ["test", "./..."],
  "shell": false,
  "read_only": true,
  "tags": ["test", "pre-push"],
  "depends_on": ["generate"],
  "parallel_group": "tests",
  "continue_on_error": true,
  "required": true,
  "cwd": "backend",
  "environment": {"CI": "true"},
  "artifacts": [
    {"path": "reports/junit.xml", "kind": "junit", "media_type": "application/xml"}
  ],
  "timeout": "5m"
}
```

Without `shell: true`, `command` must be exactly one executable and `args`
contains argv. `timeout` is a positive Go duration such as `30s`, `5m`, or
`1h`. Shell gates cannot also declare `args`.

`depends_on` must name unique gates and the complete graph must be acyclic.
Ungrouped gates remain sequential; ready read-only gates sharing a
`parallel_group` may run concurrently. `required` defaults to true and controls
whether a missing executable fails or produces a skipped result.

`continue_on_error` permits independent work after ordinary gate failure while
failed dependents are skipped. Policy, mutation, cancellation, and internal
failures still halt. `cwd` and artifact paths are repository-relative and are
checked again after symlink resolution. Environment values are literal;
`VIGIL_*` names are reserved.

Retries use:

```json
{
  "max_attempts": 3,
  "delay": "2s",
  "on": ["failed", "timed_out"]
}
```

Retries require a read-only gate tagged `network`, are capped at five attempts,
and never apply to policy, cancellation, missing-tool, mutation, or internal
states. See the [workflow graph contract](../concepts/workflow-graph.md).

## Pack Settings

```json
{
  "enabled": true,
  "manifest_root": "extensions",
  "allowed_kinds": ["custom"],
  "enabled_ids": [],
  "disabled_ids": [],
  "require_private": false
}
```

`manifest_root` must be repository-relative and confined to the config
directory. Selection arrays reject empty values, duplicates, and enabled /
disabled conflicts.

## Plugin Policy

```json
{
  "mode": "enabled",
  "local": "deny",
  "require_signed": true,
  "min_signature_threshold": 2,
  "allowed_ids": [],
  "denied_ids": [],
  "allowed_publisher_key_ids": [],
  "denied_capabilities": ["secrets"]
}
```

An absent `plugins` object uses the compatibility default: plugins enabled,
explicit local-file installation allowed, no ID or publisher allowlist, and no
denied capabilities. `require_signed: true` requires `local: "deny"`.
`min_signature_threshold` defaults to `0`; when set above zero, every
signed-index plugin must carry at least that many trusted publisher signatures.

An empty allowlist means unrestricted within the other policy controls.
Publisher allowlists are evaluated against the threshold-valid signer set.
Policy is enforced both during installation and every discovery, so tightening
repository policy immediately removes noncompliant locked commands from the
active registry.

## Migration

`vigil config:migrate` previews migration. Add `--write` plus mutation
authorization to persist it. Migration:

- preserves unknown document fields;
- converts legacy authority policy into `coordination`;
- converts simple legacy command strings into argv;
- explicitly marks shell-dependent command strings;
- refuses to downgrade a future schema.
