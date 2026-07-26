# Contributing

## Getting started

git clone https://github.com/atqamz/secondhand
cd secondhand
nix develop
make build
make test

`nix develop` is optional and provides the full toolchain: Go, golangci-lint, gopls, gotools, and gcc (gcc is required because `make test` runs with `-race`, which needs CGO).
Without Nix, install those yourself.

## Making changes

1. Fork and branch from main.
2. Make changes.
3. make lint && make test
4. Open a PR.

Commits use conventional commits: feat:, fix:, chore:, etc.
release-please handles versioning and changelogs from these.

## Reporting issues

Open a GitHub issue with repro steps, OS, arch, and hand --version.
