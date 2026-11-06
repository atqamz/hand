# Fleets

A fleet is any folder `hand init` has run in. Commands find it from `--home DIR` (given before the command), from the working directory or any folder inside it, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`. To move one, `mv` the folder and run `hand init` in its new place.

## Merge a fleet into another

`hand fleet merge SRC INTO DST` moves a source fleet's rows and files into a destination fleet, called the survivor. `SRC` and `DST` are fleet ids or names from `hand fleet list`. The source id, home and database stay in place: the command never deletes a home.

Merge is a stop-the-world operation. It refuses:

- the same fleet, nested homes, an unreadable home, or a schema that is not current (open that fleet once with this hand to migrate it);
- a running board, a live watcher in either fleet, a live supervisor in either fleet, a `launching` or `running` attempt in either fleet, or pending supervisor input in either fleet;
- an armed timer in the source, unless `--cancel-timers` cancels it and records a `timer.cancelled` event;
- a project name that exists on both sides with a different `repo`, unless `--rename-project OLD=NEW` renames the source project (repeat the flag for each collision);
- files of the same relative path with different content. `memory/operator.md` is the one exception: the destination file stays, and the source file is saved as `memory/operator.<source id>.md`;
- a source that the destination's `fleet.merged` event already names. A second merge is a non-operation: it changes nothing and reports the existing event.

`--stop` stops the source's watcher and supervisor when they are live. It never stops a worker: the operator must stop every live attempt first. Stop the board with `hand board stop` before merging, and stop the destination's watcher and supervisor yourself. Every refusal above is checked before `--stop` touches a service: file collisions, armed timers and project collisions included. `--dry-run` refuses `--stop` and never stops anything; stop the source yourself for a dry run of a live source.

With no live writer, the command prints a TOON manifest and, unless `--dry-run` is given, changes the destination. A real run prints the manifest immediately after the database commits and again when the file phase finishes; if a post-commit step fails, the second manifest carries `post_commit_error` and the `moves` that already happened. `--dry-run` writes no fleet database and moves no file; it prints the same manifest fields, including a `db_sha256` and `text_sha256` that are identical on two runs over the same inputs. The manifest has:

- `offsets`: the destination's current maximum id for each ref kind, added to every source id;
- `counts`: per-table source, destination and merged row counts plus the offset used;
- `refs`: refs changed by kind and refs left alone inside protected branch, worktree and report paths;
- `ambiguous`: every cell where an `aN` or `dN` could be a hexadecimal commit prefix; those cells are reported and not rewritten;
- `text_sha256`, `db_sha256`, `files`, `manual` and `warnings`.

The source is read from one `VACUUM INTO` snapshot, so a concurrent writer cannot make its counts and rows disagree, and the destination is written in one `BEGIN IMMEDIATE` transaction. Source rows are inserted in foreign-key order and their text is rewritten by the offsets. Refs inside `hand/<fleet id>/tN-aM`, `worktrees/<fleet id>/tN-aM`, bare `tN-aM`, `reports/tN`, `briefs/tN` and `shots/tN` are protected, because they are branch, worktree and file names that stay valid as history. A URL to another fleet's row is rewritten to the survivor's fleet id and the mapped ref. Source home paths in database text point at the destination home. Events are renumbered by `at` so the survivor's recent events stay newest, and the destination supervisor's wake cursor follows its event. A cancelled timer's event is sequenced after every source event, so `--cancel-timers` cannot collide with the source's first sequence. A `project_grant` that both sides hold is kept once, with the earliest `created_at`. Source supervisor and device rows are dropped; the survivor keeps its own. One `fleet.merged` event records the source id, name, counts and offsets. The transaction commits only if `PRAGMA foreign_key_check` is empty and `PRAGMA integrity_check` is `ok`; otherwise nothing is written.

After the database commits, the command moves these source folders into the destination, recursively: `memory/`, `reports/`, `briefs/`, `shots/`, `deliverables/`, `plans/`, `inbox/` (including `.trash/`) and `projects/`. It keeps the destination's `routing.json` and `board.token`, and reports both as manual: profiles must be combined by hand, and the token is never copied or printed. File contents are not ref-rewritten; treat old reports and briefs as historical artifacts.

`--archive-source` is optional and runs only after a real merge. It stops the source's Luvus server, writes and verifies a `merged-into` marker in the source home, then removes the registry file last, only when it still points at that home. The manifest says which of those happened; a failed marker leaves the registry in place. It does not delete the source. Keep the source home and a backup for at least a week, and check the merged fleet with `hand orient`, `hand task show` and `hand report show` before removing anything else.

The command cannot stop or transfer workers or their Luvus panes, merge `routing.json`, `board.token` or the device pairing, edit `memory/operator.md` for you, rewrite prose inside files, or rename or move the destination home. It also cannot know a destination home's future path: moving the survivor afterwards and running `hand init` repairs project and worktree paths, but database text keeps the path it had at merge time.

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
└── board.addr                         where the running board listens
```

Each attempt works on the branch `hand/<fleet id>/tN-aN` in the project's repository. `hand attempt clean aN` removes the worktree and a Claude worker's scratch directory, and keeps the branch. The watcher does the same by itself, 7 days after an attempt ended, when its tree is clean, its commits are pushed and no ignored file looks like config (other ignored files, such as build caches, go with the worktree: a path a worker wants to keep must be outside it); otherwise the Board shows a Needs item with the reason. `hand orient` and the Board warn when `reports/` and `shots/` pass 20 GB or hold files older than 30 days, and `hand gc --older-than 30d` lists them and deletes only with `--yes`. The `retention` object in `routing.json` sets the limits ([Retention](vocabulary.md#retention)).
