#!/usr/bin/env bash
set -euo pipefail

repo="PayCal-Technologies/vigil-public"
tap_repo="PayCal-Technologies/homebrew-tap"
environment="release"
tag="v0.2.0-beta.1"
tag_pattern="v*"
branch="main"
workflow="Vigil"
stable="0"

usage() {
  cat >&2 <<USAGE
usage: $0 [--repo OWNER/REPO] [--tap-repo OWNER/REPO] [--environment NAME] [--tag vX.Y.Z[-pre]] [--tag-pattern PATTERN] [--branch NAME] [--workflow NAME] [--stable]
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    --tap-repo)
      tap_repo="${2:-}"
      shift 2
      ;;
    --environment)
      environment="${2:-}"
      shift 2
      ;;
    --tag)
      tag="${2:-}"
      shift 2
      ;;
    --tag-pattern)
      tag_pattern="${2:-}"
      shift 2
      ;;
    --branch)
      branch="${2:-}"
      shift 2
      ;;
    --workflow)
      workflow="${2:-}"
      shift 2
      ;;
    --stable)
      stable="1"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

for value_name in repo tap_repo environment tag tag_pattern branch workflow; do
  if [[ -z "${!value_name}" ]]; then
    printf 'required value is empty: %s\n' "$value_name" >&2
    exit 2
  fi
done
if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  printf 'invalid --repo: %s\n' "$repo" >&2
  exit 2
fi
if [[ ! "$tap_repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  printf 'invalid --tap-repo: %s\n' "$tap_repo" >&2
  exit 2
fi
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([+-][0-9A-Za-z.-]+)?$ ]]; then
  printf 'invalid --tag: %s\n' "$tag" >&2
  exit 2
fi

missing_tools=()
for tool in gh git jq; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    missing_tools+=("$tool")
  fi
