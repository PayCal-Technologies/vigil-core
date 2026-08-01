# Plugin Authoring

Vigil plugins are standalone executables. Any language that can read and write
strict JSON over standard streams can implement protocol `1`.

Start from the external
[POSIX reference plugin](../examples/plugins/reference/vigil-plugin-reference)
and its [README](../examples/plugins/reference/README.md). The fixture has no Go
package dependency and is exercised against release binaries.

## Executable Identity

Name the executable `vigil-plugin-<id>`, where the ID is lowercase kebab-case.
The file must be regular, non-symlink, executable, non-empty, no larger than
256 MiB, and not group- or world-writable.

Installation locks:

- plugin ID and semantic version;
- executable SHA-256;
- metadata SHA-256;
- exact commands and aggregate capabilities;
- acquisition provenance and signed publisher threshold.

Changing bytes without changing the semantic version is rejected.

## Handshake

Vigil invokes:

```text
vigil-plugin-example handshake --protocol-version=1
```

The process receives a sanitized environment, no standard input, a three-second
timeout, and at most 1 MiB of accepted standard output. Standard output must be
one JSON object and nothing else. Diagnostics belong on standard error.

The object uses
[`vigil-plugin-protocol-v1.schema.json`](../schemas/vigil-plugin-protocol-v1.schema.json)
and declares schema `1`, protocol `1`, host API `v1`, plugin identity, and at
least one namespaced command such as `example:scan`.

Every command declares:

- aliases, summary, usage, examples, flags, and positional arguments;
- `read`, `write`, or `conditional-write` access;
- complete capabilities;
- stability, timeout, network behavior, and required tools;
- output formats plus write/read-only flags where applicable.

Unknown or omitted fields fail strict decoding. Keep command and capability
arrays deterministic across repeated handshakes.

## Execution

Vigil invokes:

```text
vigil-plugin-example execute \
  --protocol-version=1 \
  --command=example:scan
```

The plugin reads one request object from standard input and writes one response
object to standard output. Echo the exact request ID. The request includes
repository root, selected config, output format, arguments, and mutation
authorization.

The response contains an exit code from Vigil's public taxonomy, bounded human
output, JSON data, diagnostics, and artifacts. Artifact paths must resolve to
regular non-symlink files inside the repository, and must be normalized
repository-relative strings without leading or trailing whitespace, absolute
paths, or traversal segments. Vigil rejects request-ID
mismatches, trailing JSON, malformed diagnostics, path escapes, unknown fields,
and output above 8 MiB.

Do not print progress to standard output. Use standard error, and keep it free
of secrets.

## Capabilities

Declare the union needed by every command:

```text
filesystem:read
filesystem:write
git:read
git:write
network
process
environment
secrets
interactive
```

Access and capability declarations must agree. A read command cannot declare a
write capability. Network behavior must agree with the `network` capability.
Repository policy may deny IDs, publishers, local acquisition, capabilities, or
signed indexes whose publisher signature threshold is below the configured
minimum, even after local approval.

Capability approval is not a sandbox. A plugin executable is trusted code
running as the user, isolated by a process boundary and sanitized environment.

## Conformance

Run handshake-only conformance first:

```bash
vigil plugins:conformance \
  --file ./vigil-plugin-example \
  --json
```

Then execute every declared command in a disposable repository:

```bash
vigil --allow-mutation plugins:conformance \
  --file ./vigil-plugin-example \
  --execute \
  --timeout 30s \
  --json
```

Conformance checks acquisition policy before process launch, repeated
determinism, metadata policy, request/response shape, artifacts, and executable
digest stability. Include its schema-1 report in publisher review evidence.

## Publishing

Produce one immutable artifact per supported OS/architecture. Follow
[Plugin Index Publishing](plugin-publishing.md) for index authoring, offline
threshold signatures, independent verification, key rotation, and incident
response. Do not claim official distribution until the reviewed keys, immutable
artifacts, and live HTTPS index are all published.
