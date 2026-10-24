#!/bin/sh
set -eu

out=${1:?usage: e2e-dist.sh OUTDIR COMMIT}
commit=${2:?usage: e2e-dist.sh OUTDIR COMMIT}
flat=$(mktemp -d)
trap 'rm -rf "$flat"' EXIT

layout() {
	sh .github/scripts/build-dist.sh 0.0.0-e2e stable "$2" "$flat/$1"
	for sub in latest/download download/v0.0.0-e2e download/edge; do
		mkdir -p "$3/$sub"
		cp "$flat/$1"/* "$3/$sub/"
	done
	echo "$2" >"$3/commit.txt"
}

layout candidate "$commit" "$out"
layout next "next-$commit" "$out/next"
cp install.sh install.ps1 "$out/"
mkdir -p "$out/stub"
for target in linux/amd64 darwin/arm64 windows/amd64; do
	os=${target%/*}
	arch=${target#*/}
	ext=
	if [ "$os" = windows ]; then ext=.exe; fi
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -o "$out/stub/claude-$os-$arch$ext" .github/e2e/stub/main.go
done
