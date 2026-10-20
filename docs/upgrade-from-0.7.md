# Upgrading from 0.7

The new Hand does not read 0.7 data. Its supervisor rebuilds what still matters, with your approval. [`docs/vocabulary.md`](vocabulary.md#changed-since-07) lists the 0.7 terms that changed.

1. With 0.7 still installed, finish or `hand teardown` every live task.
2. Keep the old binary if you like, for example `cp "$(command -v hand)" ~/.local/bin/hand-0.7`.
3. Build the new one, either as `hand-next` to try it beside 0.7 or as `hand` to replace it.
4. In the fleet folder, remove the 0.7 wiring that would keep waking a supervisor: the Stop hook with `supervision claude-stop` in `.claude/settings.json`, and `.pi/extensions/hand-*` and `.opencode/plugins/hand-*`. `hand init` warns if they are still there.
5. Run `hand init` there with the new binary. The old `AGENTS.md` and skill are replaced. Everything else (`data/`, `state/`, `config/`, `projects/`) is left alone, and the folder gets a `secondhand-migrate` skill.
6. Open the supervisor there and ask it to migrate the fleet. It lists what it would create (projects, open tasks, queue items, decisions, routing and memory), then waits for your approval before it changes anything.
7. Afterwards it can remove the 0.7 pool worktrees and shared files, and then archive the old data into `legacy-0.7/`. Each of those steps waits for your approval. Its last step runs `hand init`, which removes the migrate skill.
8. Trust the worktrees folder once, so Claude does not ask in every worktree: `w="${SECONDHAND_HOME:-$HOME/.secondhand}/worktrees"; mkdir -p "$w" && cd "$w" && claude`. On Windows, use the PowerShell line under [Windows](install.md#windows).
