# v1 Acceptance Matrix

The canonical ledger is
[`docs/v1-acceptance.json`](v1-acceptance.json), with published schema
`schemas/vigil-v1-acceptance-ledger-v1.schema.json`. Contract tests validate
its schema, evidence paths, named Go tests, typed operational and external
reports, required domains, and one-to-one coverage of every product safety
invariant.

Status meanings:

- `verified`: checked-in automated evidence passes locally and in CI.
- `operational_pending`: implementation exists, but live release, custody, or
  distribution evidence is still required.
- `external_pending`: evidence must come from an independent user, integrator,
  or reviewer.

## Current Gate

| IDs | Area | Status |
| --- | --- | --- |
| VIGIL-SI-01 through VIGIL-SI-10 | Product safety invariants | verified |
| VIGIL-AC-01 through VIGIL-AC-08 | Candidate v1 contracts, JSON/JSONL output, and documented support window | verified |
| VIGIL-AC-09 | Native validation of every claimed release target | operational_pending |
| VIGIL-AC-10 | Reproducible unsigned release candidates | verified |
| VIGIL-AC-11 through VIGIL-AC-13 | Published assets, Homebrew, macOS signing/notarization | operational_pending |
| VIGIL-AC-14 | Empty-directory binary operation | verified |
| VIGIL-AC-15 | Representative repository workflow | verified |
| VIGIL-AC-16 through VIGIL-AC-17 | Independent integration and public RFC evidence | external_pending |
| VIGIL-AC-18 | Production publisher ceremony and signed index | operational_pending |
| VIGIL-AC-19 through VIGIL-AC-21 | Independent security/usability review and P0/P1 disposition | external_pending |
| VIGIL-AC-22 | Stable v1 release fail-closed gate | verified |

Vigil must not report Stage 6 complete or publish a stable `v1.0` tag while any
criterion remains pending. A criterion moves to `verified` only when its
evidence is committed or points to an immutable public release record.
Committed Go-test evidence must name a `*_test.go` file and a valid
`func TestXxx(t *testing.T)` symbol whose suffix is not lowercase.
`scripts/v1-acceptance-check.go --json` emits
`schemas/vigil-v1-acceptance-gate-v1.schema.json` reports so CI and release
automation can inspect the gate result without parsing human log text. Release
candidates publish that report as `v1-acceptance-gate.json` and also embed it
inside each archive.
For VIGIL-AC-09, VIGIL-AC-11, VIGIL-AC-12, VIGIL-AC-13, and VIGIL-AC-18, the
ledger evidence kind is `operational_report` and the referenced report must
validate against `schemas/vigil-operational-evidence-v1.schema.json` and
include that exact criterion with status `verified`. Operational reports cannot
verify AC22 or independent-review criteria, and local tests cannot substitute
for the report when those criteria are marked `verified`.
For independent criteria, the ledger evidence kind is `external_report` and the
referenced report must validate against
`schemas/vigil-external-evidence-v1.schema.json`, identify an independent
reviewer, point at an immutable public report URL, and include that exact
criterion with status `verified`. Local tests cannot substitute for the report
when those criteria are marked `verified`. AC21 additionally requires an explicit
findings inventory and rejects any open P0 or P1 finding in the typed report.
Run `scripts/validate-v1-external-evidence.go --json` before referencing an
external report from this ledger; its result follows
`schemas/vigil-external-evidence-validation-v1.schema.json`.
