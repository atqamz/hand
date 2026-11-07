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
MINGW* | MSYS* | CYGWIN*) die "on Windows, run install.ps1 in PowerShell instead: see https://github.com/atqamz/hand#windows" ;;
*) die "Hand runs on Linux, macOS and Windows only" ;;
esac
machine=$(uname -m)
case "$machine" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $machine" ;;
esac
for tool in curl tar sed; do
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
iroh="hand-iroh-$os-$arch"
tmp=$(mktemp -d 2>/dev/null) || die "cannot create a temporary folder"
part=""
ipart=""
trap 'rm -rf "$tmp"; if [ -n "$part" ]; then rm -f "$part"; fi; if [ -n "$ipart" ]; then rm -f "$ipart"; fi' EXIT
trap 'exit 1' HUP INT TERM

curl -fsL -o "$tmp/$asset" "$url/$asset" || die "download failed: $url/$asset"
curl -fsL -o "$tmp/checksums.txt" "$url/checksums.txt" || die "download failed: $url/checksums.txt"
grep " $asset\$" "$tmp/checksums.txt" >"$tmp/want" || die "checksum mismatch for $asset"
(cd "$tmp" && $check >/dev/null 2>&1) || die "checksum mismatch for $asset"
tar -xzf "$tmp/$asset" -C "$tmp" hand >/dev/null 2>&1 || die "cannot unpack $asset"

want=$("$tmp/hand" version 2>/dev/null | while read -r key value; do if [ "$key" = "iroh:" ]; then printf '%s' "$value"; fi; done)
got=""
if [ -n "$want" ]; then
	status=$(curl -sL -o "$tmp/$iroh.tar.gz" -w '%{http_code}' "$url/$iroh.tar.gz") || die "download failed: $url/$iroh.tar.gz"
	case "$status" in
	200)
		curl -fsL -o "$tmp/$iroh.sha256" "$url/$iroh.sha256" || die "download failed: $url/$iroh.sha256"
		grep " $iroh.tar.gz\$" "$tmp/$iroh.sha256" >"$tmp/want" || die "checksum mismatch for $iroh.tar.gz"
		(cd "$tmp" && $check >/dev/null 2>&1) || die "checksum mismatch for $iroh.tar.gz"
		tar -xzf "$tmp/$iroh.tar.gz" -C "$tmp" hand-iroh >/dev/null 2>&1 || die "cannot unpack $iroh.tar.gz"
		chmod 0755 "$tmp/hand-iroh"
		out=$("$tmp/hand-iroh" --version 2>/dev/null) || out=""
		got=$(printf '%s\n' "$out" | sed -n '1s/^[[:space:]]*hand-iroh \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\) iroh [^[:space:]][^[:space:]]*[[:space:]]*$/\1/p')
		[ "$got" = "$want" ] || die "$iroh.tar.gz is hand-iroh ${got:-unknown}, but this hand needs $want"
		;;
	404) printf 'this release has no %s.tar.gz; phone access over iroh is not installed\n' "$iroh" ;;
	*) die "download failed: $url/$iroh.tar.gz (HTTP $status)" ;;
	esac
fi

mkdir -p "$dir" 2>/dev/null || die "cannot create $dir"
part=$(mktemp "$dir/.hand.XXXXXX" 2>/dev/null) || die "cannot write to $dir"
{ cp "$tmp/hand" "$part" && chmod 0755 "$part"; } 2>/dev/null || die "cannot install $dir/hand"
if [ -n "$got" ]; then
	[ ! -d "$dir/hand-iroh" ] || die "cannot install $dir/hand-iroh: it is a directory"
	ipart=$(mktemp "$dir/.hand-iroh.XXXXXX" 2>/dev/null) || die "cannot write to $dir"
	{ cp "$tmp/hand-iroh" "$ipart" && chmod 0755 "$ipart" && mv -f "$ipart" "$dir/hand-iroh"; } 2>/dev/null || die "cannot install $dir/hand-iroh"
	ipart=""
fi
mv -f "$part" "$dir/hand" 2>/dev/null || die "cannot install $dir/hand"
part=""
printf 'installed hand %s to %s\n' "$("$dir/hand" version | head -n 1)" "$dir/hand"
if [ -n "$got" ]; then
	printf 'installed hand-iroh %s to %s\n' "$got" "$dir/hand-iroh"
fi
case ":$PATH:" in
*":$dir:"*) ;;
*) printf "%s is not on PATH; add this line to your shell profile: export PATH=\"%s:\$PATH\"\n" "$dir" "$dir" ;;
esac
