#!/usr/bin/env bash
set -eu
computed=0
changed=""
if [ -n "${BASE_REF:-}" ] && git rev-parse --verify --quiet "origin/$BASE_REF" >/dev/null; then
  changed=$(git diff --no-renames --name-only "origin/$BASE_REF...HEAD") && computed=1
elif [ -n "${BEFORE:-}" ] && [ "$BEFORE" != "0000000000000000000000000000000000000000" ] && git cat-file -e "$BEFORE^{commit}" 2>/dev/null; then
  changed=$(git diff --no-renames --name-only "$BEFORE" HEAD) && computed=1
fi
if [ "$computed" -eq 0 ] && git rev-parse --verify --quiet "origin/main" >/dev/null; then
  changed=$(git diff --no-renames --name-only "origin/main...HEAD") && computed=1
  [ -n "$changed" ] || computed=0
fi
if [ "$computed" -eq 0 ]; then
  changed=$(git ls-files)
fi
docs=false
while IFS= read -r file; do
  case "$file" in
    docs/*|README.md|www/*|.github/workflows/publish.yaml) docs=true ;;
  esac
done <<<"$changed"
echo "docs=$docs"
echo "docs=$docs" >> "${GITHUB_OUTPUT:-/dev/null}"
