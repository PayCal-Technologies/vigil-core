# RFC 0001: Vigil v1 Contract Freeze

- Status: proposed
- Authors: Vigil maintainers
- Opened: pending public discussion
- Decision deadline: at least 14 calendar days after opening
- Discussion: pending public URL

## Summary

Adopt the candidate command, configuration, reviewed-plan, machine-output, exit,
pack, plugin, platform-support, and deprecation contracts in
`docs/v1-contracts.md` as Vigil's v1 baseline. A v1 release remains blocked by
the operational and independent evidence in `docs/v1-acceptance.json`.

## Motivation

Vigil now has the mechanisms expected of a policy-aware repository preflight
engine, but a mechanism is not a compatibility promise. Consumers need to know
which invocations and machine fields remain stable, and maintainers need an
objective way to distinguish local tests from live distribution or independent
review evidence.

Without a freeze, a broad command surface can drift silently. Without an
acceptance ledger, local implementation can be mistaken for a verified
ecosystem, release channel, or supported platform.

## Contract Changes

The proposal makes the following candidate commitments:

- the embedded command contract is represented by
  `cmd/vigil/testdata/v1-command-contract.json`;
- configuration schema `3`, plan schema `1`, output envelope schema `1`, pack
  schema `1`, plugin protocol `1`, and host API `v1` are v1 surfaces;
- exit meanings `0` through `7` are not reassigned;
- stable contract removal follows the published minimum deprecation window;
- support is evidence-tiered, and cross-compilation alone is not support;
- a stable v1 tag is forbidden while an acceptance criterion is pending.

The proposal does not freeze prose, dynamically installed plugin command names,
or experimental contracts. Additive commands, optional flags, and
command-specific data fields remain possible when they do not reinterpret an
existing contract.

## Safety and Threat Model

The ten safety invariants in `docs/product-contract.md` remain normative and
map one-to-one to acceptance evidence. Access or capability metadata continues
to fail closed. Read-only remains a Git-visible mutation assertion, not an
operating-system sandbox. Plugin execution retains exact lock, trust, policy,
digest, handshake, and provenance checks.

A contract freeze must not preserve unsafe behavior. A material security issue
may use the documented deprecation exception, but the advisory must identify
the changed contract and safest migration.

## Design

Contract tests enforce:

- a deterministic golden embedded command catalogue;
- runtime/schema version agreement;
- exact exit taxonomy and output fixtures;
- valid, repository-confined evidence paths;
- real named Go test symbols for verified criteria;
- complete safety-invariant and acceptance-domain coverage;
- concrete blockers for every pending criterion.

The acceptance statuses are `verified`, `operational_pending`, and
`external_pending`. Only committed automated evidence, a checked-in typed
`operational_report`, or a checked-in typed `external_report` backed by
immutable public records can move a criterion to `verified`. Operational
reports are limited to VIGIL-AC-09, VIGIL-AC-11, VIGIL-AC-12, VIGIL-AC-13,
and VIGIL-AC-18.

## Alternatives

### Freeze only the JSON envelope

Rejected. Stable top-level JSON cannot compensate for drifting command access,
arguments, plan integrity, or plugin trust semantics.

### Wait until the v1 tag to create fixtures

Rejected. The fixture must detect accidental drift during the stabilization
period, before a release candidate is tagged.

### Treat every cross-compiled archive as supported

Rejected. Native execution, downloaded-asset verification, and macOS signing
are separate evidence.

### Do nothing

Rejected. Consumers would continue depending on undocumented behavior, and
maintainers would have no objective final release gate.

## Migration and Rollback

Existing schema-3 configurations require no migration. Older configurations use
`vigil config:migrate --json`, preserving an exact backup before write.
Reviewed plans remain binary/config/repository/registry/pack bound and are
regenerated after any upgrade or rollback. Plugin rollback restores the exact
executable, metadata, lock, trust, and publisher state documented in
`docs/upgrading.md`.

Rejecting this RFC leaves Vigil pre-1.0 and permits explicit migrations under
the current compatibility policy; it does not remove existing safety controls.

## Performance

The freeze retains CI medians of:

- `version` under 20 ms;
- `help` under 50 ms;
- discovery under 100 ms;
- setup detection under 500 ms.

The measurement harness uses a release-like `CGO_ENABLED=0` binary, fresh
processes, warmups, medians, and both Linux and macOS CI.

## Testing

Required evidence includes:

- unit, integration, black-box, race, vet, static, and vulnerability checks;
- output and plan golden fixtures;
- mutation, cancellation, workflow graph, plugin hostile-process, and large Git
  fixtures;
- byte-for-byte release rebuilds;
- four-target native downloaded-archive jobs;
- an independent plugin/output integration report;
- independent security and usability reviews.

## Deprecation

This RFC does not immediately deprecate a command. Compatibility aliases such
as `extensions:*`, `settings:show`, and colon-style advanced commands remain
available. Any later removal requires a separate accepted decision, stable
warning code, replacement, migration, rollback, and at least two minor releases
plus 90 calendar days.

## Unresolved Questions

1. Should v1 support both stable macOS architectures before notarization, or
   should unsigned macOS archives remain beta-only?
2. Should Linux arm64 remain a supported target while its hosted runner label
   is in public preview?
3. Which command-specific `data` payloads need dedicated JSON Schemas in
   addition to the common envelope?
4. Which compatibility aliases should begin a post-v1 deprecation window?
5. What immutable public system records the final P0/P1 disposition?

## Decision

Pending public discussion and the minimum review period. Record the decision,
rationale, dissent, accepted amendments, and follow-up issue URLs here.
