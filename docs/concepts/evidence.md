# Acceptance Evidence

Vigil's release gates are backed by a checked-in evidence ledger. The ledger is
not a changelog and not a marketing checklist. It is a machine-validated record
of which safety and acceptance criteria have evidence strong enough to support
a v1 release claim.

## Files And Roles

```text
docs/v1-acceptance.json
        |
        | cites evidence by kind, path, and sometimes Go test symbol
        v
repository evidence
        |
        +-- Go tests, schemas, scripts, and documents
        +-- operational reports for release mechanics
        +-- external reports for independent review
```

The canonical ledger is `docs/v1-acceptance.json`. It targets `v1.0` and must
contain every required `VIGIL-SI-*` safety invariant and `VIGIL-AC-*`
acceptance criterion. A verified criterion must cite evidence; a pending
criterion must keep a blocker.

## Evidence Kinds

The ledger accepts these evidence kinds:

- `go_test`: a named top-level Go test in a `*_test.go` file.
- `document`: checked-in documentation that states or explains a requirement.
- `schema`: a checked-in JSON Schema or contract file.
- `script`: checked-in automation that verifies or produces evidence.
- `workflow`: checked-in workflow automation.
- `operational_report`: typed release-operation evidence.
- `external_report`: typed independent-review evidence.

Evidence paths are repository-relative, regular, non-symlink files. Oversized
evidence and paths that escape the repository fail validation.

## Operational Reports

Operational reports prove release mechanics that cannot be established by local
unit tests alone. They cover criteria such as release workflow behavior,
published asset checks, Homebrew publication, plugin-index publication, and
release-backed acceptance gate evidence.

Operational reports are typed JSON. They record the repository, tag, version,
acceptance ledger path, optional release summary, optional workflow run,
downloaded assets, plugin-index evidence, commands run, and per-criterion
results.

When a stable release candidate is evaluated, candidate-bound operational
reports must match the requested version. Criteria that depend on a completed
workflow also require a completed successful workflow run. Criteria that depend
on a release commit must identify the matching release candidate commit.

## External Reports

External reports prove criteria that require independent review. They bind to a
candidate commit and optionally a candidate version. They record the reviewer,
public report URL, reviewed criteria, and any findings inventory.

The external report validator requires an independent reviewer record and a
public URL. For the finding-inventory criterion, a verified report cannot leave
open P0 or P1 findings.

## Stable Release Gate

Pre-v1 and prerelease builds can proceed while criteria remain pending, because
release candidates are needed to collect operational and external evidence.

A stable semantic version with major version `1` or later is different. The
gate requires every required criterion to be verified. If any criterion remains
`operational_pending` or `external_pending`, release automation fails before
artifact generation.

## Contributor Guidance

When changing a safety or compatibility surface, update evidence in the same
change when possible:

- add or update the Go test that proves the behavior;
- update the relevant schema or contract fixture;
- update the concept or reference documentation;
- update `docs/v1-acceptance.json` only when the evidence actually proves the
  criterion;
- leave operational and external criteria pending until the matching report is
  produced and validates for the candidate.

Do not mark a criterion verified because a related document exists. The cited
evidence must prove the exact criterion under the ledger rules.
