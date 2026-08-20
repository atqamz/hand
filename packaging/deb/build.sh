#!/usr/bin/env bash
set -euo pipefail

# Builds a .deb for one linux architecture by cross-compiling the tagged source,
# not by repackaging the prebuilt release archive: that asset already embeds
# -X main.distribution=github, which would make hand update wrongly try to
# replace a .deb-installed binary in place.

tag="${1:?usage: build.sh <tag> <amd64|arm64> [out-dir]}"
goarch="${2:?usage: build.sh <tag> <amd64|arm64> [out-dir]}"
outdir="${3:-.}"
repo="atqamz/hand"
version="${tag#v}"

case "$goarch" in
  amd64|arm64) ;;
  *) echo "unsupported architecture $goarch (want amd64 or arm64)" >&2; exit 1 ;;
esac

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel)
git -C "$repo_root" fetch origin "tag" "$tag" >/dev/null 2>&1 || true
commit=$(git -C "$repo_root" rev-parse "$tag^{commit}")

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

src_dir="$tmp/src"
mkdir -p "$src_dir"
git -C "$repo_root" archive "$tag" | tar -x -C "$src_dir"

pkgroot="$tmp/pkgroot"
mkdir -p "$pkgroot/DEBIAN" "$pkgroot/usr/bin"
(
  cd "$src_dir"
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
    -ldflags "-s -w -X main.version=$version -X main.channel=stable -X main.commit=$commit -X main.distribution=deb" \
    -o "$pkgroot/usr/bin/hand" .
)
chmod 755 "$pkgroot/usr/bin/hand"

cat >"$pkgroot/DEBIAN/control" <<EOF
Package: hand
Version: $version
Architecture: $goarch
Maintainer: Atqa Munzir <atqamz@gmail.com>
Homepage: https://github.com/$repo
Section: utils
Priority: optional
Description: Manage a fleet of coding agents
 Secondhand's hand CLI - one worker per task, its own worktree and herdr pane.
EOF

mkdir -p "$outdir"
out="$outdir/hand_${version}_${goarch}.deb"
dpkg-deb --root-owner-group --build "$pkgroot" "$out"
echo "wrote $out"
