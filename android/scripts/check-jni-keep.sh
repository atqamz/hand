#!/usr/bin/env bash
set -euo pipefail

apk=$1
sources=${SOURCES:-src-tauri/src}
package=io.github.atqamz.handcompanion
escaped=${package//./\\.}
dex=$("$ANDROID_HOME/cmdline-tools/latest/bin/apkanalyzer" dex packages --defined-only "$apk")
entries=$(unzip -Z1 "$apk")
failed=0

fail() {
  echo "$1"
  failed=1
}

if grep -nE 'find_class|call_static_method|get_static|new_object|get_field|get_method_id' "$sources"/*.rs; then
  fail "a JNI lookup this script does not follow: extend it and the keep rules"
fi

grep -qx lib/arm64-v8a/libhand_android_lib.so <<<"$entries" || fail "lib/arm64-v8a/libhand_android_lib.so is missing"
! grep '^lib/' <<<"$entries" | grep -qv '^lib/arm64-v8a/' || fail "the APK holds a library for another ABI"

natives=$(grep -ohP 'Java_io_github_atqamz_handcompanion_\K\w+' "$sources/host.rs" || true)
calls=$(perl -0ne 'print "$1\n" while /call_method\w*\(\s*[^,]+,\s*"(\w+)"/g' "$sources"/*.rs)

call_sites=$(grep -ohE 'call_method\w*\(' "$sources"/*.rs | wc -l)
[[ $(grep -c . <<<"$calls") -eq $call_sites ]] || fail "a call_method names its method by something other than a string literal"

for category in natives calls; do
  [[ -n ${!category} ]] || fail "no $category found in $sources: the script no longer reads the sources it checks"
done

require() {
  grep -qE "$1" <<<"$dex" || fail "R8 removed or renamed: $2"
}

while read -r native; do
  [[ -n $native ]] || continue
  require "^M.*[[:space:]]$escaped\.${native%%_*} .* ${native#*_}\(" "${native%%_*}.${native#*_} (native)"
done <<<"$natives"

while read -r method; do
  [[ -n $method ]] || continue
  require "^M.*[[:space:]]$escaped\.AppHost .* ${method}\(" "AppHost.$method"
done <<<"$calls"

exit "$failed"
