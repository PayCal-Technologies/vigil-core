# Release Channels

Vigil defines three distribution channels with different stability and
retention contracts.

## Stable

Tags of the form `vX.Y.Z` produce a normal GitHub release and become the
repository's latest release. Stable assets pass the complete source, race,
unsigned-candidate reproducibility, macOS signing/notarization, SBOM, checksum,
Sigstore, attestation, downloaded-asset, empty-directory smoke matrix, and
draft-local Homebrew formula validation. The uploaded candidate remains a
draft through native smoke and a local-asset formula install, then is published
once as latest. Public-URL Homebrew audit and tap synchronization remain
stable-only downstream release-workflow evidence.

Stable releases produced after repository immutability was enabled are
immutable. A correction requires a new semantic version; assets and tags are
never replaced in place. The existing `v0.1.0` bootstrap release predates that
repository setting and is not part of the current release-workflow contract.

## Beta

Semantic-version prerelease tags such as `v0.2.0-beta.1` run the same release
pipeline through the native matrix and draft-local Homebrew formula validation,
then publish the same signed artifact and provenance set as stable releases.
GitHub marks them as prereleases only after the draft assets pass that matrix
and candidate packaging test, and they never replace the latest stable release.
Public-URL Homebrew audit and tap publication jobs are skipped.

GitHub release immutability is enabled for the repository. Because published
immutable releases permit only title and note edits, Vigil never relies on
changing a published prerelease into a stable release.

Beta releases may change experimental contracts before the final stable
version. Published schema versions and safety invariants still fail closed and
are not weakened by channel.

## Nightly

The `Nightly` workflow runs daily and on manual dispatch from the selected
commit. It derives an immutable version:

```text
0.0.0-nightly.YYYYMMDD.<12-character-commit>
```

Nightly builds all four supported archives, verifies byte-for-byte
reproducibility, creates an SPDX SBOM, signs checksums with keyless Sigstore,
attests every artifact, smoke-tests the Linux archive, uploads one GitHub
Actions artifact, downloads it again, and re-verifies checksums, signatures,
and attestations.

Nightly artifacts are retained for 14 days. They are not GitHub releases, do
not update a Homebrew formula, do not receive compatibility support, and must
not be used as a durable dependency. Their commit-qualified identity makes
rollback explicit.

## Promotion

Channels are rebuilt from source; an artifact is never copied from nightly to
beta or stable. Promotion means tagging the reviewed commit and rerunning the
appropriate release pipeline. Stable and beta tags must resolve exactly to the
injected commit. Every channel reports version, commit, build date, dirty state,
Go version, target OS, and architecture.

## macOS Gate

The release workflow contains a required Developer ID signing and notarization
job. It imports credentials into an ephemeral keychain, signs with hardened
runtime and secure timestamps, submits each archive root to Apple's notary
service, and Gatekeeper-tests both architectures before and after publication.
The gate remains operationally pending until project credentials are installed
and an immutable public release completes the workflow.
