# GitHub Release Readiness

This checklist covers GitHub repository settings that cannot be proven from the
source tree alone. Complete it before pushing a beta or stable release tag.

## Required Repository Settings

- Actions are enabled for the repository.
- Workflow permissions allow the release workflow to request `id-token: write`
  and create attestations.
- GitHub release immutability is enabled for the repository.
- A reviewer-protected environment named `release` exists.
- The `release` environment is configured so release tags cannot publish
  without maintainer approval.
- The `release` environment allows deployment from semantic-version tags such
  as `v*`.

As of the initial release-readiness pass, immutable releases are enabled and the
`release` environment exists with `cshaiku` as a required reviewer. Maintainers
still need to confirm that the environment permits deployment from release tags
such as `v*`, and add the required secrets.

## Required Release Secrets

Configure these secrets in the `release` environment:

- `APPLE_DEVELOPER_ID_CERTIFICATE_BASE64`
- `APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD`
- `APPLE_SIGNING_IDENTITY`
- `APPLE_NOTARY_PRIVATE_KEY_BASE64`
- `APPLE_NOTARY_KEY_ID`
- `APPLE_NOTARY_ISSUER_ID`
- `RELEASE_ADMIN_READ_TOKEN`

`RELEASE_ADMIN_READ_TOKEN` must be a fine-grained token scoped only to
`PayCal-Technologies/vigil-public` with read-only Administration permission.
The release workflow uses it only to prove immutable releases are enabled.

Stable releases also require:

- `HOMEBREW_TAP_TOKEN`

`HOMEBREW_TAP_TOKEN` must be able to update
`PayCal-Technologies/homebrew-tap`.

## Current Tag Policy

- Use annotated semantic-version tags only.
- Use `vX.Y.Z-beta.N` for public prerelease candidates.
- Do not push `v1.0.0` until `docs/v1-acceptance.json` and all referenced
  external or operational evidence satisfy the stable v1 gate.
- Do not advertise Homebrew installation until a stable public release has
  published and validated the project tap formula.

## Maintainer Verification

Before tagging:

```bash
gh auth status -h github.com
gh api repos/PayCal-Technologies/vigil-public/immutable-releases --jq .enabled
gh api repos/PayCal-Technologies/vigil-public/environments --jq '.environments[].name'
gh secret list --env release --repo PayCal-Technologies/vigil-public
git ls-remote --tags origin
```

Then run the local candidate smoke from `main`:

```bash
RELEASE_TAG=v0.2.0-beta.1 scripts/build-release.sh
RELEASE_TAG=v0.2.0-beta.1 scripts/check-release-reproducibility.sh dist
shasum -a 256 -c dist/SHA256SUMS
```

If those pass and the GitHub settings above are complete:

```bash
git tag -a v0.2.0-beta.1 -m "v0.2.0-beta.1"
git push origin v0.2.0-beta.1
```
