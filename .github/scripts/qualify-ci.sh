#!/usr/bin/env bash
set -euo pipefail

sha="${1:?}"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || exit 1
: "${GITHUB_REPOSITORY:?}"

for ((attempt = 0; attempt < 30; attempt++)); do
  runs="$(gh api -X GET "repos/$GITHUB_REPOSITORY/actions/workflows/ci.yaml/runs" -f head_sha="$sha" -f event=push -f per_page=100)"
  id="$(jq -r --arg sha "$sha" '[.workflow_runs[] | select(.head_sha == $sha and .event == "push") | .id][0] // empty' <<< "$runs")"
  if [[ -n "$id" ]]; then
    gh run watch "$id" --repo "$GITHUB_REPOSITORY" --exit-status --compact --interval 30
    exit 0
  fi
  sleep 10
done

echo "No push CI run found for release source $sha" >&2
exit 1
