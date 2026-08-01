# Vigil Roadmap

This roadmap turns the world-class audit into release gates. A stage is complete
only when its acceptance criteria are automated where practical.

## Delivery Status

| Stage | Status | Current evidence | Next blocking work |
| --- | --- | --- | --- |
| 0 Product contract | Complete | Product boundary, vocabulary, safety invariants, README, and architecture docs agree. | Keep new feature proposals inside the preflight boundary. |
| 1 Reliability | Complete | Embedded packs, typed registry, schema-3 argv execution, cancellation, policy enforcement, secure bundles, Git-aware hooks, extracted domain packages, bounded per-stream and aggregate private run artifacts, host compatibility checks, startup-only `main.go`, release-binary black-box tests, race, vet, static analysis, and vulnerability gates pass on Go 1.26.5. | Preserve the matrix on every change. |
| 2 Public contract | Complete | Common envelope schema 1, named exit taxonomy, strict reviewed plans, stale-input rejection, JSONL/JUnit/SARIF/GitHub adapters, capability-complete pack contracts, structured registry arguments, rich completions, schemas, and golden fixtures pass the compatibility matrix. | Preserve additive compatibility through v1 review. |
| 3 Extension runtime | In progress | Strict subprocess protocol 1, host API v1, local and HTTPS signed-index acquisition, threshold Ed25519 verification, publisher trust/revocation, repository policy, provenance-bearing lock/trust state, lifecycle commands, sanitized execution, public schemas, hostile-process tests, an external reference-plugin conformance gate, and an offline threshold-publisher utility are implemented. | Complete a real multi-custodian key ceremony, publish the reviewed keys/index endpoint, and integrate that endpoint with release channels. |
| 4 Distribution | In progress | Reproducible unsigned candidates, clean/tag-bound builds, post-build macOS Developer ID signing/notarization, final-asset SBOM/checksum/Sigstore/attestation generation, draft-first four-target downloaded-asset smoke, single-transition immutable publication, a verified nightly workflow, and stable-only candidate/public Homebrew install/audit/test plus tap automation are implemented. | Install production credentials, enable repository release immutability, and complete release and tap runs on real GitHub infrastructure. |
| 5 Scale and adoption | In progress | Workflow DAGs, bounded caches, startup budgets, scale fixtures, exact config rollback evidence, and architecture/security/schema/migration/plugin-authoring/troubleshooting/performance/RFC/deprecation documentation are implemented. | Open and run the first public RFC after publication; collect an external integration report. |
| 6 v1.0 stability | In progress | Candidate command/config/plan/output/exit/plugin contracts, a support policy, and an executable invariant-to-evidence acceptance ledger are checked in. | Native release-target evidence, live distribution/key operations, public RFC/integration evidence, and independent security/usability review. |

Progress is evaluated against exit gates, not feature count. Pulling a later
control forward does not make its stage complete.

## Stage 0: Product Contract

Target: immediately.

- Adopt the policy-aware repository preflight positioning.
- Publish the product boundary and safety invariants.
- Distinguish built-in modules, declarative packs, and subprocess plugins.
- Freeze new top-level commands unless they strengthen the core preflight loop.

Exit gate:

- README, help, architecture, and release language use one product model.
- Each proposed feature names the preflight problem it solves.

## Stage 1: Reliability Release (v0.2)

Target outcome: a downloaded binary behaves predictably without a source
checkout.

- Embed official packs and merge them with user and repository packs using
  deterministic precedence.
- Replace the dispatch switch and duplicated metadata with one typed command
  registry.
- Split build metadata, command registry, pack loading, execution, Git, config,
  output, and wizard behavior out of the CLI entrypoint.
- Add process cancellation, timeouts, signal propagation, and explicit terminal
  states.
- Execute direct argv by default; retain shell execution only through an
  explicit contract.
- Stream human output while preserving structured results and bounded run logs.
- Enforce `allowed_kinds`, `require_private`, disabled IDs, duplicate handling,
  root confinement, and host compatibility.
- Harden support bundles and Git hook discovery.
- Correct shallow or misleading checks and missing-tool behavior.

Exit gate:

- A release binary passes black-box tests from an empty directory and a fresh
  repository.
- No command can execute without a complete registry contract.
- Cancellation reaches a child process in less than 250 ms in integration tests.
- The full test, race, vet, and static analysis suite passes.

## Stage 2: Public Contract Release (v0.3)

Target outcome: humans, agents, and CI can depend on stable execution evidence.

- Ship a versioned JSON envelope with command, status, exit code, timestamps,
  warnings, errors, data, and artifacts.
- Publish the exit taxonomy:
  `0 success`, `1 check failed`, `2 usage/config`, `3 policy blocked`,
  `4 dependency missing`, `5 timeout/cancel`, `6 mutation violation`,
  `7 internal`.
- Add JSONL, JUnit, SARIF, and GitHub annotation adapters where semantically
  appropriate.
- Require access, capabilities, timeout, binding, stability, required tools,
  network behavior, output formats, and host compatibility for every command.
- Add digest-bound `plan_id`, `vigil plan --output`, and
  `vigil apply <plan-file>`.
- Generate rich completions and manuals from the registry.

Exit gate:

- Schemas have golden compatibility fixtures.
- A reviewed plan fails to apply after any relevant config, pack, binary, or
  repository-state digest changes.
- All machine formats are generated from one internal result model.

## Stage 3: Extension Runtime Release (v0.4)

