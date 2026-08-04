# Secondhand

`hand` is a CLI that manages a fleet of coding agents from a fleet home (`data/`, `state/`, this file).
This checkout is the tool's own source, not a fleet home itself - there is no `state/hand.db` here, so `internal/home.IsHome` reports false.
`internal/agentsmd`'s `generatedBody` constant is the authoritative template `hand init` writes and `hand update` refreshes into every real fleet home's `AGENTS.md`; see SPECS.md's "AGENTS.md (target)" section for the full design, and `hand --help` for the command reference.

## Rules

- Comments obey two rules `make lint` enforces through `tools/commentlint`: a comment may not open with the identifier it documents, and a comment block may not exceed three lines. CONTRIBUTING.md's "Comments" section owns the exemptions and the reasoning.
- Command output goes through `internal/axi` as TOON and every failure through `cmd/root.go`'s error document; `hand watch`'s event stream is the one exception, and SPECS.md's "Output shape" section owns the contract.
- Harness/herdr syntax, exit-code enforcement, watch's stdout/errOut split, and first-run prompt handling are commented at point of use (`internal/herdr`, `internal/harness`, `cmd/root.go`, `cmd/precondition.go`, `internal/watcher`, `cmd/teardown.go`, `cmd/prdetect.go`, `cmd/merge.go`, `cmd/launch.go`); SPECS.md's "Exit codes" and each command's spec section own the authoritative tables.
- `herdr`, `treehouse` and `gh` are faked once, in `internal/faketool`, for every suite; a test declares the fleet it wants rather than writing sh. `internal/faketool/FIDELITY.md` records what the real tools do and `tests/contract` (`make contract`) rechecks that record against them. Extend the shared fake, never hand-write another; SPECS.md's "Testing strategy" owns the rule behind it.
- Test, release, and write conventions live as doc comments: `tests/e2e` (`fakes_test.go`, `e2e_test.go`), GitHub access via `gh` (`internal/ghutil`), AGENTS.md refresh (`internal/agentsmd`), atomic writes (`internal/atomicfile`).
- Dev environment is Nix-based (`flake.nix`, `CONTRIBUTING.md`); `make lint`, `go build ./...`, and `go test -race ./...` verify inside `nix develop`.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
