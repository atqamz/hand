#!/bin/sh
set -eu

dir=${1:?usage: check-assets.sh DIR}
cd "$dir"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

while read -r stem ext exe want; do
	[ "$exe" = - ] && exe=
	test -f "$stem$ext" || { echo "missing $stem$ext" >&2; exit 1; }
	test -f "$stem.sha256" || { echo "missing $stem.sha256" >&2; exit 1; }
	grep -q " $stem$ext\$" "$stem.sha256" || { echo "$stem.sha256 does not name $stem$ext" >&2; exit 1; }
	sha256sum -c "$stem.sha256"
	rm -rf "${tmp:?}/x"
	mkdir "$tmp/x"
	case $ext in
	.zip) unzip -q "$stem$ext" -d "$tmp/x" ;;
	*) tar -xzf "$stem$ext" -C "$tmp/x" ;;
	esac
	test "$(ls -A "$tmp/x")" = "hand-desktop$exe" || { echo "$stem$ext holds '$(ls -A "$tmp/x")', want hand-desktop$exe" >&2; exit 1; }
	got=$(file -b "$tmp/x/hand-desktop$exe")
	printf '%s: %s\n' "$stem$ext" "$got"
	printf '%s\n' "$got" | grep -q "$want" || { echo "$stem$ext: not $want" >&2; exit 1; }
done <<'LIST'
hand-desktop-linux-amd64 .tar.gz - ELF 64-bit.*x86-64
hand-desktop-linux-arm64 .tar.gz - ELF 64-bit.*ARM aarch64
hand-desktop-darwin-arm64 .tar.gz - Mach-O 64-bit arm64
hand-desktop-darwin-amd64 .tar.gz - Mach-O 64-bit x86_64
hand-desktop-windows-amd64 .zip .exe PE32+ executable.*(GUI).*x86-64
LIST

n=$(ls hand-desktop-*.sha256 | wc -l)
test "$n" = 5 || { echo "$n checksum files, want 5" >&2; exit 1; }
