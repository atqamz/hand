#!/bin/sh
set -eu

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
version=$(sed -n 's/^var Version = "\(.*\)"$/\1/p' internal/cli/cli.go)
test -n "$version"
commit=${1:-$(git rev-parse --short=12 HEAD)}
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT
exe=$(go env GOEXE)
handflags="-s -w -X github.com/atqamz/hand/internal/cli.Version=$version -X github.com/atqamz/hand/internal/cli.Channel=source -X github.com/atqamz/hand/internal/cli.Commit=$commit"
deskflags="-s -w -X github.com/egoist/mygo.production=1 -X github.com/atqamz/hand/internal/desktopasset.Version=$version -X github.com/atqamz/hand/internal/desktopasset.Channel=stable -X github.com/atqamz/hand/internal/desktopasset.Commit=$commit"
if [ "$exe" = .exe ]; then
	deskflags="$deskflags -H=windowsgui"
fi

CGO_ENABLED=0 go build -trimpath -ldflags "$handflags" -o "$out/hand$exe" .
CGO_ENABLED=0 go build -trimpath -tags mygo_noinspector -ldflags "$deskflags" -o "$out/hand-desktop$exe" ./cmd/hand-desktop

want=$("$out/hand$exe" version | awk -F': ' '/^version:/{v=$2} /^commit:/{c=$2} END {print v" "c}')
test -n "$want"
got=$("$out/hand-desktop$exe" --version)
test "$got" = "hand-desktop $want" || {
	echo "hand-desktop --version printed '$got', want 'hand-desktop $want'" >&2
	exit 1
}
printf 'hand-desktop --version: %s\n' "$got"

if [ "$(go env GOOS)" = windows ]; then
	code=0
	"$out/hand-desktop$exe" >"$out/refusal" 2>&1 </dev/null || code=$?
	test "$code" = 2 || {
		echo "a stable hand-desktop on $(go env GOOS) exited $code, want 2" >&2
		exit 1
	}
	grep -q '^hand-desktop: unsupported on ' "$out/refusal" || {
		echo "a stable hand-desktop on $(go env GOOS) printed '$(cat "$out/refusal")'" >&2
		exit 1
	}
	printf 'stable build refuses on %s: %s\n' "$(go env GOOS)" "$(cat "$out/refusal")"
fi
