# Plugins

Vigil plugins are separately installed executables that contribute namespaced
commands through subprocess protocol `1`. They do not run inside the Vigil
process and they do not replace declarative packs.

For the full safety flow, including command access, mutation confirmation, and
known non-sandbox limits, see [Safety Model](safety-model.md).

## Lifecycle

Local-file installation remains available when repository policy permits it:

```bash
vigil --allow-mutation plugins:install \
  --file ./vigil-plugin-example \
  --approve filesystem:read \
  --approve process

vigil plugins:list --json
vigil plugins:doctor --json

vigil --allow-mutation plugins:update \
  --file ./vigil-plugin-example \
  --approve-all

vigil --allow-mutation plugins:remove example
```

Signed-index installation adds publisher verification and exact release
selection:

```bash
vigil --allow-mutation plugins:trust-publisher \
  --key ./publisher.pub \
  --name "Example Publisher"

vigil plugins:index:verify --index ./index-v1.json --json

vigil --allow-mutation plugins:install \
  --index ./index-v1.json \
  --id example \
  --version 1.2.3 \
  --approve filesystem:read

vigil plugins:publishers --json
vigil --allow-mutation plugins:revoke-publisher sha256:<key-digest>
```

Publisher key files contain one base64-encoded 32-byte Ed25519 public key.
Indexes may be local files or HTTPS URLs. Signed artifact URLs may be HTTPS or
relative to the verified index. An exact ID and semantic version are mandatory;
there is no implicit "latest" selection.

Installation verifies policy before downloading or executing. It then runs a
bounded handshake before writing state. The user must approve every aggregate
capability, either individually with repeatable `--approve` flags or explicitly
with `--approve-all`. Publisher trust never implies capability approval.
Capability approval is a policy and review contract. It records what the
plugin says it needs and what the user approved, but it does not sandbox the
plugin process.

Removal revokes the exact executable digest by default. `--keep-trust` removes
the installed executable and repository lock without adding a revocation.
Reinstalling a revoked digest requires the explicit `--restore-trust` path.

## Conformance

Plugin authors can validate an executable without installing or trusting it:

```bash
vigil plugins:conformance \
  --file ./vigil-plugin-example \
  --json
```

The default checks enforce executable constraints, run two bounded handshakes,
require stable executable and metadata digests, validate every command
contract, and apply repository plugin policy. Full protocol execution is an
explicit second level:

```bash
vigil --allow-mutation plugins:conformance \
  --file ./vigil-plugin-example \
  --execute \
  --timeout 10s \
  --json
```

Execution invokes every declared command with no arguments, mutation disabled,
and a deterministic request ID inside a disposable repository. Any exit code
from a structurally valid response is accepted; the conformance result measures
wire compatibility, not command business logic. Candidate digest drift,
malformed responses, timeouts, artifact escapes, and request-ID mismatches fail
the report.

Conformance is not installation and creates no lock or trust record. It still
executes an external process, so `--execute` requires mutation confirmation and
local candidates must be permitted by repository policy. The
[reference shell plugin](../../examples/plugins/reference/README.md) consumes
only the public subprocess contract and runs in CI on Linux and macOS.

## State And Binding

Each repository records its selected plugins in:

```text
vigil.plugins.lock.json
```

The lock pins plugin ID, semantic version, executable digest, metadata digest,
protocol and host API versions, capabilities, command names, acquisition type,
signed index digest, publisher signer IDs, signature threshold, and the exact
binding:

```text
plugin:<id>@<version>#sha256:<digest>
```

Local approvals and revocations are stored outside the repository in the
platform user configuration directory under `vigil/plugins/trust-v1.json`.
Trusted and revoked publisher keys use `vigil/plugins/publishers-v1.json`.
Repository policy can also set `min_signature_threshold`; signed-index plugins
whose locked threshold falls below that value are removed from the active
registry during discovery.
`VIGIL_PLUGIN_ROOT` overrides that user root for controlled environments and
tests.

Repository lock state is shareable. User trust state is local and is written
with mode `0600`. A lock alone never authorizes execution.

## Discovery

For each locked plugin, Vigil verifies:

1. strict lock and trust documents;
2. absence of local digest revocation;
3. an exact trust record for ID, version, executable digest, metadata digest,
   capabilities, and acquisition provenance;
