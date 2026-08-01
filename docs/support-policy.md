# Support Policy

This policy separates artifacts Vigil can build from platforms Vigil has
actually exercised. A cross-compiled archive is not called supported merely
because compilation succeeded.

## Release Line

- The latest stable minor release receives correctness and security fixes.
- The previous stable minor receives critical correctness and security fixes
  for 90 days after its successor is published.
- Beta and nightly channels are evaluation channels and receive no backports.
- A security advisory may require an immediate upgrade when preserving old
  behavior would retain material risk.
- Configuration migration and binary rollback remain documented for every
  supported stable minor.

## Platform Evidence

Vigil produces `CGO_ENABLED=0` archives for:

| Operating system | Architecture | Artifact status |
| --- | --- | --- |
| Linux | amd64 | Release candidate; published-asset smoke required |
| Linux | arm64 | Release candidate; native published-asset smoke required |
| macOS | amd64 | Release candidate; native signed-asset smoke required |
| macOS | arm64 | Release candidate; native signed-asset smoke required |

Source tests are release-blocking on the repository's current Linux and macOS
GitHub-hosted runner matrix. The release workflow configures native
downloaded-asset jobs for all four archive targets. Those targets remain
candidates until the matrix completes against an immutable public release; the
macOS targets additionally require the configured signing, notarization, and
Gatekeeper evidence.

Windows has process-control implementations so source builds can be evaluated,
but no Windows release artifact or v1 support commitment exists. Other
operating systems and architectures are unsupported unless promoted through an
RFC with native CI evidence.

## Toolchain Window

The Go version in `go.mod` is the authoritative build toolchain. Release
workflows must use that exact version. Building with an older toolchain is
unsupported; newer toolchains are accepted only after the full quality and
reproducibility matrix passes.

External tools invoked by configured gates or packs keep their own support
policies. Vigil reports required tools but does not extend their vendor support
windows.

## What Support Means

A supported-platform report must reproduce on an unmodified, vendor-supported
operating system using an official Vigil archive. Maintainers will triage:

- contract, safety, cancellation, and release-provenance regressions as release
  blockers;
- pack adapter defects according to the affected upstream tool;
- behavior outside the documented threat model as an enhancement or security
  hardening request, not as proof of sandbox escape.

The machine-readable status of every platform and installation gate is in the
[v1 acceptance matrix](v1-acceptance-matrix.md).
