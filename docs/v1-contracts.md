# v1 Contract Freeze

This document defines Vigil's candidate v1 compatibility baseline. It does not
declare a `v1.0` release. The release may claim v1 only after every criterion in
the [acceptance matrix](v1-acceptance-matrix.md) is verified.

## Command Contract

The embedded command catalogue is frozen by
`cmd/vigil/testdata/v1-command-contract.json`. The fixture covers canonical
names, aliases, access, capabilities, structured flags and arguments, source,
binding, stability, host API, timeout, network behavior, required tools, output
formats, mutation flags, and usage.

- Existing accepted invocations remain accepted through the v1 line.
- New commands and optional flags may be additive.
- Removing or renaming a command, flag, argument, alias, or output format uses
  the published deprecation process.
- Access, capability, binding, timeout, and network changes require explicit
  compatibility and security review; the golden fixture prevents silent drift.
- Prose descriptions may improve without changing the invocation contract.
- Dynamically installed plugin commands are governed by their digest-bound lock
  and protocol contract, not by the embedded catalogue fixture.

## Data Contracts

- Configuration schema `3` is the v1 candidate configuration contract.
  Migration never downgrades a future schema and retains a byte-exact backup
  before replacement.
- Reviewed plan schema `1` is strict. Any incompatible field change requires a
  new plan schema and invalidates old plans rather than reinterpreting them.
- Machine envelope schema `1` keeps all top-level fields present. Existing
  fields are not removed, renamed, or retyped. Additive command-specific data
  fields are allowed.
- JSONL event schema `1` keeps the streaming event envelope stable: sequence,
  type, command, timestamp, and data remain present for every event.
- Private run artifact manifest schema `1` records the run identity, standard
  evidence filenames, and enforced log budgets for `--artifacts` directories.
- Empty collections represented as arrays remain arrays, not `null`, wherever
  the published command contract documents a collection.
- Exit codes `0` through `7` and their statuses are never reassigned.
- Pack schema `1`, plugin protocol and handshake `1`, host API `v1`, and the
  published plugin state, index, publisher, trust, and conformance schemas are
  compatibility surfaces.
- Unknown fields in strict plans, plugin messages, and plugin state fail closed.
  Configuration migration preserves unknown fields where structurally
  possible.

## Change Control

Stable contract removal follows `docs/deprecations.md`: an accepted decision,
stable warning, replacement and rollback guidance, and at least two minor
releases plus 90 days. Security exceptions require an advisory and the safest
available migration.

Every intentional fixture change must include:

1. the compatibility decision or accepted RFC;
2. migration and rollback impact;
3. updated schemas, docs, and tests in one change;
4. a release-note entry identifying affected consumers.

The checked-in acceptance ledger uses
`schemas/vigil-v1-acceptance-ledger-v1.schema.json` and prevents local evidence
from being confused with live distribution, key-custody, external integration,
or independent review evidence. Verified criteria must be backed by named Go
tests, by a checked-in `operational_report` whose own typed report marks the
same operational criterion `verified`, or by a checked-in `external_report`
that validates against `schemas/vigil-external-evidence-v1.schema.json` and
marks the same independent criterion `verified`. Operational reports may only
verify VIGIL-AC-09, VIGIL-AC-11, VIGIL-AC-12, VIGIL-AC-13, and VIGIL-AC-18;
those criteria require `operational_report` evidence when verified. VIGIL-AC-16,
VIGIL-AC-17, VIGIL-AC-19, VIGIL-AC-20, and VIGIL-AC-21 require
`external_report` evidence when verified.
Go-test evidence must name a `*_test.go` file and a top-level
`func TestXxx(t *testing.T)` symbol whose suffix is not lowercase.
The stable release gate binds cited reports to the candidate under evaluation:
external reports must match the current repository HEAD and requested version,
and operational release reports must match the requested version plus the
release commit for release-backed criteria. Workflow-backed operational
reports must also identify a completed successful workflow run before they can
unlock the stable gate.
`scripts/validate-v1-external-evidence.go --json` emits
`schemas/vigil-external-evidence-validation-v1.schema.json` reports so
maintainers can check external evidence before updating the ledger.

`scripts/build-release.sh` evaluates that ledger before doing release work.
Pre-v1 and prerelease versions may build so release candidates can collect
evidence. A stable semantic version with major version 1 or later fails before
artifact generation while any required criterion remains pending.
For automation that needs structured output, the same gate can be run with
`scripts/v1-acceptance-check.go --json`, which emits
`schemas/vigil-v1-acceptance-gate-v1.schema.json` and preserves the same exit
codes. Release candidates carry `v1-acceptance-gate.json` as a top-level asset
and inside every archive so downstream automation can prove what gate state was
evaluated for those bytes. Operational release evidence rejects a downloaded
gate report that is invalid, for a different version, blocked, or inconsistent
with the release version's required gate state.