4. current repository plugin policy;
5. the locked publisher threshold for signed-index acquisitions;
6. the installed path, file type, permissions, and executable digest;
7. a bounded protocol handshake;
8. exact handshake agreement with the lock and trust records;
9. namespaced commands with no collision against core, pack, or earlier plugin
   commands.

Only plugins that pass every check enter the typed command registry. A command
from an unavailable locked plugin returns the underlying stable policy,
dependency, interruption, usage, or internal exit class rather than appearing
as an unknown command. `doctor`, `status`, and `verify` include plugin health.

## Protocol

Vigil invokes:

```text
<plugin> handshake --protocol-version=1
<plugin> execute --protocol-version=1 --command=<namespaced-command>
```

The handshake is strict JSON on standard output. Execution receives one strict
JSON request on standard input and must return one strict JSON response on
standard output. Unknown fields, missing required fields, extra JSON values,
unsupported versions, malformed command contracts, oversized output, and
request-ID mismatches fail closed.

The host supplies repository root, resolved config path, requested output
format, mutation authorization, command arguments, and a request ID. Responses
use Vigil's exit taxonomy and may include diagnostics and repository-relative
artifacts. Reported artifacts must exist as regular non-symlink files inside
the repository. Artifact paths must be normalized repository-relative strings;
leading or trailing whitespace, absolute paths, and traversal segments are
rejected. Declared SHA-256 digests are verified.

The public wire contract is
[`vigil-plugin-protocol-v1.schema.json`](../../schemas/vigil-plugin-protocol-v1.schema.json).
Conformance output uses the
[`vigil-plugin-conformance-v1.schema.json`](../../schemas/vigil-plugin-conformance-v1.schema.json)
report inside Vigil's common output envelope.
Repository and local state use the
[`lock`](../../schemas/vigil-plugin-lock-v1.schema.json) and
[`trust`](../../schemas/vigil-plugin-trust-v1.schema.json) schemas. Signed
distribution uses the
[`index`](../../schemas/vigil-plugin-index-v1.schema.json) and
[`publisher store`](../../schemas/vigil-plugin-publishers-v1.schema.json)
schemas.
Offline key custody and threshold signing are covered by the
[plugin publishing guide](../plugin-publishing.md).

## Repository Policy

Configuration schema `3` can disable plugins, allow or deny local acquisition,
require signed indexes, select plugin IDs, restrict publisher key IDs, and deny
capabilities. Policy is enforced on install and on every discovery. Tightening
policy therefore removes a noncompliant locked command from the active registry
without rewriting the lock.

## Execution Controls

- Handshakes time out after 3 seconds.
- Commands use their declared timeout, capped at 30 minutes.
- Cancellation propagates through Vigil's process runner.
- Child environments are cleared and rebuilt with only a minimal `PATH`,
  temporary directory, C locale, an empty `HOME`, and protocol version.
- Executable, handshake, and response sizes are bounded.
- Same-version executable digests are immutable.
- Candidate bytes are rechecked after handshake and installed atomically.
- Signed indexes are strict, threshold-signed, expiring documents.
- Remote index and artifact acquisition requires HTTPS, rejects credentials and
  downgrade redirects, and bounds redirects, time, and bytes.
- Signed artifact size and digest are verified before handshake.
- Signed metadata digest and capabilities are verified again after handshake.
- Publisher revocation blocks installed signed plugins whose remaining trusted
  signers fall below their locked threshold.

## Security Boundary

Capability approval is a trust decision and command contract, not an
operating-system sandbox. Once approved, a malicious executable still runs
with the user's OS identity and may attempt operations beyond its declaration.
The more common risk Vigil is designed to reduce is accidental and over-broad
automation: a plugin command that runs too much, changes files unexpectedly, or
no longer matches the reviewed lock and policy state. Vigil limits inherited
environment data, enforces its mutation activation boundary, verifies identity
and response integrity, and bounds process life, but it cannot confine
arbitrary system calls.

Vigil supports local and HTTPS signed indexes, but no organization-wide
"official" publisher key is silently trusted. A publisher key becomes a trust
anchor only through explicit local mutation authorization. Local-file
installation has no publisher identity; repositories that require publisher
authentication must set `plugins.local` to `deny` and `require_signed` to
`true`.
