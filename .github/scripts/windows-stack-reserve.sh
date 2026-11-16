#!/bin/sh
# golang/go#81238: Windows dispatches an exception on the faulting goroutine's
# own stack, and an Intel Xeon with AMX pushes a ~11.7 KiB CONTEXT into the 4 KiB
# the runtime reserves below that stack, corrupting the heap span there; a test
# then dies in the collector on whatever goroutine runs. 16 KiB covers the
# largest CONTEXT a hosted runner reports plus the 2 KiB the dispatcher adds.
set -eu

goroot=${1:-$(go env GOROOT)}
if [ -z "${1:-}" ] && [ "$(go env GOOS)" != windows ]; then
	echo "windows-stack-reserve: $(go env GOOS) does not dispatch exceptions on the goroutine stack; nothing to do"
	exit 0
fi
if command -v cygpath >/dev/null 2>&1; then
	goroot=$(cygpath -u "$goroot")
fi
stack=$goroot/src/runtime/stack.go
if [ ! -f "$stack" ]; then
	echo "windows-stack-reserve: $goroot is not a Go toolchain" >&2
	exit 1
fi
stripped=$(mktemp "${TMPDIR:-/tmp}/windows-stack-reserve.XXXXXX")
trap 'rm -f "$stripped"' EXIT HUP INT TERM
# Go comments keep a `/* … */` declaration shape alive across lines, so the
# marker reads the code with comments removed; a `//` or `/* */` mention of the
# name must not skip the patch.
awk '{ code = ""; for (i = 1; i <= length($0); i++) {
	if (block) { if (substr($0, i, 2) == "*/") { block = 0; i++ } continue }
	if (substr($0, i, 2) == "/*") { block = 1; i++; continue }
	if (substr($0, i, 2) == "//") break
	code = code substr($0, i, 1) }
print code }' "$stack" >"$stripped"
if grep -Eq \
	'^[[:space:]]*(var|const)[[:space:]]+stackSystemReserve([[:space:]]|=|$)|^[[:space:]]*stackSystemReserve[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]*=' \
	"$stripped"; then
	echo "windows-stack-reserve: $(go env GOVERSION) sizes the reserve from the host"
	exit 0
fi
if grep -q 'goos\.IsWindows\*16384' "$stack"; then
	echo "windows-stack-reserve: $(go env GOVERSION) already reserves 16 KiB"
	exit 0
fi
floor=$(grep -c 'goos\.IsWindows\*4096' "$stack" || true)
if [ "$floor" -ne 1 ]; then
	echo "windows-stack-reserve: $stack holds $floor goos.IsWindows*4096 lines, want one" >&2
	exit 1
fi
sed -i 's/goos\.IsWindows\*4096/goos.IsWindows*16384/' "$stack"
if ! grep -q 'goos\.IsWindows\*16384' "$stack"; then
	echo "windows-stack-reserve: $stack still reserves 4096" >&2
	exit 1
fi
if [ -z "${1:-}" ]; then
	# A toolchain the edit does not fit fails here, not halfway through the tests.
	go list -export -f '{{.Export}}' runtime >/dev/null
fi
echo "windows-stack-reserve: $(go env GOVERSION) reserves 16 KiB below a Windows goroutine stack"
