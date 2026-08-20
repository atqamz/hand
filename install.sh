#!/bin/sh
set -eu

# Installs hand only: no herdr, treehouse, gh, agent harness, or no-mistakes.
# HAND_INSTALL_DIR overrides the install directory; HAND_INSTALL_VERSION pins a tag.

repo="atqamz/hand"
bin_dir="${HAND_INSTALL_DIR:-$HOME/.local/bin}"
tag="${HAND_INSTALL_VERSION:-}"

log() { printf '%s\n' "$*" >&2; }
die() { log "install.sh: $*"; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required"; }
need curl
need tar

case "$(uname -s)" in
  Linux) goos=linux ;;
  Darwin) goos=darwin ;;
  *) die "unsupported OS $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64) goarch=amd64 ;;
  arm64|aarch64) goarch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if [ -z "$tag" ]; then
  tag=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
  [ -n "$tag" ] || die "could not resolve the latest release tag"
fi

asset="hand-${goos}-${goarch}.tar.gz"
base_url="https://github.com/$repo/releases/download/$tag"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

log "downloading $asset ($tag)..."
curl -fsSL -o "$tmp/$asset" "$base_url/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$base_url/checksums.txt"

want=$(sed -n "s/^\([0-9a-f]*\)[[:space:]]*\*\{0,1\}${asset}\$/\1/p" "$tmp/checksums.txt" | head -n1)
[ -n "$want" ] || die "checksums.txt has no entry for $asset"

if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
else
  die "sha256sum or shasum is required to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $asset: want $want, got $got"

tar xzf "$tmp/$asset" -C "$tmp" hand
mkdir -p "$bin_dir"
install -m 755 "$tmp/hand" "$bin_dir/hand"

log "installed hand to $bin_dir/hand"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) log "note: $bin_dir is not on your PATH" ;;
esac
