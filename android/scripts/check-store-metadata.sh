#!/usr/bin/env bash
set -euo pipefail
shopt -s nullglob
export LC_ALL=C.UTF-8
dir=fastlane/metadata/android/en-US
fail() { echo "store metadata: $*" >&2; exit 1; }
limit() { [ "$(tr -d '\r\n' < "$1" | wc -m)" -le "$2" ] || fail "$1 is over $2 characters"; }
limit "$dir/title.txt" 50
limit "$dir/short_description.txt" 80
limit "$dir/full_description.txt" 4000
tr -d '\r\n' < "$dir/short_description.txt" | grep -q '\.$' && fail "short_description.txt ends with a dot"
for f in "$dir"/changelogs/*; do
  [[ $(basename "$f") =~ ^[0-9]+\.txt$ ]] || fail "$f is not named <versionCode>.txt"
  limit "$f" 500
done
code=$(sed -n 's/^versionCode=//p' version.properties)
[ -f "$dir/changelogs/$code.txt" ] || fail "changelogs/$code.txt is missing"
[[ $(file -b "$dir/images/icon.png") == "PNG image data, 512 x 512,"* ]] || fail "images/icon.png is not a 512x512 PNG"
