# v1 Independent Evidence

These briefs make the remaining independent v1 gates reproducible. Reviewers
must identify the exact commit or release candidate they assessed and disclose
any employment, authorship, or financial relationship that could affect
independence.

- [External integration report](external-integration-report.md)
- [Security review brief](security-review-brief.md)
- [Usability review brief](usability-review-brief.md)

Maintainers may help reproduce an environment but must not write the reviewer's
findings or mark their own work independent. Raw reports should be retained at
an immutable public URL and summarized in `docs/v1-acceptance.json`.
When an independent criterion is ready to close, commit a typed
`external_report` that validates against
`schemas/vigil-external-evidence-v1.schema.json`, then reference it from
`docs/v1-acceptance.json`. The brief Markdown files are not themselves
verifying evidence. A report that verifies VIGIL-AC-21 must include the
`findings` array explicitly, even when it is empty. Required reviewer, criterion,
finding, and validation error text fields must contain non-whitespace content.
Validate the typed report before changing the ledger:

```bash
go run ./scripts/validate-v1-external-evidence.go \
  --report docs/reviews/v1-external-evidence-integration.json \
  --criterion VIGIL-AC-16 \
  --json
```

The JSON result follows
`schemas/vigil-external-evidence-validation-v1.schema.json`.

Minimal verified external report shape:

```json
{
  "schema_version": "1",
  "target": "v1.0",
  "generated_at": "2026-08-01T12:00:00Z",
  "candidate_commit": "0123456789abcdef0123456789abcdef01234567",
  "candidate_version": "1.0.0-rc.1",
  "public_url": "https://example.com/immutable-vigil-review",
  "reviewer": {
    "name": "Independent Reviewer",
    "organization": "External Lab",
    "relationship": "No employment, authorship, or financial relationship with the implementation.",
    "independent": true
  },
  "criteria": [
    {
      "id": "VIGIL-AC-16",
      "status": "verified",
      "detail": "The external integration consumed plugin protocol 1 and output schema 1 without Go code.",
      "evidence": [
        "https://example.com/immutable-vigil-review/integration"
      ]
    }
  ],
  "findings": []
}
```

When the report is cited from the acceptance ledger, the stable v1 gate also
requires `candidate_commit` to match the repository HEAD being released and
`candidate_version` to match the requested release version.

Operational release evidence is collected separately:

```bash
go run ./scripts/collect-v1-operational-evidence.go \
  --repo PayCal-Technologies/vigil-public \
  --tag v0.4.0 \
  --tap-repo PayCal-Technologies/homebrew-tap \
  --workflow-run-id "$GITHUB_RUN_ID" \
  --require-release-proof \
  --output docs/reviews/v1-operational-evidence-v0.4.0.json
```

That report can support VIGIL-AC-09 and VIGIL-AC-11 through VIGIL-AC-13. Add
`--plugin-index`, `--plugin-ceremony-url`, repeated `--publisher-key`, and
`--require-plugin-index-proof` for VIGIL-AC-18 only after the production
publisher ceremony.
Operational reports cannot verify independent-review criteria or AC22; those
must be proven by external evidence reports or by the stable v1 acceptance gate.
Verified operational report `evidence` entries are public HTTPS records; the
typed report fields carry release commits, checksums, index digests, and key
metadata.