Target outcome: third parties can add behavior without sharing Vigil's process.

- Define a versioned subprocess handshake and capability negotiation.
- Add install, update, remove, list, doctor, and lockfile workflows.
- Resolve executable bindings independently from declarative packs.
- Add trust policy, checksum/signature verification, compatibility ranges,
  revocation, and deterministic precedence.
- Publish an official signed index only after local lifecycle behavior is
  complete.

Implemented local-runtime evidence:

- strict required-field and unknown-field rejection for handshake and response;
- exact executable and metadata digests across lock, trust, discovery, and
  registry binding;
- explicit capability approval, local revocation, and same-version
  immutability;
- sanitized environment, bounded streams, timeout, cancellation, and confined
  artifact verification;
- threshold-signed expiring indexes, HTTPS-only remote acquisition, exact
  platform artifact selection, signed size/digest/metadata/capability binding,
  and publisher revocation;
- repository ID, acquisition, publisher, and capability policy enforced before
  download or handshake and during every discovery;
- stable unavailable-command diagnostics and plugin health in `doctor`,
  `status`, and `verify`.
- policy-aware conformance runs deterministic handshakes and optional isolated
  command execution against a language-neutral external fixture in CI.
- the separate `vigil-plugin-publisher` utility enforces private-key
  permissions, no-clobber key generation, deterministic partial signing, and
  public-key-only threshold verification without mutating client trust.

Exit gate:

- Broken, incompatible, unsigned, shadowed, and unavailable plugins fail closed
  with stable diagnostics.
- Plugin conformance and hostile-process tests run in CI.
- No Go in-process plugin ABI is part of the public contract.

## Stage 4: Distribution Release (v0.5)

Target outcome: installation and provenance are as trustworthy as local
execution.

- Inject version, commit, build date, dirty state, Go version, OS, and
  architecture at build time.
- Enforce tag/version agreement and reproducible metadata with
  `SOURCE_DATE_EPOCH`.
- Build static archives containing license, README, manual, and completions.
- Publish SBOMs, signed checksums, provenance, and GitHub attestations.
- Add macOS signing/notarization and a tested Homebrew tap.
- Define stable, beta, and nightly channels.
- Smoke-test downloaded release assets, not just source builds.

Exit gate:

- Two clean unsigned candidate builds from the same source and epoch are
  byte-identical; final signed macOS bytes carry Apple secure timestamps and
  are bound by checksums, attestations, notarization, and native verification.
- Every published artifact is verified after upload on each supported platform.
- Installation tests exercise meaningful commands from an empty directory.

## Stage 5: Scale and Adoption (v0.6-v0.9)

Target outcome: Vigil handles real repository workflows without losing
predictability.

- Preserve the implemented dependency graphs, explicit parallel groups, retry,
  continue-on-error, cwd, env, artifacts, and required/optional semantics with
  compatibility and race tests.
- Preserve the implemented bounded discovery/registry caches and
  content-transparent invalidation without hidden cache-file writes.
- Preserve CI startup targets: version under 20 ms, help under 50 ms,
  discovery under 100 ms, setup detection under 500 ms.
- Add fixtures for worktrees, submodules, large and dirty repositories, missing
  shells/tools, cancellation, forward schema versions, and broken packs/plugins.
- Publish architecture, security model, schemas, migration guides, plugin
  authoring, troubleshooting, and performance methodology.
- Run a public RFC and deprecation process.

Exit gate:

- Performance budgets and large-repository fixtures are CI gates.
- At least one external integration validates the extension and output
  contracts.
- Upgrade and rollback paths are documented and tested.

## Stage 6: Stability Release (v1.0)

Target outcome: Vigil's safety and automation contracts are durable.

- Freeze the v1 command, config, plan, output, exit, plugin, and deprecation
  contracts.
- Publish support windows and compatibility policy.
- Complete an independent security and usability review.
- Remove or clearly deprecate commands that do not meet the product boundary.

Implemented local contract evidence:

- the embedded command invocation and execution-policy surface has a reviewed
  golden fixture;
- config, plan, output, pack, and plugin schema versions are checked against
  runtime constants;
- every product safety invariant has a stable ID and named automated or
  limitation evidence;
- the machine-readable acceptance ledger rejects missing files, stale test
  symbols, incomplete domains, unsupported statuses, and pending gates without
  concrete blockers;
- release-line, platform-evidence, architecture, and toolchain support windows
  are explicit and do not treat cross-compilation as native support.
- release candidates can collect operational evidence, while stable v1 builds
  fail before artifact generation until all required acceptance criteria are
  verified.
- bounded config, pack, path, argv, atomic-write, plugin-protocol, and
  support-redaction fuzz targets run as a pull-request quality gate.

Exit gate:

- Every safety invariant in `docs/product-contract.md` has an automated test,
  documented limitation, or both.
- Release assets, install paths, empty-directory operation, and representative
  repository workflows pass the v1 acceptance matrix.
- There are no known P0/P1 correctness, safety, or distribution findings.

## Permanent Quality Gates

Every release must pass:

- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- static analysis and vulnerability scanning
- config, manifest, argument, path, and atomic-write fuzz targets
- black-box binary tests
- release-asset verification
- documentation and schema compatibility checks

Roadmap order is intentional. Reliability precedes a frozen machine contract;
the machine contract precedes a plugin ecosystem; the plugin ecosystem precedes
distribution scale; v1.0 follows evidence from all of them.
