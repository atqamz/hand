# Fleets

A fleet is any folder `hand init` has run in. Commands find it from `--home DIR` (given before the command), from the working directory or any folder inside it, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`.

To move one on the same filesystem, run `hand fleet move --dry-run PATH`, then `hand fleet move --yes PATH`. The dry run lists every cell whose recorded home changes, the absolute and `~/` counts, and the hits the verification would still find; it writes nothing to `hand.db`, but it can create or change `hand.db-shm` and needs a writable folder. `--yes` rewrites every text column in one transaction, scans the database for the old home independently of the rewrite, refuses while any plain, `file://` or JSON `\u002f` spelling (and on Windows any case or slash variant) remains, runs `foreign_key_check` and `integrity_check`, and writes `hand.db.pre-move-<time>` beside the database first; that copy is taken after Hand migrates an older database to the current schema. Then it closes the database, moves the folder and points the registry at it. The fleet ID, refs, branch names and worktree paths do not change.

`~/<old folder>` becomes `~/<new folder>` only when the old folder is exactly `$HOME/<old folder>` and the new one is exactly `$HOME/<new folder>`; for any other destination it becomes the absolute destination. A `~/<old folder>` that is not the old home is left in place and listed for review. A path followed by `.` is read as a longer name (`fleet-hand.bak`), so a sibling backup is never rewritten.

To move to another filesystem, copy the folder (a plain `cp -a` of a stopped fleet), run `hand init` in the copy to adopt it, then run `hand fleet move --rewrite-only --yes --from OLD` in the copy, where `OLD` is the home the database records. `--rewrite-only` rewrites the recorded paths to the copy's folder, verifies them and records the copy as the home without moving the folder; a later `hand fleet move --yes PATH` then finds those paths. If the database records a home other than the folder it is in and `--from` is missing, the command refuses and names the home to pass.

The move is two steps. The rewrite commits a `fleet.moving` event before the folder is renamed; the rename then moves the folder, the registry follows, and a `fleet.moved` event records the move. If the process stops between them, `hand orient` shows the interrupted move and `hand fleet move --yes PATH` finishes it without rewriting the database or the `fleet.moved` history again. If the rename keeps failing, the printed message names the pre-move database to copy back over `hand.db` (and the `-wal` and `-shm` files to remove) to undo the rewrite. `fleet.moved` rows are history and are never rewritten by a later move.

A plain `mv` leaves every recorded path behind; `hand init` in the new place repairs worktrees of projects that were inside the old home, but it does not rewrite those paths. Run a move instead.

A fleet folder looks like this:

```text
~/fleets/demo/
├── AGENTS.md          Hand's; rewritten by every hand init
├── CLAUDE.md          Hand's; points to AGENTS.md
├── .claude/ .agents/ .grok/ .pi/
│                      the secondhand skill, for Claude Code, Codex, Grok and Pi
├── hand.db            the fleet's state
├── routing.json       routing profiles
├── memory/
│   ├── operator.md    your standing preferences
│   └── projects/      one file of lessons per project
├── inbox/             files attached on the board, and .trash/ for pruned ones
└── board.token        the board's token for this fleet
```

`hand init` rewrites `AGENTS.md`, `CLAUDE.md` and the skills every time, so put your own preferences in `memory/operator.md` instead. `hand orient` shows the supervisor up to 4000 bytes of that file on every turn.

Hand keeps shared state in `~/.secondhand`, or `$SECONDHAND_HOME`. There, `fleets/` holds a file per fleet ID that names its folder, and `worktrees/` holds every worker worktree, outside every fleet:

```text
~/.secondhand/
├── fleets/<fleet id>                  file naming the fleet folder
├── worktrees/<fleet id>/tN-aN/        one worktree per attempt
├── luvus/                             pinned Luvus copies and pin.json
├── iroh/                              the hand-iroh endpoint's key, hand-iroh.key
├── board.iroh                         the board's --iroh setting and the sidecar's last state
└── board.addr                         where the running board listens
```

Each attempt works on the branch `hand/<fleet id>/tN-aN` in the project's repository. `hand attempt clean aN` removes the worktree and a Claude worker's scratch directory, and keeps the branch. The watcher does the same by itself, 7 days after an attempt ended, when its tree is clean, its commits are pushed and no ignored file looks like config (other ignored files, such as build caches, go with the worktree: a path a worker wants to keep must be outside it); otherwise the Board shows a Needs item with the reason. `hand orient` and the Board warn when `reports/` and `shots/` pass 20 GB or hold files older than 30 days, and `hand gc --older-than 30d` lists them and deletes only with `--yes`. The `retention` object in `routing.json` sets the limits ([Retention](vocabulary.md#retention)).
