#!/usr/bin/env bash
set -eu
git() { command git -c core.quotePath=false "$@"; }
go=false android=false workflows=false desktop=false render=false iroh=false setup=false e2e_install=false e2e_harness=false docs=false
emit() {
  for line in "go=$go" "android=$android" "workflows=$workflows" "desktop=$desktop" "render=$render" "iroh=$iroh" "setup=$setup" "e2e_install=$e2e_install" "e2e_harness=$e2e_harness" "docs=$docs"; do
    echo "$line"
    echo "$line" >> "${GITHUB_OUTPUT:-/dev/null}"
  done
}
if [ "${EVENT:-}" = "workflow_dispatch" ]; then
  case "${AREA:-all}" in
    all) go=true android=true workflows=true desktop=true render=true iroh=true setup=true e2e_install=true e2e_harness=true ;;
    go) go=true ;;
    android) android=true workflows=true ;;
    desktop) desktop=true ;;
    render) render=true ;;
    iroh) iroh=true ;;
    setup) setup=true ;;
    e2e-install) e2e_install=true ;;
    e2e-harness) e2e_harness=true ;;
    *) echo "unknown area: $AREA" >&2; exit 1 ;;
  esac
  emit
  exit 0
fi
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
while IFS= read -r file; do
  case "$file" in
    android/*) ;;
    *) go=true ;;
  esac
  case "$file" in
    android/* | iroh/* | .github/* | docs/companion-vectors.json) android=true ;;
  esac
  case "$file" in
    .github/workflows/* | .github/actions/*) workflows=true ;;
  esac
  case "$file" in
    internal/desktop/* | internal/desktopasset/* | internal/launcher/* | internal/update/* | internal/board/* | cmd/hand-desktop/* | internal/cli/cli.go | internal/buildstamp/* | .github/desktop/* | .github/scripts/build-desktop.sh | go.mod | go.sum | .github/workflows/check.yaml) desktop=true ;;
  esac
  case "$file" in
    internal/board/* | .github/render/* | .github/scripts/windows-stack-reserve.sh | go.mod | go.sum | .github/workflows/check.yaml) render=true ;;
  esac
  case "$file" in
    iroh/* | internal/board/peer.go | internal/board/iroh_client_test.go | internal/board/companion_host_test.go | internal/sidecar/* | internal/desktop/remote/* | .github/workflows/iroh.yaml | .github/workflows/check.yaml) iroh=true ;;
  esac
  case "$file" in
    packaging/windows/* | .github/windows/* | .github/scripts/build-dist.sh | .github/scripts/build-desktop.sh | internal/launcher/* | go.mod | go.sum | .github/workflows/setup.yaml | .github/workflows/check.yaml) setup=true ;;
  esac
  case "$file" in
    install.sh | install.ps1 | install_test.go | main.go | go.mod | go.sum | internal/* | .github/e2e/* | .github/scripts/* | .github/workflows/e2e-install.yaml | .github/workflows/publish.yaml | .github/workflows/check.yaml) e2e_install=true ;;
  esac
  case "$file" in
    internal/harness/* | internal/luvus/* | internal/cli/attempt*.go | internal/cli/supervisor*.go | internal/e2e/* | .github/e2e/versions.env | .github/e2e/install-*.sh | .github/workflows/e2e-harness.yaml | .github/workflows/check.yaml) e2e_harness=true ;;
  esac
  case "$file" in
    docs/* | README.md | www/* | .github/workflows/publish.yaml) docs=true ;;
  esac
done <<<"$changed"
if [ "${EVENT:-}" = "push" ]; then
  desktop=true render=false setup=false e2e_install=false e2e_harness=false
fi
emit
