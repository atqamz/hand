# Hand

Hand is a personal supervisor layer for coding agents. You talk to one supervisor agent. It captures every request as a task, dispatches Claude Code or Codex workers into isolated git worktrees through [Luvus](https://github.com/RizRiyz/luvus), waits for them with zero tokens, reads their reports, and asks you only through decisions. Each fleet is a folder with its own SQLite state, and `hand board` shows it.

Linux only. The design and its non-goals are in [`docs/spec.md`](docs/spec.md).

## Requirements

- Go 1.26.5, git, and Luvus 0.14.x (`luvus` on `PATH`).
- Claude Code and/or Codex, logged in.
- Optional: `notify-send` for desktop notifications.

## Install and make a fleet

```sh
go build -o ~/.local/bin/hand .
mkdir -p ~/fleet-work && cd ~/fleet-work
hand init                                      # or: hand init --name work DIR
hand project add myrepo /absolute/path/to/repo
```

A fleet is any folder `hand init` has run in. Commands find it from the working directory or any folder inside it, from `--home DIR`, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees, watcher and board. `hand fleet list` shows them all.

Rename a fleet with `hand init --name NEW`. To move one, `mv` the folder and run `hand init` in its new place.

Hand keeps shared state in `~/.secondhand`, or `$SECONDHAND_HOME`. There, `fleets/` links each fleet ID to its folder, and `worktrees/` holds every worker worktree, outside every fleet.

Claude asks once to trust a folder. Trust the worktrees folder once for every fleet:

```sh
w="${SECONDHAND_HOME:-$HOME/.secondhand}/worktrees"; mkdir -p "$w" && cd "$w" && claude
```

## Keep the watcher and the board running

From inside the fleet folder:

```sh
mkdir -p ~/.config/systemd/user
hand unit watch > ~/.config/systemd/user/secondhand-watch-work.service
hand unit board > ~/.config/systemd/user/secondhand-board-work.service
systemctl --user daemon-reload
systemctl --user enable --now secondhand-watch-work secondhand-board-work
journalctl --user -u secondhand-board-work | grep board:   # the private board link
```

The board listens on `127.0.0.1:7777`. Give a second fleet its own port with `hand unit --addr 127.0.0.1:7778 board`. To reach a board from a phone, use a tunnel such as `ssh -L` or `tailscale serve`. After you move a fleet, generate its units again.

## The supervisor

Open your agent in the fleet folder:

```sh
cd ~/fleet-work && claude      # or codex
```

`hand init` writes the folder's `AGENTS.md` and `CLAUDE.md`, which make the agent run `hand orient` every turn. It also installs the `secondhand` skill for Claude Code, Codex, Grok and Pi. Every `hand init` rewrites these files, so put your own preferences in `memory/operator.md` instead.

The loop it follows:
1. `hand orient`
2. capture with `hand task add`
3. dispatch with `hand attempt start --profile …`
4. wait with `hand wait --after CURSOR`
5. read and ack reports
6. ask with `hand decision ask`
7. finish with `hand task done`

Routing profiles live in `<home>/routing.json`, and `hand route list` shows them.

## License

MIT
