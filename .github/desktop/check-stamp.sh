#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/cli/cli.go)
test -n "$version"
probe=qzstampprobe
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

sh .github/scripts/build-desktop.sh "$version" "$probe" "$(git rev-parse --short=12 HEAD)" "$out/assets" >/dev/null
mkdir "$out/x"
unzip -q "$out/assets/hand-desktop-windows-amd64.zip" -d "$out/x"
grep -aq "$probe" "$out/x/hand-desktop.exe" || {
	echo "the Windows hand-desktop.exe does not carry the channel build-desktop.sh was given" >&2
	exit 1
}
echo "the Windows hand-desktop.exe carries the channel it was built with"
