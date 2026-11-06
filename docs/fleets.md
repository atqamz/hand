# Fleets

A fleet is any folder `hand init` has run in. Commands find it from `--home DIR` (given before the command), from the working directory or any folder inside it, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`. To move one at this head, `mv` its folder and run `hand init` in the new place: that repairs worktrees of projects that were inside the old home, but every other recorded home path is left behind. A `hand fleet move PATH` that rewrites every recorded home in one transaction, verifies the old home is gone, moves the folder and points the registry at it arrives in atqamz/hand#1143.

## Merge a fleet into another

`hand fleet merge SRC INTO DST` moves a source fleet's rows and files into a destination fleet, called the survivor. `SRC` and `DST` are fleet ids or names from `hand fleet list`. The source id, home and database stay in place: the command never deletes a home, though a real run moves the source's files out of it.

Merge is a stop-the-world operation. It refuses:

- the same fleet, nested homes, an unreadable home, or a schema that is not current (open that fleet once with this hand to migrate it);
- a running board, a live watcher in either fleet, a live supervisor in either fleet, a `launching` or `running` attempt in either fleet, or pending supervisor input in either fleet;
- an armed timer in the source, unless `--cancel-timers` cancels the imported copy and records a `timer.cancelled` event; the source database keeps its own timer;
- a project name that exists on both sides with a different `repo`, unless `--rename-project OLD=NEW` renames the source project (repeat the flag for each collision; its `memory/projects/OLD.md` moves to `NEW.md`);
- files of the same relative path with different content. `memory/operator.md` is the one exception: the destination file stays, and the source file is saved as `memory/operator.<source id>.md`;
- a source task title or timer note that would pass 200 characters after its refs are rewritten; shorten it in the source and retry;
- a source that the destination's `fleet.merged` event already names. A second merge is a non-operation: it changes nothing and reports the existing event.

`--stop` stops the source's watcher and supervisor when they are live. It never stops a worker: the operator must stop every live attempt first. Stop the board with `hand board stop` before merging, and stop the destination's watcher and supervisor yourself. Every refusal above is checked before `--stop` touches a service: file collisions, armed timers and project collisions included. `--dry-run` refuses `--stop` and never stops anything; stop the source yourself for a dry run of a live source. A dry run changes no row and moves no file, though SQLite may still touch a `hand.db-shm` while it takes the snapshots. It merges a copy of the destination, so it cannot see an unwritable destination file or folder; the real run can still stop there.

With no live writer, the command prints a TOON manifest and, unless `--dry-run` is given, changes the destination. A real run prints the manifest immediately after the database commits and again when the file phase finishes; if a post-commit step fails, the second manifest carries `post_commit_error` and the `moves` that already happened. `--dry-run` merges a copy of the destination, so it moves no file and changes no row; it prints the same fields. `text_sha256` and `db_sha256` hash sorted logical content rather than database bytes, so a dry run and a real run over the same inputs print the same values. The manifest has:

- `offsets`: the destination's current maximum id for each ref kind, added to every source id;
- `counts`: per-table source, destination and merged row counts plus the offset used;
- `refs`: refs changed by kind and refs left alone because they are part of a protected branch, worktree, report path, filename, markdown anchor, UUID or code span; `url` counts the board URLs rewritten and the URLs left as written;
- `ambiguous`: every cell where a ref could be something else: an `aN` or `dN` that looks like a hexadecimal commit prefix, or a one-digit `dN` that is part of a larger token, such as the section number `d1.2` or an ordinal. Those tokens are reported and left as written, while the rest of the cell's refs and source home paths are still rewritten. A whole-token one-digit `dN` is not ambiguous: when the source has that decision it is rewritten like any other ref;
- `text_sha256` (every text value in every table), `db_sha256` (every column of every table), `files`, `manual` and `warnings`; both hashes sort their rows, so the database's byte layout does not matter. Neither covers the moved report, brief or shot bodies; `db_sha256` also leaves out the `hand.db-shm` and `hand.db-wal` side files.

The source is read from one `VACUUM INTO` snapshot, so a concurrent writer cannot make its counts and rows disagree, and the destination is written in one `BEGIN IMMEDIATE` transaction. Source rows are inserted in foreign-key order and their text is rewritten by the offsets. A ref is rewritten only as a whole token: one next to `[A-Za-z0-9_./#-]`, `\`, `|`, `"`, `'` or `’`, inside a code span, inside a UUID, or in a branch, worktree, report, brief or shot path is left as written, because it is a path, filename, anchor or code, not a reference. A URL is rewritten only when it parses as a board URL for the source fleet that addresses a task, decision or report the source has: its fleet id and that ref are mapped and the rest of the URL, including any query and fragment, stays byte for byte. A doc URL such as `.../doc/reports/t1/report.md` and every other URL stay as written, because files are not renamed. Imported source rows have their source home paths rewritten to the destination home, including a sentence-final `~/name.`; destination rows keep their text except that a URL naming the source fleet's row is rewritten to the survivor's id and the mapped ref. Events are renumbered by `at` so the survivor's recent events stay newest, and the destination supervisor's wake cursor follows its last event.

After the database commits, the command moves these source folders into the destination, recursively: `memory/`, `reports/`, `briefs/`, `shots/`, `deliverables/`, `plans/`, `inbox/` (including `.trash/`) and `projects/`. It keeps the destination's `routing.json` and `board.token`, and reports both as manual: profiles must be combined by hand, and the token is never copied or printed. File contents are not ref-rewritten; treat old reports and briefs as historical artifacts.

`--archive-source` is optional and runs only after a real merge. It stops the source's Luvus server, writes and verifies a `merged-into` marker in the source home, then removes the registry file last, only when it still points at that home. The manifest says which of those happened; a failed marker leaves the registry in place. It does not delete the source. Keep the source home and a backup for at least a week, and check the merged fleet with `hand orient`, `hand task show` and `hand report show` before removing anything else.

The command cannot stop or transfer workers or their Luvus panes, merge `routing.json`, `board.token` or the device pairing, edit `memory/operator.md` for you, rewrite prose inside files, or rename or move the destination home. Renaming or moving the survivor after a merge is separate work: `hand fleet move PATH` will rewrite every recorded home path (the destination's own and the imported rows') in one transaction, verify the old home is gone and move the folder, and it arrives in atqamz/hand#1143. At this head, a plain `mv` or `hand init` leaves the recorded paths behind.

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
