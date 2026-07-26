# Secondhand

You manage a fleet of coding agents using the `hand` CLI.
Run `hand --help` for the full command reference.

## Workflow

1. Read `data/dashboard.md` for current fleet state.
2. Match the request to a project in `data/projects.md`.
3. Edit `data/backlog.md` to record the task with a unique ID.
4. Write a brief at `data/<id>/brief.md`.
5. `hand spawn <id> <project>` to start a worker.
6. `hand watch` as a background task to monitor the fleet.
7. Act on watch output: steer blocked workers with `hand send`, relay results.
8. When told to merge: `hand merge <id>`.
9. `hand teardown <id>` after work is landed.

## Rules

- Never edit files under `projects/`. Workers do that in worktrees.
- Never merge without explicit authorization.
- Never force-teardown without explicit authorization.
- Report outcomes plainly. If work failed, say so with evidence.
- Ship tasks produce PRs or local branches. Scout tasks produce `data/<id>/report.md`.
- `data/backlog.md` is your task queue. Edit it directly.
- For no-mistakes projects, workers use `no-mistakes axi` directly in the worktree.
- Use `qmd search` to find historical context in data/ when available. Fall back to reading files directly.
- Zero comments by default. Only add one when the WHY is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug. Never restate code, narrate what, add banners, or docstring the obvious.
- `internal/herdr/client.go` and `internal/harness/harness.go` are the source of truth for herdr and verified Claude/OpenCode syntax. Codex, Grok, and Pi templates in `SPECS.md` remain unverified until those binaries are available.
- Exit codes: SPECS.md "Exit codes" owns which condition maps to which code, and `cmd.ExitError` (`cmd/root.go`), unwrapped in `Execute()`, is how they are enforced. Plain errors default to code 1; return `&ExitError{Err: ..., Code: 3}` for precondition failures. Usage errors get code 2, either by wrapping a command's `Args` validator in `usageArgs()` or by returning `&ExitError{..., Code: 2}` directly (see the mutually-exclusive merge flags in `cmd/merge.go`, validated before any lock or state read, and the `validateProject*` value checks in `cmd/project.go`). For a value that falls back to a `config/` default when the flag is absent (`--poll`, `--harness`), tag it with `usageValue(fromFlag, err)` so only the command-line form is code 2. Unknown commands directly under root get code 2 via a structural check in `Execute()` comparing `root.ExecuteC()`'s returned command against `root` itself (do not give `root.Args` a non-nil value - that makes cobra's `Runnable()` guard swallow the unknown-command case as a silent exit 0); unknown subcommands of a group (`project`, cobra's built-in `completion`) never reach that check, so `guardSubcommandGroups()` in `cmd/root.go` gives every non-runnable group a `NoArgs` validator plus a help-printing `RunE` - the `RunE` is what stops the same `Runnable()` guard from turning `hand project bogus` into a silent exit 0. `internal/state` and `internal/project` can't construct `ExitError` themselves (import-cycle direction), so they signal preconditions via sentinel errors (`state.ErrTaskNotFound`, `project.ErrNotFound`, etc.) translated to `ExitError{Code:3}` by `cmd/precondition.go`'s `asPrecondition()`; those sentinels hold only the trailing phrase (`not registered`, `not found`) and are wrapped as `fmt.Errorf("project %q %w", name, ErrNotFound)` so each condition renders one string everywhere - keep new sentinels in that shape.
- `hand watch` writes actionable events to stdout and diagnostics (state-read failures, log-append failures) to a separate `errOut` writer threaded through `internal/watcher.Run`/`tick`/`handleEvent` - keep new watcher diagnostics on that path, not `out`.
- Dashboard maintenance (SPECS.md's per-command update rules): `spawn` and `teardown` update the Active Tasks/Completions sections directly (`dashboard.Update{AddActiveTask}` / `{Complete}`); `hand watch` updates agent state/pending-decision/events on every classified transition; `project add/remove/sync` update the Projects section via `updateDashboardProjects`. `promote` never calls `dashboard.Update` at all - the scout task's dashboard row is left as-is (still `KindScout`) even though the underlying task becomes a ship task, a recorded scope-down rather than an oversight. `merge`'s PR path re-syncs and touches the Projects section only if that sync advances the clone; `merge --local` never touches the dashboard. Follow this precedent rather than building it out unilaterally.
- `tests/e2e` (built binary against a real temp home, gated behind `go:build e2e`, run via `make e2e`) is the place for tests that exercise `hand` end-to-end rather than through `cmd` package internals; extend it rather than building a second harness. `TestMain` builds the binary once; `runHand` drives run-to-completion commands, `startHandBackground`/`waitForStdout`/`stop` drive `watch`. `fakes_test.go` holds shared fake `herdr`/`treehouse`/`gh` binaries (case-dispatched shell scripts placed on `PATH` via `binDir`) plus `redirectGitRemote` (git `insteadOf` config) for tests needing a real local git remote with no network. `writeFakeHerdrStatic` gives fixed workspace/tab/pane identifiers for a single spawn-or-promote-then-teardown lifecycle; `writeFakeHerdrWatch` + `setPaneStatus` key responses by pane ID under a status directory so `watch` tests can drive independent per-task transitions. `go test`'s result cache is keyed on the test package's own inputs, not on `TestMain`'s nested `go build` of `cmd/`, so changing production code alone will not invalidate a cached e2e run - pass `-count=1` when checking red/green behavior after a temporary production-code edit.
- GitHub Releases access (`hand update`, `internal/selfupdate`) shells out to the `gh` CLI rather than calling the REST API directly, matching `internal/ghutil`'s existing convention. Tests fake `gh` with a shell script on `PATH` (see `writeFakeGH` in `internal/selfupdate/selfupdate_test.go`), the same pattern `cmd/status_test.go` uses for `herdr`.
- `hand update` implements SPECS.md's update flow only partially: it deliberately does not re-run `hand init` to refresh the AGENTS.md template and does not print release notes. This is a recorded scope-down, not an oversight; follow this precedent rather than building it out unilaterally.
- Dev environment is Nix-based: `nix develop` provides the toolchain (listed in `flake.nix`, summarized for humans in `CONTRIBUTING.md`), and `make lint`/`go build ./...`/`go test -race ./...` are the commands to verify inside it. `flake.nix`'s `packages.default` derivation needs `nativeCheckInputs = [ pkgs.git ]` because its test suite execs `git` directly, and a `postInstall` rename is required because `buildGoModule` names the output binary after the Go module (`secondhand`) while every other build path (`Makefile`, `.gitignore`) expects `hand`. Intel macOS (`x86_64-darwin`) is not a supported target: the pinned `nixpkgs-unstable` aborts evaluation for it because upstream dropped support, so re-adding it to `systems` breaks `nix flake show` and every output under that system.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
