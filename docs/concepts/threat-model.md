# Threat Model

## Protected Assets

Vigil primarily protects the integrity of the repository workspace, reviewed
automation intent, configuration, hooks, local diagnostic artifacts, and
release provenance.

## Trust Boundaries

- command-line arguments and configuration are untrusted input;
- repository and user pack manifests are untrusted metadata;
- plugin candidates, protocol messages, repository locks, and local trust files
  are untrusted until validated;
- configured gate processes are trusted only to the extent explicitly approved;
- approved plugin executables remain separate processes running with the user's
  operating-system identity;
- official pack metadata is part of the compiled release;
- Git and external tools are separate processes;
- GitHub Actions and Sigstore are external release trust services.

## Addressed Threats

Vigil is designed to reduce:

- accidental execution of mutating automation;
- accidental and over-broad automation whose real behavior exceeds the reviewed
  command contract;
- shell interpretation when argv execution is sufficient;
- hanging or orphaned child processes;
- repository mutation by a gate declared read-only;
- hidden pack overrides, duplicates, or policy violations;
- plugin substitution, metadata drift, local revocation bypass, command
  collision, unbounded execution, and repository artifact escape;
- manifest-root traversal and symlink escape;
- hook replacement without preservation;
- support-bundle path, diagnostic secret-pattern, and configuration disclosure
  by default;
- release binaries whose version or command surface differs from the tag.

## Out Of Scope

Vigil does not currently claim protection against:

- malicious code with the user's operating-system privileges;
- network, database, cloud, or external-service mutation;
- writes outside the Git-visible repository;
- compromised compilers, runners, package registries, or GitHub accounts;
- kernel-level confinement or secret extraction by an approved executable;
- undeclared plugin behavior that bypasses its capability declaration through
  direct operating-system access;
- publisher identity for explicitly allowed local plugin files;
- compromise of every locally trusted publisher key needed to satisfy an index
  threshold;
- an organization-wide official trust root until a reviewed multi-custodian key
  ceremony, public-key registry, and live index endpoint are operational.

## Security Invariants

Unknown executable behavior fails closed. Mutation needs an explicit activation
path. Release assets are checksummed, signed or attested, and tested after
publication. Support bundles never upload automatically. Plugins use a
versioned subprocess protocol, exact executable and metadata digests, local
trust and revocation records, capability approval, bounded output, timeout, and
cancellation. Capability approval is policy metadata and a local trust decision,
not process confinement. Signed acquisition adds expiring threshold-signed
Ed25519 indexes, HTTPS downgrade prevention, exact platform size/digest
verification, and publisher revocation. These controls do not claim
operating-system sandboxing.
Publisher-side private keys are handled only by the separate offline utility,
which rejects symlinks and permissive key modes, never overwrites outputs, and
supports independent public-key verification. Key custody and endpoint
operations remain human and organizational trust boundaries.
