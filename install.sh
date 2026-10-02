#!/bin/sh
set -eu

base="${HAND_INSTALL_BASE:-https://github.com/atqamz/hand/releases}"
version="${HAND_INSTALL_VERSION:-latest}"
dir="${HAND_INSTALL_DIR:-$HOME/.local/bin}"

die() {
	printf 'install.sh: %s\n' "$1" >&2
	exit 1
}

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "Hand runs on Linux and macOS only" ;;
esac
machine=$(uname -m)
case "$machine" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $machine" ;;
esac
for tool in curl tar; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done
if command -v sha256sum >/dev/null 2>&1; then
	check="sha256sum -c want"
elif command -v shasum >/dev/null 2>&1; then
	check="shasum -a 256 -c want"
else
	die "sha256sum or shasum is required"
fi

if [ "$version" = latest ]; then
	url="$base/latest/download"
else
	url="$base/download/$version"
fi
asset="hand-$os-$arch.tar.gz"
tmp=$(mktemp -d)
part=""
trap 'rm -rf "$tmp"; if [ -n "$part" ]; then rm -f "$part"; fi' EXIT
trap 'exit 1' HUP INT TERM

curl -fsSL -o "$tmp/$asset" "$url/$asset"
curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt"
grep " $asset\$" "$tmp/checksums.txt" >"$tmp/want" || die "checksum mismatch for $asset"
(cd "$tmp" && $check >/dev/null 2>&1) || die "checksum mismatch for $asset"
tar -xzf "$tmp/$asset" -C "$tmp" hand

mkdir -p "$dir"
part=$(mktemp "$dir/.hand.XXXXXX")
cp "$tmp/hand" "$part"
chmod 0755 "$part"
mv -f "$part" "$dir/hand"
part=""
printf 'installed hand %s to %s\n' "$("$dir/hand" version | head -n 1)" "$dir/hand"
