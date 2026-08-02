# Secondhand

`hand` is a CLI that manages a fleet of coding agents from a fleet home (`data/`, `state/`, this file).
This checkout is the tool's own source, not a fleet home itself - `internal/home.IsHome` reports false here.
`internal/agentsmd`'s `generatedBody` constant is the authoritative template `hand init` writes and `hand update` refreshes into every real fleet home's `AGENTS.md`; see SPECS.md's "AGENTS.md (target)" section for the full design, and `hand --help` for the command reference.

## Rules

- Zero comments by default. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug. Never restate code, narrate what, add banners, or docstring the obvious.
- Harness/herdr syntax, exit-code enforcement, watch's stdout/errOut split, per-command dashboard rules, and first-run prompt handling are commented at point of use (`internal/herdr`, `internal/harness`, `cmd/root.go`, `cmd/precondition.go`, `internal/watcher`, `cmd/teardown.go`, `cmd/prdetect.go`, `cmd/merge.go`, `cmd/launch.go`); SPECS.md's "Exit codes" and each command's spec section own the authoritative tables.
- Test, release, and write conventions live as doc comments: `tests/e2e` (`fakes_test.go`, `e2e_test.go`), GitHub access via `gh` (`internal/ghutil`), AGENTS.md refresh (`internal/agentsmd`), atomic writes (`internal/atomicfile`); SPECS.md's "Testing strategy" owns the fake-fidelity rule every faked backend invocation follows.
- Dev environment is Nix-based (`flake.nix`, `CONTRIBUTING.md`); `make lint`, `go build ./...`, and `go test -race ./...` verify inside `nix develop`.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
