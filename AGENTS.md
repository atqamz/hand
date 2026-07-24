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
- `SPECS.md`'s herdr CLI call templates and claude/opencode harness launch templates are known to be wrong (checked against each tool's `--help`). `internal/herdr/client.go` and `internal/harness/harness.go` are the verified source of truth for that syntax; re-verify against `--help` before trusting SPECS.md for those two areas.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.
