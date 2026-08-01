# Plugin Index Publishing

`vigil-plugin-publisher` is the offline companion for plugin index schema `1`.
It generates Ed25519 publisher keys, appends deterministic partial signatures,
and independently verifies a completed threshold-signed index. It never changes
Vigil's local trust store and never uploads an artifact.

## Trust Model

An index signature authenticates an exact JSON payload. It does not grant a
plugin capability, install a plugin, or make a publisher key trusted. A Vigil
user must explicitly trust each public key and approve plugin capabilities.

Vigil has no silently embedded organization trust root. An index must not be
called official until all of these exist:

1. a reviewed publisher-key policy with named custodial roles;
2. a completed key ceremony;
3. independently stored private keys;
4. checked-in public keys and reviewed key IDs;
5. an immutable HTTPS artifact location;
6. a threshold-signed, non-expired index;
7. CI verification using only checked-in public keys;
8. a documented rotation and compromise procedure.

Until those gates are met, examples and test keys are non-production fixtures.

## Recommended Roles

Use at least three independent key custodians with a `2-of-3` signature
threshold. The release preparer may construct the draft but should not control
enough signing keys to satisfy the threshold. Each signer reviews the payload,
artifact digests, expiry, capabilities, and conformance evidence before signing.

Private keys belong in separate offline encrypted storage. Do not place them in
the repository, CI secrets, workflow artifacts, shell history, support bundles,
or shared password fields. Public keys and their SHA-256 key IDs are not secret
and should be reviewed in source control.

## Key Ceremony

On an offline machine, each custodian runs:

```bash
vigil-plugin-publisher keygen \
  --private-key custodian-a.key \
  --public-key custodian-a.pub \
  --json
```

The utility refuses to replace either file. Private files contain one
base64-encoded Ed25519 seed and are created with mode `0600`; loading rejects
symlinks and group/world permissions. Public files contain one base64-encoded
32-byte Ed25519 public key.

Confirm identity independently:

```bash
vigil-plugin-publisher key-id --public-key custodian-a.pub
vigil-plugin-publisher key-id --private-key custodian-a.key
```

The two key IDs must match. Record public keys and IDs through normal code
review. Transfer private keys only to their designated offline custody.

## Draft Preparation

Prepare an index-schema document with `signatures: []`. Every release must bind:

- exact plugin ID and semantic version;
- protocol `1` and host API `v1`;
- metadata digest and aggregate capabilities from a conforming handshake;
- one immutable artifact URL, SHA-256 digest, and byte size per platform;
- an explicit positive signature threshold;
- RFC3339 generation and expiry timestamps.

Run the reference conformance flow against a locally executable artifact before
the draft enters signing review. Non-host artifacts should be produced from the
same reviewed source and release pipeline. Use short expiry periods; 30 days is
the recommended maximum for a routinely refreshed index.

Inspect the unsigned or partially signed document:

```bash
vigil-plugin-publisher inspect --index index-draft.json --json
```

`threshold_filled` only compares signature count to the declared threshold. It
is not cryptographic verification.

## Threshold Signing

Each custodian receives the exact previous output and writes a new file:

```bash
vigil-plugin-publisher sign \
  --index index-draft.json \
  --private-key custodian-a.key \
  --output index-signed-a.json \
  --json

vigil-plugin-publisher sign \
  --index index-signed-a.json \
  --private-key custodian-b.key \
  --output index-final.json \
  --json
```

Outputs are canonical JSON, signatures are sorted by key ID, Ed25519 signing is
deterministic, and existing output files are never replaced. Re-signing an
unchanged payload with the same key is idempotent. A conflicting signature from
the same key ID fails closed.

## Independent Verification

Verify the final document with public keys only:

```bash
vigil-plugin-publisher verify \
  --index index-final.json \
  --public-key custodian-a.pub \
  --public-key custodian-b.pub \
  --public-key custodian-c.pub \
  --json
```

Verification checks schema, expiry, future timestamps, every artifact contract,
signature uniqueness, Ed25519 signatures, and the threshold. CI should run this
command against checked-in public keys before publication.

A user still bootstraps trust explicitly:

```bash
vigil --allow-mutation plugins:trust-publisher \
  --key custodian-a.pub \
  --name "Vigil Publisher A"
```

The user must trust enough independent keys to satisfy the index threshold.

## Publication

Publish the verified index and immutable artifacts together. Relative artifact
URLs must be plain slash-separated paths beneath the index location; they cannot
use traversal, query strings, fragments, colons, percent escapes, or backslashes.
HTTPS artifact URLs must not contain credentials or fragments. Never reuse a
version for different bytes.
Repositories can set `plugins.min_signature_threshold` to reject signed indexes
whose trusted signer threshold is below local policy.
Upload the index only after every referenced artifact is present, then verify
the published HTTPS index with `vigil plugins:index:verify`.
For v1 acceptance, the operational evidence collector must receive the live
index URL, the immutable ceremony record URL, and enough reviewed public keys to
satisfy the threshold:

```bash
go run ./scripts/collect-v1-operational-evidence.go \
  --repo PayCal-Technologies/vigil-public \
  --tag v1.0.0 \
  --plugin-index https://example.com/vigil/plugins/index-v1.json \
  --plugin-ceremony-url https://example.com/vigil/plugin-key-ceremony \
  --publisher-key docs/plugin-publishers/custodian-a.pub \
  --publisher-key docs/plugin-publishers/custodian-b.pub \
  --require-plugin-index-proof \
  --output docs/reviews/v1-operational-evidence-v1.0.0.json
```

## Rotation And Compromise

Planned rotation uses an overlap release signed by enough old and new keys.
Publish new public keys and IDs before requiring them, then retire old keys only
after users have had a documented migration window.

For suspected compromise:

1. stop index publication;
2. identify every index signed by the affected key;
3. instruct users to run `plugins:revoke-publisher` immediately;
4. publish an incident notice with exact key IDs and affected releases;
5. rotate through uncompromised custodians;
6. issue a new short-lived index at a new immutable digest;
7. preserve forensic evidence and complete a post-incident review.

Do not lower the threshold to work around a missing or compromised custodian.
