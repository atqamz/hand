# Hand

Hand is a personal supervisor layer for coding agents. You talk to one supervisor agent. It captures every request as a task, dispatches Claude Code, Codex or opencode workers into isolated git worktrees through [Luvus](https://github.com/RizRiyz/luvus), waits for them with zero tokens, reads their reports, and asks you only through decisions. Each fleet is a folder with its own SQLite state, and one `hand board` shows every fleet.

Linux only. The design and its non-goals are in [`docs/spec.md`](docs/spec.md).

## Requirements

- Go 1.26.5, git, and Luvus 0.14.x (`luvus` on `PATH`).
- Claude Code, Codex and/or opencode 2.x, logged in. opencode uses the model from its own configuration.
- Optional: `notify-send` for desktop notifications.

## Install and make a fleet

```sh
go build -o ~/.local/bin/hand .
mkdir -p ~/fleet-work && cd ~/fleet-work
hand init                                      # or: hand init --name work DIR
hand project add myrepo /absolute/path/to/repo
```

To try it next to an older Hand, build it under another name such as `~/.local/bin/hand-next`. Hand calls itself by its binary's name in the fleet's `AGENTS.md`, its skill, its help lines and its errors, so a supervisor in that fleet runs `hand-next`. When you later install it as plain `hand`, run that `hand init` in each fleet to rewrite them.

A fleet is any folder `hand init` has run in. Commands find it from the working directory or any folder inside it, from `--home DIR`, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`. To move one, `mv` the folder and run `hand init` in its new place.

Hand keeps shared state in `~/.secondhand`, or `$SECONDHAND_HOME`. There, `fleets/` links each fleet ID to its folder, and `worktrees/` holds every worker worktree, outside every fleet.

Claude asks once to trust a folder. Trust the worktrees folder once for every fleet:

```sh
w="${SECONDHAND_HOME:-$HOME/.secondhand}/worktrees"; mkdir -p "$w" && cd "$w" && claude
```

## Keep the watcher and the board running

Each fleet has its own watcher. From inside the fleet folder:

```sh
mkdir -p ~/.config/systemd/user
hand unit watch > ~/.config/systemd/user/secondhand-watch-work.service
systemctl --user daemon-reload
systemctl --user enable --now secondhand-watch-work
```

One board serves every fleet. Install it once, from any folder:

```sh
hand unit board > ~/.config/systemd/user/secondhand-board.service
systemctl --user daemon-reload
systemctl --user enable --now secondhand-board
```

The board listens on `127.0.0.1:7777`. Each fleet has its own page at `http://127.0.0.1:7777/<fleet id>/`, and `/` lists the fleets. Each fleet keeps its own token in `board.token` in its folder, so logging in to one fleet never logs you out of another. A fleet you add or move shows up without a restart. After a move, generate only its watcher unit again. To reach the board from a phone, use a tunnel such as `ssh -L` or `tailscale serve`.

`hand open` opens a page in your browser, from inside a fleet:

| Command | Opens |
|---|---|
| `hand open`, `hand open supervisor` | the fleet page |
| `hand open tN` | the task |
| `hand open dN` | the decision |
| `hand open rN`, `hand open aN` | the report or attempt, on its task's page |
| `hand open tN --pr` | the task's newest PR, on GitHub |

It logs the browser in with the fleet's token through `xdg-open` and never prints the token. It finds the board through `~/.secondhand/board.addr`, which the running board writes.

To move from one board per fleet, stop and remove each old board unit, then install the one above:

```sh
systemctl --user disable --now secondhand-board-work
rm ~/.config/systemd/user/secondhand-board-work.service
```

In a systemd user session, Hand runs each fleet's Luvus server in its own user unit, `secondhand-luvus-<fleet id>`. Restarting the watcher or the board therefore never stops the agents. `systemctl --user stop secondhand-luvus-<fleet id>` stops the server and every agent in that fleet. Without a user session, for example over plain SSH, Hand starts Luvus directly.

The board is where you start, chat with and resume the supervisor. Those controls work only while the board listens on a loopback address, which a tunnel keeps true. On a network address such as `0.0.0.0` the supervisor controls are off; answering decisions and acknowledging reports still work.

## The supervisor

Open the fleet's page with `hand open` and press **Start**. Pick a routing profile, or a harness with its model and effort. Hand runs the supervisor in the background in the fleet's Luvus session, in full-auto mode:
- `claude --dangerously-skip-permissions`;
- `codex --dangerously-bypass-approvals-and-sandbox`;
- `opencode --auto`.

The page updates itself as things change, and keeps what you are typing. It shows the conversation without tool calls or thinking. It takes your messages, and answers the supervisor's blocked screens with a fixed set of keys. The live terminal stays in Luvus. `hand attach supervisor` opens it in your terminal, `hand attach aN` opens a worker's, and `hand attach` opens the whole fleet session. You never have to. The same controls exist as `hand supervisor start|send|keys|interrupt|stop|resume|show`.

`hand init` writes the folder's `AGENTS.md` and `CLAUDE.md`, which make the agent run `hand orient` every turn. It also installs the `secondhand` skill for Claude Code, Codex, Grok and Pi. Every `hand init` rewrites these files, so put your own preferences in `memory/operator.md` instead.

The loop it follows:
1. `hand orient`
2. capture with `hand task add`
3. dispatch with `hand attempt start --profile …`
4. wait: wakes arrive as messages that start with `[hand v1 wake]`
5. read and ack reports
6. ask with `hand decision ask`
7. finish with `hand task done`

Routing profiles live in `<home>/routing.json`, and `hand route list` shows them.

You can still open an agent in the fleet folder by hand (`cd ~/fleet-work && claude`). It then waits with `hand wait --after CURSOR`, and it stands down while a managed supervisor runs.

### After a reboot

The supervisor shows as `interrupted`. Press **Resume**, or run `hand supervisor resume`, to continue the same session with its history. It spends no tokens until you write or a wake arrives. **New session** starts fresh from `hand orient`. To resume automatically when `hand watch` starts, add `"supervisor": {"autoresume": true}` to `routing.json`.

## Upgrading from 0.7

The new Hand does not read 0.7 data. Its supervisor rebuilds what still matters, with your approval:

1. With 0.7 still installed, finish or `hand teardown` every live task.
2. Keep the old binary if you like, for example `cp "$(command -v hand)" ~/.local/bin/hand-0.7`.
3. Build the new one, either as `hand-next` to try it beside 0.7 or as `hand` to replace it.
4. In the fleet folder, remove the 0.7 wiring that would keep waking a supervisor: the Stop hook with `supervision claude-stop` in `.claude/settings.json`, and `.pi/extensions/hand-*` and `.opencode/plugins/hand-*`. `hand init` warns if they are still there.
5. Run `hand init` there with the new binary. The old `AGENTS.md` and skill are replaced. Everything else (`data/`, `state/`, `config/`, `projects/`) is left alone, and the folder gets a `secondhand-migrate` skill.
6. Open the supervisor there and ask it to migrate the fleet. It lists what it would create (projects, open tasks, queue items, decisions, routing and memory), then waits for your approval before it changes anything.
7. Afterwards it can remove the 0.7 pool worktrees and shared files, and then archive the old data into `legacy-0.7/`. Each of those steps waits for your approval. Its last step runs `hand init`, which removes the migrate skill.
8. Trust the worktrees folder once, as described above.

## License

MIT
