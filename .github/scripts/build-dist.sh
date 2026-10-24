#!/bin/sh
set -eu

version=${1:?usage: build-dist.sh VERSION CHANNEL COMMIT [OUTDIR]}
channel=${2:?usage: build-dist.sh VERSION CHANNEL COMMIT [OUTDIR]}
commit=${3:?usage: build-dist.sh VERSION CHANNEL COMMIT [OUTDIR]}
mkdir -p "${4:-.}"
out=$(cd "${4:-.}" && pwd)
pkg=github.com/atqamz/hand/internal/cli
flags="-s -w -X $pkg.Version=$version -X $pkg.Channel=$channel -X $pkg.Commit=$commit"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

digest() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	else
		shasum -a 256 "$@"
	fi
}

for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
	os=${target%/*}
	arch=${target#*/}
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$flags" -o "$tmp/hand" .
	tar -czf "$out/hand-$os-$arch.tar.gz" -C "$tmp" hand
	rm "$tmp/hand"
done
(cd "$out" && digest hand-*.tar.gz >checksums.txt)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$flags" -o "$tmp/hand.exe" .
rm -f "$out/hand-windows-amd64.zip"
(cd "$tmp" && zip -X "$out/hand-windows-amd64.zip" hand.exe)
(cd "$out" && digest hand-windows-amd64.zip >hand-windows-amd64.sha256)
