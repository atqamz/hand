#!/bin/sh
set -eu

version=${1:?usage: build-desktop.sh VERSION CHANNEL COMMIT [OUTDIR]}
channel=${2:?usage: build-desktop.sh VERSION CHANNEL COMMIT [OUTDIR]}
commit=${3:?usage: build-desktop.sh VERSION CHANNEL COMMIT [OUTDIR]}
mkdir -p "${4:-.}"
out=$(cd "${4:-.}" && pwd)
pkg=github.com/atqamz/hand/internal/desktopasset
flags="-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

digest() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	else
		shasum -a 256 "$@"
	fi
}

for target in linux/amd64 linux/arm64; do
	os=${target%/*}
	arch=${target#*/}
	name=hand-desktop-$os-$arch
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$flags" -o "$tmp/hand-desktop" ./cmd/hand-desktop
	tar -czf "$out/$name.tar.gz" -C "$tmp" hand-desktop
	rm "$tmp/hand-desktop"
	(cd "$out" && digest "$name.tar.gz" >"$name.sha256")
done
