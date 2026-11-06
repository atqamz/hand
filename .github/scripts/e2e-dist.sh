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

digest() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	else
		shasum -a 256 "$@"
	fi
}

iroh() {
	version=$(sed -n 's/^[[:space:]]*Version = "\(.*\)"$/\1/p' internal/sidecar/sidecar.go)
	test -n "$version"
	for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
		os=${target%/*}
		arch=${target#*/}
		ext=
		if [ "$os" = windows ]; then ext=.exe; fi
		stem=hand-iroh-$os-$arch
		CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-X main.version=$version" -o "$flat/hand-iroh$ext" .github/e2e/stub-iroh/main.go
		if [ "$os" = windows ]; then
			rm -f "$flat/$stem.zip"
			(cd "$flat" && zip -X "$stem.zip" hand-iroh.exe)
			(cd "$flat" && digest "$stem.zip" >"$stem.sha256")
		else
			tar -czf "$flat/$stem.tar.gz" -C "$flat" hand-iroh
			(cd "$flat" && digest "$stem.tar.gz" >"$stem.sha256")
		fi
		for sub in latest/download download/v0.0.0-e2e; do
			cp "$flat/$stem".* "$1/$sub/"
		done
		rm -f "$flat/hand-iroh$ext" "$flat/$stem".*
	done
}

layout candidate "$commit" "$out"
layout next "next-$commit" "$out/next"
iroh "$out"
iroh "$out/next"
cp install.sh install.ps1 "$out/"
mkdir -p "$out/stub"
for target in linux/amd64 darwin/arm64 windows/amd64; do
	os=${target%/*}
	arch=${target#*/}
	ext=
	if [ "$os" = windows ]; then ext=.exe; fi
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -o "$out/stub/claude-$os-$arch$ext" .github/e2e/stub/main.go
done
