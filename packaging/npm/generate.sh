#!/usr/bin/env bash
# Cross-compiles hand from the tagged source for each platform and packages
# the results for npm: five platform packages plus one meta package, versioned
# to match the tag. Building from source (rather than downloading hand's
# prebuilt release archives, which already embed -X main.distribution=github)
# is what lets each binary correctly self-identify as an npm install.
set -euo pipefail

REPO="atqamz/hand"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel)"
PLATFORMS_DIR="$SCRIPT_DIR/platforms"
META_DIR="$SCRIPT_DIR/meta"

usage() {
  cat <<'EOF'
Usage: generate.sh [tag]

Cross-compiles hand's tagged source for <tag> (e.g. v0.5.0) into each of
packaging/npm/platforms/<platform>/bin/, and stamps that tag's version
(without the leading "v") into all five platform package.json files and the
meta package.json, including its optionalDependencies pins.

With no tag, defaults to the latest GitHub release. Requires `go` on PATH.
Re-running is safe: builds and rewrites are idempotent.
EOF
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
esac

if [[ $# -gt 1 ]]; then
  echo "generate.sh: too many arguments" >&2
  usage >&2
  exit 1
fi

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  TAG="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
fi
VERSION="${TAG#v}"

git -C "$REPO_ROOT" fetch origin "tag" "$TAG" >/dev/null 2>&1 || true
COMMIT="$(git -C "$REPO_ROOT" rev-parse "$TAG^{commit}")"
LDFLAGS="-s -w -X main.version=$VERSION -X main.channel=stable -X main.commit=$COMMIT -X main.distribution=npm"

PLATFORM_ORDER=(linux-x64 linux-arm64 darwin-x64 darwin-arm64 win32-x64)
declare -A GOOS=(
  [linux-x64]="linux" [linux-arm64]="linux"
  [darwin-x64]="darwin" [darwin-arm64]="darwin"
  [win32-x64]="windows"
)
declare -A GOARCH=(
  [linux-x64]="amd64" [linux-arm64]="arm64"
  [darwin-x64]="amd64" [darwin-arm64]="arm64"
  [win32-x64]="amd64"
)
declare -A BINNAME=(
  [linux-x64]="hand" [linux-arm64]="hand"
  [darwin-x64]="hand" [darwin-arm64]="hand"
  [win32-x64]="hand.exe"
)

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

src_dir="$TMP_DIR/src"
mkdir -p "$src_dir"
git -C "$REPO_ROOT" archive "$TAG" | tar -x -C "$src_dir"

for platform in "${PLATFORM_ORDER[@]}"; do
  binname="${BINNAME[$platform]}"
  dest_dir="$PLATFORMS_DIR/$platform/bin"
  mkdir -p "$dest_dir"
  (
    cd "$src_dir"
    CGO_ENABLED=0 GOOS="${GOOS[$platform]}" GOARCH="${GOARCH[$platform]}" \
      go build -ldflags "$LDFLAGS" -o "$dest_dir/$binname" .
  )
  if [[ "$platform" != win32-* ]]; then
    chmod +x "$dest_dir/$binname"
  fi
done

set_version() {
  local pkg="$1"
  local tmp
  tmp="$(mktemp "$pkg.XXXXXX")"
  jq --arg v "$VERSION" '.version = $v' "$pkg" > "$tmp"
  mv "$tmp" "$pkg"
}

for platform in "${PLATFORM_ORDER[@]}"; do
  set_version "$PLATFORMS_DIR/$platform/package.json"
done

meta_pkg="$META_DIR/package.json"
tmp_meta="$(mktemp "$meta_pkg.XXXXXX")"
jq --arg v "$VERSION" \
  '.version = $v | .optionalDependencies |= with_entries(.value = $v)' \
  "$meta_pkg" > "$tmp_meta"
mv "$tmp_meta" "$meta_pkg"

echo "generate.sh: packaged hand $VERSION for ${#PLATFORM_ORDER[@]} platforms"
