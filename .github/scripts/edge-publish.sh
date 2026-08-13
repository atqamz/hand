#!/usr/bin/env bash
# Replaces the mutable edge release and tag with CANDIDATE, whose assets are
# expected in the working directory. Ordering, rollback and reconciliation are
# argued in docs/adr/edge-is-one-mutable-tag-published-only-by-ci.md.
set -euo pipefail

cat > edge-notes.md <<EOF
This is the rolling development build for maintainers and contributors.

Source commit: ${CANDIDATE}
Channel: edge
CI status: passed

This release and its \`edge\` tag are intentionally mutable and are replaced after newer \`main\` commits pass the complete CI gate.

Not intended as the stable release channel.
EOF

assets=(
  hand-linux-amd64.tar.gz
  hand-linux-arm64.tar.gz
  hand-darwin-amd64.tar.gz
  hand-darwin-arm64.tar.gz
  hand-windows-amd64.zip
  checksums.txt
)
repo=atqamz/hand
all_assets() {
  gh api --paginate "repos/${repo}/releases/${release_id}/assets?per_page=100" "$@"
}
asset_id() {
  all_assets --jq "first(.[] | select(.name == \"$1\") | .id) // empty"
}
rename_asset() {
  gh api --method PATCH "repos/${repo}/releases/assets/$1" -f name="$2" >/dev/null
}
delete_asset() {
  gh api --method DELETE "repos/${repo}/releases/assets/$1" >/dev/null
}

release_tag=edge
bootstrap_tag=""
release_id=$(gh api --paginate "repos/${repo}/releases?per_page=100" \
  --jq 'first(.[] | select(.tag_name == "edge") | .id) // empty')
if [ -z "$release_id" ]; then
  # A draft reserves its tag name without creating the ref, which GitHub
  # writes only on publish, so a temporary tag leaves edge unclaimed
  # until the complete asset set is promoted.
  bootstrap_tag="edge-bootstrap-${CANDIDATE}"
  release_tag=$bootstrap_tag
  stale_drafts=$(gh api --paginate "repos/${repo}/releases?per_page=100" \
    --jq '.[] | select(.tag_name | startswith("edge-bootstrap-")) | .id')
  for stale in $stale_drafts; do
    gh api --method DELETE "repos/${repo}/releases/${stale}" >/dev/null
  done
  gh release create "$bootstrap_tag" --repo "$repo" --target "$CANDIDATE" --title Edge --prerelease --draft --notes-file edge-notes.md
  release_id=$(gh release view "$bootstrap_tag" --repo "$repo" --json databaseId --jq .databaseId)
fi

missing=0
for asset in "${assets[@]}"; do
  existing=$(asset_id "$asset")
  if [ -z "$existing" ]; then
    missing=$((missing + 1))
  fi
done

# A run killed between the backup and promote loops leaves the exact names
# split across two commits, so recovery restores one interrupted candidate's
# whole edge-previous-<commit>-* group rather than filling gaps asset by asset.
if [ "$missing" -gt 0 ]; then
  declare -A backup_present=()
  declare -A backup_groups=()
  backups=$(all_assets --jq '.[] | select(.name | startswith("edge-previous-")) | .name')
  for backup in $backups; do
    backup_present[$backup]=1
    for asset in "${assets[@]}"; do
      if [ "$backup" != "${backup%"-$asset"}" ]; then
        group=${backup#edge-previous-}
        backup_groups[${group%"-$asset"}]=1
      fi
    done
  done
  recovery=""
  for group in "${!backup_groups[@]}"; do
    whole=true
    for asset in "${assets[@]}"; do
      if [ -z "${backup_present[edge-previous-${group}-${asset}]:-}" ]; then
        whole=false
      fi
    done
    if [ "$whole" = true ] && [ -z "$recovery" ]; then
      recovery=$group
    fi
  done
  if [ -z "$recovery" ] && [ -n "$backups" ]; then
    echo "edge assets are incomplete and no single backup group restores them; refusing to publish" >&2
    echo "recover by hand from the edge-previous-* assets on the edge release" >&2
    exit 1
  fi
  if [ -n "$recovery" ]; then
    for asset in "${assets[@]}"; do
      partial_id=$(asset_id "$asset")
      if [ -n "$partial_id" ]; then
        delete_asset "$partial_id"
      fi
    done
    for asset in "${assets[@]}"; do
      restore_id=$(asset_id "edge-previous-${recovery}-${asset}")
      test -n "$restore_id"
      rename_asset "$restore_id" "$asset"
    done
    missing=0
  fi
fi

if [ "$missing" -eq 0 ]; then
  stale_ids=$(all_assets --jq '.[] | select(.name | startswith("edge-staging-") or startswith("edge-previous-")) | .id')
  for stale in $stale_ids; do
    delete_asset "$stale"
  done
fi

staging_assets=()
for asset in "${assets[@]}"; do
  staging="edge-staging-${CANDIDATE}-${asset}"
  existing=$(asset_id "$staging")
  if [ -n "$existing" ]; then
    delete_asset "$existing"
  fi
  cp "$asset" "$staging"
  staging_assets+=("$staging")
done

declare -A backup_of=()
declare -A promoted=()
rollback() {
  status=$?
  if [ "$status" -eq 0 ]; then
    return
  fi
  set +e
  for index in "${!promoted[@]}"; do
    promoted_id=$(asset_id "${assets[$index]}")
    if [ -n "$promoted_id" ]; then
      rename_asset "$promoted_id" "${staging_assets[$index]}"
    fi
  done
  for index in "${!backup_of[@]}"; do
    backup_id=$(asset_id "${backup_of[$index]}")
    if [ -n "$backup_id" ]; then
      rename_asset "$backup_id" "${assets[$index]}"
    fi
  done
  for staging in "${staging_assets[@]}"; do
    staging_id=$(asset_id "$staging")
    if [ -n "$staging_id" ]; then
      delete_asset "$staging_id"
    fi
  done
  if [ -n "$bootstrap_tag" ]; then
    gh api --method DELETE "repos/${repo}/releases/${release_id}" >/dev/null
  fi
  exit "$status"
}
trap rollback EXIT

# Upload under unique names so a failed candidate upload leaves the
# previous exact-name assets available to edge clients.
gh release upload "$release_tag" --repo "$repo" "${staging_assets[@]}"
for staging in "${staging_assets[@]}"; do
  staged_id=$(asset_id "$staging")
  test -n "$staged_id"
done

for index in "${!assets[@]}"; do
  final=${assets[$index]}
  old_asset_id=$(asset_id "$final")
  if [ -z "$old_asset_id" ]; then
    continue
  fi
  backup="edge-previous-${CANDIDATE}-${final}"
  clashing_id=$(asset_id "$backup")
  if [ -n "$clashing_id" ]; then
    delete_asset "$clashing_id"
  fi
  rename_asset "$old_asset_id" "$backup"
  backup_of[$index]=$backup
done
for index in "${!assets[@]}"; do
  staged_asset_id=$(asset_id "${staging_assets[$index]}")
  test -n "$staged_asset_id"
  rename_asset "$staged_asset_id" "${assets[$index]}"
  promoted[$index]=1
done
for index in "${!backup_of[@]}"; do
  backup_asset_id=$(asset_id "${backup_of[$index]}")
  if [ -n "$backup_asset_id" ]; then
    delete_asset "$backup_asset_id"
  fi
done
trap - EXIT

# Publishing before the tag moves keeps edge from naming a commit
# whose complete asset set is not yet available.
gh release edit "$release_tag" --repo "$repo" --tag edge --title Edge --prerelease --draft=false --notes-file edge-notes.md
git tag -f edge "$CANDIDATE"
git push origin refs/tags/edge --force