done
if (( ${#missing_tools[@]} > 0 )); then
  printf 'required tool not found: %s\n' "${missing_tools[*]}" >&2
  exit 4
fi

blockers=()
notes=()

record_blocker() {
  blockers+=("$1")
}

record_note() {
  notes+=("$1")
}

if ! gh auth status -h github.com >/dev/null 2>&1; then
  record_blocker "gh is not authenticated for github.com"
fi

if ! git diff --quiet || ! git diff --cached --quiet; then
  record_blocker "local Git worktree has uncommitted changes"
fi
if ! local_head="$(git rev-parse HEAD 2>/dev/null)"; then
  record_blocker "cannot resolve local Git HEAD"
elif ! remote_head="$(git ls-remote --heads origin "$branch" | awk '{print $1}' | head -n 1)"; then
  record_blocker "cannot resolve origin/$branch"
elif [[ -z "$remote_head" ]]; then
  record_blocker "origin/$branch does not exist"
elif [[ "$local_head" != "$remote_head" ]]; then
  record_blocker "local HEAD does not match origin/$branch"
fi

if ! repo_json="$(gh api "repos/$repo" 2>/dev/null)"; then
  record_blocker "repository is not accessible: $repo"
else
  if [[ "$(jq -r '.archived' <<<"$repo_json")" == "true" ]]; then
    record_blocker "repository is archived: $repo"
  fi
fi

if ! gh repo view "$tap_repo" --json nameWithOwner >/dev/null 2>&1; then
  record_blocker "Homebrew tap repository is not accessible: $tap_repo"
fi

if git ls-remote --exit-code --tags "https://github.com/$repo.git" "refs/tags/$tag" >/dev/null 2>&1; then
  record_blocker "release tag already exists on origin: $tag"
fi

if ! run_json="$(gh run list --repo "$repo" --workflow "$workflow" --branch "$branch" --limit 1 --json conclusion,headSha,status,url 2>/dev/null)"; then
  record_blocker "cannot inspect latest $workflow workflow run on $branch"
elif [[ "$(jq 'length' <<<"$run_json")" != "1" ]]; then
  record_blocker "no $workflow workflow run found on $branch"
else
  run_status="$(jq -r '.[0].status' <<<"$run_json")"
  run_conclusion="$(jq -r '.[0].conclusion // ""' <<<"$run_json")"
  run_head="$(jq -r '.[0].headSha' <<<"$run_json")"
  run_url="$(jq -r '.[0].url' <<<"$run_json")"
  if [[ "${remote_head:-}" != "" && "$run_head" != "$remote_head" ]]; then
    record_blocker "latest $workflow run does not match origin/$branch: $run_url"
  elif [[ "$run_status" != "completed" || "$run_conclusion" != "success" ]]; then
    record_blocker "latest $workflow run is not green: $run_status/$run_conclusion $run_url"
  fi
fi

if ! immutable="$(gh api -H 'X-GitHub-Api-Version: 2026-03-10' "repos/$repo/immutable-releases" --jq .enabled 2>/dev/null)"; then
  record_blocker "cannot verify immutable releases for $repo"
elif [[ "$immutable" != "true" ]]; then
  record_blocker "immutable releases are not enabled for $repo"
fi

if ! env_json="$(gh api "repos/$repo/environments/$environment" 2>/dev/null)"; then
  record_blocker "release environment does not exist: $environment"
else
  reviewer_count="$(jq '[.protection_rules[]? | select(.type == "required_reviewers") | .reviewers[]?] | length' <<<"$env_json")"
  if (( reviewer_count < 1 )); then
    record_blocker "release environment has no required reviewer: $environment"
  fi

  protected_branches="$(jq -r '.deployment_branch_policy.protected_branches // false' <<<"$env_json")"
  custom_policies="$(jq -r '.deployment_branch_policy.custom_branch_policies // false' <<<"$env_json")"
  if [[ "$custom_policies" != "true" ]]; then
    record_blocker "release environment does not use custom branch/tag policies"
  elif ! policies_json="$(gh api --paginate "repos/$repo/environments/$environment/deployment-branch-policies" 2>/dev/null)"; then
    record_blocker "cannot read release environment deployment policies"
  elif ! jq -e --arg pattern "$tag_pattern" '.branch_policies[]? | select(.type == "tag" and .name == $pattern)' <<<"$policies_json" >/dev/null; then
    record_blocker "release environment is missing tag deployment policy: $tag_pattern"
  fi
  if [[ "$protected_branches" == "true" ]]; then
    record_note "release environment currently allows protected branches"
  fi
fi

required_secrets=(
  APPLE_DEVELOPER_ID_CERTIFICATE_BASE64
  APPLE_DEVELOPER_ID_CERTIFICATE_PASSWORD
  APPLE_SIGNING_IDENTITY
  APPLE_NOTARY_PRIVATE_KEY_BASE64
  APPLE_NOTARY_KEY_ID
  APPLE_NOTARY_ISSUER_ID
  RELEASE_ADMIN_READ_TOKEN
)
if [[ "$stable" == "1" ]]; then
  required_secrets+=(HOMEBREW_TAP_TOKEN)
fi

if ! secrets_json="$(gh secret list --env "$environment" --repo "$repo" --json name 2>/dev/null)"; then
  record_blocker "cannot list secrets for environment: $environment"
else
  for secret in "${required_secrets[@]}"; do
    if ! jq -e --arg name "$secret" '.[] | select(.name == $name)' <<<"$secrets_json" >/dev/null; then
      record_blocker "missing release environment secret: $secret"
    fi
  done
fi

for note in "${notes[@]}"; do
  printf 'note: %s\n' "$note"
done

if (( ${#blockers[@]} > 0 )); then
  printf 'release readiness blocked for %s (%s):\n' "$repo" "$tag" >&2
  for blocker in "${blockers[@]}"; do
    printf '%s\n' "- $blocker" >&2
  done
  exit 1
fi

printf 'release readiness passed for %s (%s)\n' "$repo" "$tag"
