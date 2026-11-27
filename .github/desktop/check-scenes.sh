#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
if [ -n "${SCENES_OUT:-}" ]; then
	out=$SCENES_OUT
	rm -rf "$out"
	mkdir -p "$out"
else
	out=$(mktemp -d)
	trap 'rm -rf "$out"' EXIT
fi

FONTCONFIG_FILE="$root/internal/desktop/testdata/fonts.conf" DESKTOP_SHOTS="$out" go test ./internal/desktop -run TestScenes -count=1

status=0
for f in "$out"/*; do
	name=$(basename "$f")
	if [ ! -f "docs/images/desktop/$name" ]; then
		echo "not committed: $name"
		status=1
	elif ! cmp -s "$f" "docs/images/desktop/$name"; then
		echo "stale: $name"
		status=1
	fi
done
for f in docs/images/desktop/*; do
	name=$(basename "$f")
	if [ ! -f "$out/$name" ]; then
		echo "no scene renders it: $name"
		status=1
	fi
done
if [ "$status" -ne 0 ]; then
	echo "regenerate from the repository root: FONTCONFIG_FILE=\$PWD/internal/desktop/testdata/fonts.conf DESKTOP_SHOTS=\$PWD/docs/images/desktop go test ./internal/desktop -run TestScenes" >&2
	exit 1
fi
echo "docs/images/desktop matches the scene renders"
