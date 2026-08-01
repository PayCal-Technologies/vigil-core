# GitHub Release Readiness

This checklist covers GitHub repository settings that cannot be proven from the
source tree alone. Complete it before pushing a beta or stable release tag.
The active `v0.2.0-beta.1` release tracker is
https://github.com/PayCal-Technologies/vigil-public/issues/2.

## Verified Repository State

Last verified: 2026-08-01.

- Current public release: `v0.1.0`.
- Current remote tags: `v0.1.0`.
- Latest `main` quality workflow: passing at
  `5b23c784285dd5bc9f5f0c37abb30f5b059a29e0`.
- Actions setting: enabled, with all actions allowed.
- Default workflow token permission: read-only. The release workflow requests
  its required `contents`, `id-token`, and `attestations` permissions
  explicitly.
- GitHub release immutability setting: enabled for future releases.
- Existing `v0.1.0` release immutability: false.
- `release` environment: exists.
- Required reviewer on `release`: `cshaiku`.
- Current `release` deployment policy: protected branches only.
- `release` environment secrets: none configured.
- Homebrew tap repository: `PayCal-Technologies/homebrew-tap` exists.
- Homebrew tap formula: `Formula/vigil.rb` has not been published.

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

As of the latest release-readiness pass, immutable releases are enabled and the
`release` environment exists with `cshaiku` as a required reviewer. The
environment still needs a release-tag deployment policy such as `v*`, and the
required secrets still need to be added.

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
gh api -H 'X-GitHub-Api-Version: 2026-03-10' \
  repos/PayCal-Technologies/vigil-public/immutable-releases --jq .enabled
gh api repos/PayCal-Technologies/vigil-public/actions/permissions \
  --jq '{enabled, allowed_actions}'
gh api repos/PayCal-Technologies/vigil-public/actions/permissions/workflow \
  --jq '{default_workflow_permissions, can_approve_pull_request_reviews}'
gh api repos/PayCal-Technologies/vigil-public/environments/release \
  --jq '{name, protection_rules, deployment_branch_policy}'
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
