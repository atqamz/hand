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

1. Open an issue describing the intent, design, or proposal, and get agreement there before writing code. This applies to any contribution, no matter the size. See "Reporting issues" below for what to include.
2. Fork and branch from main.
3. Make changes.
4. make lint && make test
5. make e2e if you changed CLI behavior (end-to-end suite, excluded from make test).
6. make contract if you changed how hand calls herdr, treehouse or gh, and you have those installed. It checks the records in internal/faketool/FIDELITY.md against the real tools, skipping whichever is absent, so CI never runs it.
7. nix build .#default if you changed Go dependencies (CI builds the flake, and a stale vendorHash in flake.nix fails it).
8. Open a PR whose body carries a closing keyword (Closes, Fixes, or Resolves) directly preceding a fully qualified atqamz/secondhand#N, on its own line. A bare #N links but reads ambiguously outside the repo, and a reference without the keyword links the issue without ever closing it.

Commits use conventional commits: feat:, fix:, chore:, etc.
release-please handles versioning and changelogs from these.

## Comments

Two rules, enforced by `make lint` rather than by a reviewer reading a diff:

1. A comment may not open with the identifier it documents.
2. A comment block may not exceed three lines.

Consecutive `//` lines are one block, and a bare `//` line inside a run does not break it.
Rule 1 applies wherever Go's doc convention does not: unexported declarations, everything in `_test.go`, and comments inside function bodies.
An exported declaration's doc comment is required by convention to open with its name, so it is exempt from rule 1, but not from rule 2.
Exempt from both rules: the package doc comment, directives (`//go:build`, `//go:generate`, `//nolint`, `// #nosec`), and files carrying the generated-code header.

Rule 2 will occasionally be wrong, because a genuinely subtle invariant sometimes needs a fourth line.
That is accepted: a rule that is right most of the time and mechanically enforced binds harder than one that is right always and enforced never.
Prose that outgrows three lines belongs in SPECS.md, which is where it is read.

`go run ./tools/commentlint .` runs the check alone and prints one `file:line` per violation.

## Reporting issues

Open a GitHub issue with repro steps, OS, arch, and hand --version.
