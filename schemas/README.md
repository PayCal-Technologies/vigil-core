# Vigil Schemas

These files define the compatibility surfaces shipped by Vigil:

- `vigil-config-v3.schema.json`: repository configuration;
- `vigil-external-evidence-v1.schema.json`: independent integration, RFC,
  security, usability, and final severity evidence reports for v1 acceptance;
- `vigil-external-evidence-validation-v1.schema.json`: machine-readable
  validation results emitted by `scripts/validate-v1-external-evidence.go
  --json`;
- `vigil-output-v1.schema.json`: the common `--json` envelope;
- `vigil-jsonl-event-v1.schema.json`: the common `--format=jsonl` event
  envelope;
- `vigil-operational-evidence-v1.schema.json`: live release, workflow, tap,
  and plugin-index evidence reports for v1 acceptance;
- `vigil-v1-acceptance-gate-v1.schema.json`: machine-readable acceptance gate
  reports emitted by `scripts/v1-acceptance-check.go --json`;
- `vigil-v1-acceptance-ledger-v1.schema.json`: the checked-in v1 acceptance
  ledger shape, statuses, evidence kinds, and blocker rules;
- `vigil-pack-v1.schema.json`: declarative pack and command contracts;
- `vigil-plugin-conformance-v1.schema.json`: executable protocol conformance
  reports;
- `vigil-plugin-protocol-v1.schema.json`: executable plugin handshake plus
  request and response definitions;
- `vigil-plugin-lock-v1.schema.json`: repository-pinned plugin identities,
  metadata, capabilities, commands, and digests;
- `vigil-plugin-index-v1.schema.json`: threshold-signed, expiring plugin
  releases and platform artifacts;
- `vigil-plugin-publishers-v1.schema.json`: locally trusted and revoked
  Ed25519 publisher keys;
- `vigil-plugin-trust-v1.schema.json`: local capability approvals and digest
  revocations;
- `vigil-plan-v1.schema.json`: digest-bound reviewed plans;
- `vigil-run-artifact-manifest-v1.schema.json`: private `--artifacts`
  run-directory manifest.

Schema files are release artifacts and compatibility commitments. Existing
fields are not removed or retyped within a schema version. Additive fields
require a compatibility review; incompatible changes require a new schema
version and migration documentation.
