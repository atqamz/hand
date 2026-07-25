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
- Dashboard maintenance (SPECS.md's per-command update rules) is only partially implemented: `project add/remove/sync` update `data/dashboard.md` via `updateDashboardProjects`; `spawn`/`teardown`/`merge`/`promote` deliberately do not touch the Active Tasks/Events sections. Follow this precedent rather than building it out unilaterally.
- GitHub Releases access (`hand update`, `internal/selfupdate`) shells out to the `gh` CLI rather than calling the REST API directly, matching `internal/ghutil`'s existing convention. Tests fake `gh` with a shell script on `PATH` (see `writeFakeGH` in `internal/selfupdate/selfupdate_test.go`), the same pattern `cmd/status_test.go` uses for `herdr`.
- `hand update` implements SPECS.md's update flow only partially: it deliberately does not re-run `hand init` to refresh the AGENTS.md template and does not print release notes. This is a recorded scope-down, not an oversight; follow this precedent rather than building it out unilaterally.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
