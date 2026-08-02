# Vigil Product Contract

## North Star

Vigil is a policy-aware repository preflight engine. It lets humans and coding
agents inspect, approve, run, and verify automation before that automation
changes a project.

Vigil succeeds when a user can answer five questions before execution:

1. What will run?
2. Why will it run?
3. What can it access or change?
4. What evidence will it produce?
5. Can the exact reviewed plan be applied without reinterpretation?

## Product Boundary

Vigil owns:

- repository-local policy and command contracts;
- deterministic discovery and precedence;
- preflight planning and approval;
- process execution, cancellation, and result classification;
- mutation detection and policy blocking;
- machine-readable evidence and local run artifacts.

Vigil does not claim:

- that `read` execution is an operating-system sandbox;
- to replace hosted CI, source control, deployment orchestration, or a secrets
  manager;
- that a JSON manifest alone is an executable plugin;
- to safely execute an unknown command whose access or binding is missing.

Vigil is primarily designed to reduce accidental and over-broad automation:
commands that run more than expected, write files they were declared not to
write, or execute after the reviewed repository state has changed. Access and
capability declarations are contracts for review, policy enforcement, and
fail-closed behavior. They are not kernel-level confinement, and an approved
command or plugin still runs with the user's operating-system identity. See
[Safety Model](concepts/safety-model.md) for the contributor-facing
architecture of those boundaries.

## Runtime Vocabulary

- **Built-in module**: implementation compiled into the Vigil binary.
- **Pack**: declarative command metadata and policy that binds to built-in
  modules. Official packs are embedded in release binaries.
- **Plugin**: separately installed executable that communicates with Vigil
  through a versioned subprocess protocol. Plugin identity, metadata,
  capabilities, and commands are bound to repository lock state and local user
  trust before registration.

The compatibility commands `extensions:list` and `extensions:doctor` remain
available during the terminology migration, but current JSON manifests are
packs, not executable plugins.

## Safety Invariants

- **VIGIL-SI-01**: Unknown access, capability, binding, or host compatibility
  fails closed.
- **VIGIL-SI-02**: Mutation requires an explicit reviewed activation path.
- **VIGIL-SI-03**: A read-only declaration is verified against repository
  mutation fingerprints, but is not described as an OS sandbox. Capability
  metadata is used for policy and review, not process confinement.
- **VIGIL-SI-04**: Official, user, and repository packs have deterministic
  precedence: `core < embedded official < user < repository`.
- **VIGIL-SI-05**: Repository and user pack discovery cannot escape its
  declared root through traversal or symlinks.
- **VIGIL-SI-06**: Every executed process is cancellable and bounded by policy.
- **VIGIL-SI-07**: Plugin execution requires an exact digest-bound lock, local
  capability approval, repository policy, compatible handshake, and
  collision-free command registration. Signed acquisition additionally
  requires a threshold-valid trusted publisher set and exact index provenance.
- **VIGIL-SI-08**: Machine output and exit meanings are versioned public API.
- **VIGIL-SI-09**: Support artifacts are local-only by default, redacted,
  atomic, and mode `0600`.
- **VIGIL-SI-10**: A reviewed plan is applied only when its binary,
  configuration, repository state, registry, and pack inputs still match.

## Stability Promise

Before v1.0, Vigil may evolve config and output schemas through explicit
migrations. At v1.0, command names, config schema behavior, JSON envelopes, exit
taxonomy, plan integrity rules, plugin protocol, and deprecation policy become
compatibility commitments.

The candidate freeze and every remaining release blocker are tracked in the
[v1 contracts](v1-contracts.md) and
[v1 acceptance matrix](v1-acceptance-matrix.md).
