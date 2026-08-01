# v1 Security Review Brief

Use this brief for VIGIL-AC-19. The review must target an immutable candidate
commit and be performed by a reviewer independent of the implementation.

## Scope

Review:

- the product and threat models, including every `VIGIL-SI-*` invariant;
- CLI mutation activation, reviewed plans, stale-input rejection, and Git
  mutation fingerprints;
- process environment, streaming, bounds, timeout, cancellation, and
  process-group termination;
- config, plan, pack, plugin, artifact, hook, support-bundle, and archive path
  confinement;
- plugin handshake, lock/trust state, capabilities, revocation, signed indexes,
  threshold verification, acquisition, and publisher tooling;
- release reproducibility, workflow pinning, checksums, Sigstore identity,
  attestations, native archive smoke, Homebrew, and macOS signing design;
- upgrade, rollback, deprecation, and incident procedures.

Explicitly assess the documented non-sandbox boundary and whether any UI or
machine output implies stronger isolation than Vigil provides.

## Required Methods

1. Manual source and architecture review.
2. Independent execution of the full quality and release-candidate gates.
3. Adversarial tests for traversal, symlinks, special files, oversized input,
   malformed JSON, digest substitution, stale plans, cancellation races,
   command collisions, capability denial, threshold failure, and artifact
   escape.
4. Review of workflow permissions, immutable action pins, signing identity,
   draft verification, single-transition publication, and downstream
   public-distribution verification order.
5. Verification that no test key, local override, or unsigned downgrade can be
   mistaken for production trust.

Do not use or request production private keys. Review ceremony design and
public records separately from custody.

## Severity

- P0: active compromise or irreversible widespread unsafe mutation with no
  practical mitigation.
- P1: credible contract bypass, supply-chain substitution, trust bypass,
  orphaned execution, or data disclosure affecting supported use.
- P2: bounded security weakness with a practical workaround.
- P3: defense-in-depth, documentation, or low-impact hardening.

## Deliverable

The report must include reviewer independence, exact commit and artifact
digests, methodology, scenarios attempted, findings with reproducible evidence,
limitations, residual risk, and an explicit P0/P1 count. Maintainers record
each disposition without rewriting the original report.

V1 cannot ship with an open P0/P1. A risk acceptance must not relabel severity;
it must explain why the release remains blocked or why the finding is invalid.
