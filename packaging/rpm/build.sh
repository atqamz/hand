#!/usr/bin/env bash
set -euo pipefail

# Builds an .rpm for one linux architecture by cross-compiling the tagged source,
# not by repackaging the prebuilt release archive: that asset already embeds
# -X main.distribution=github, which would make hand update wrongly try to
# replace an .rpm-installed binary in place. The spec's %install step just
# installs whatever SOURCES/hand this script places there.

tag="${1:?usage: build.sh <tag> <amd64|arm64> [out-dir]}"
goarch="${2:?usage: build.sh <tag> <amd64|arm64> [out-dir]}"
outdir="${3:-.}"
version="${tag#v}"

case "$goarch" in
  amd64) rpmarch=x86_64 ;;
  arm64) rpmarch=aarch64 ;;
  *) echo "unsupported architecture $goarch (want amd64 or arm64)" >&2; exit 1 ;;
esac

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel)
git -C "$repo_root" fetch origin "tag" "$tag" >/dev/null 2>&1 || true
commit=$(git -C "$repo_root" rev-parse "$tag^{commit}")

topdir=$(mktemp -d)
trap 'rm -rf "$topdir"' EXIT
mkdir -p "$topdir"/{BUILD,RPMS,SOURCES,SPECS,SRPMS,BUILDROOT}

src_dir="$topdir/src"
mkdir -p "$src_dir"
git -C "$repo_root" archive "$tag" | tar -x -C "$src_dir"

(
  cd "$src_dir"
  CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build \
    -ldflags "-s -w -X main.version=$version -X main.channel=stable -X main.commit=$commit -X main.distribution=rpm" \
    -o "$topdir/SOURCES/hand" .
)

rpmbuild --define "_topdir $topdir" --define "_hand_version $version" --define "_bindir /usr/bin" \
  --target "$rpmarch" -bb "$script_dir/hand.spec"

mkdir -p "$outdir"
built=$(find "$topdir/RPMS" -name '*.rpm' -print -quit)
if [ -z "$built" ]; then
  echo "rpmbuild produced no .rpm under $topdir/RPMS" >&2
  exit 1
fi
cp "$built" "$outdir/"
echo "wrote $outdir/$(basename "$built")"
