# Hand

Hand is a personal supervisor layer for coding agents. You talk to one supervisor agent. It captures every request as a task, dispatches Claude Code or Codex workers into isolated git worktrees through [Luvus](https://github.com/RizRiyz/luvus), waits for them with zero tokens, reads their reports, and asks you only through decisions. Everything lives in a local SQLite state, and `hand board` shows it.

Linux only. The design and its non-goals are in [`docs/spec.md`](docs/spec.md).

## Requirements

- Go 1.26.5, git, and Luvus 0.14.x (`luvus` on `PATH`).
- Claude Code and/or Codex, logged in.
- Optional: `notify-send` for desktop notifications.

## Install and first run

```sh
go build -o ~/.local/bin/hand .
hand init                                      # home: $HAND_HOME, or ~/.hand
hand project add myrepo /absolute/path/to/repo
```

Claude asks once to trust a folder. Trust `<home>/worktrees` once, or answer the first attempt's prompt with `hand attempt read` and `hand attempt keys`:

```sh
w="${HAND_HOME:-$HOME/.hand}/worktrees"; mkdir -p "$w" && cd "$w" && claude
```

## Keep the watcher and the board running

```sh
mkdir -p ~/.config/systemd/user
hand unit watch > ~/.config/systemd/user/hand-watch.service
hand unit board > ~/.config/systemd/user/hand-board.service
systemctl --user daemon-reload
systemctl --user enable --now hand-watch hand-board
journalctl --user -u hand-board | grep board:   # the private board link
```

The board listens on `127.0.0.1:7777`. To reach it from a phone, use a tunnel such as `ssh -L` or `tailscale serve`.

## The supervisor

Give your agent the skill:

```sh
mkdir -p ~/.claude/skills/hand && hand skill > ~/.claude/skills/hand/SKILL.md
mkdir -p ~/.codex/skills/hand && hand skill > ~/.codex/skills/hand/SKILL.md
```

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
