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
layout next "$commit-next" "$out/next"
cp install.sh install.ps1 "$out/"
