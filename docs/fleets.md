# Fleets

A fleet is any folder `hand init` has run in. Commands find it from `--home DIR` (given before the command), from the working directory or any folder inside it, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`. A move follows the folder while the name still equals the old folder's name; a name set with `--name` stays.

To move one on the same filesystem, run `hand fleet move --dry-run PATH`, then `hand fleet move --yes PATH`. The dry run lists every cell whose recorded home changes, the absolute and `~/` counts, and the hits the verification would still find; it writes nothing to `hand.db`, but it can create or change `hand.db-shm` and `hand.db-wal` and needs a writable folder. `--yes` rewrites every text column in one transaction in the plain, `file://`, `\/` and JSON `\u002f` spellings of the old home (and on Windows its slash and ASCII case variants), renames the fleet to the new folder's name in the same transaction when its name still equals the old folder's name (recording `fleet.renamed`), scans the database for the old home independently of the rewrite, refuses while a spelling it recognises remains and names each cell with the manual fix, runs `foreign_key_check` and `integrity_check`, and writes `hand.db.pre-move-<time>` beside the database first; that copy is taken after Hand migrates an older database to the current schema. Then it closes the database, moves the folder and points the registry at it. The fleet ID, refs, branch names and worktree paths do not change. The rewrite is literal and scans every text column, so prose that names the old home is rewritten too, and the column's own limits still apply. After the move, run `hand init` in the new folder to repair the worktrees of projects that were inside the old home and to refresh the skills.

`~/<old folder>` becomes `~/<new folder>` only when the old folder is exactly `$HOME/<old folder>` and the new one is exactly `$HOME/<new folder>`; for any other destination it becomes the absolute destination. A `~/<old folder>` that is not the old home is not rewritten: the dry run lists it, `--yes` refuses, and the error names each cell so you can edit it to the destination by hand. Letters, digits, `_`, `-`, `.`, `+`, spaces and non-ASCII bytes are name bytes, so a path followed by any of them is a longer name (`fleet-hand.bak`, `fleet-hand backup`) and a sibling is never rewritten; a `.` followed by whitespace or the end of the cell ends the path, and `/` and `\` separate it from what follows.

To move to another filesystem, copy the folder (a plain `cp -a` of a stopped fleet), delete or move the original out of the way, run `hand init` in the copy to adopt it (it refuses while a fleet with the same ID still sits at the old path), then run `hand fleet move --rewrite-only --yes --from OLD` in the copy, where `OLD` is the home the database records. `--rewrite-only` rewrites the recorded paths to the copy's folder, verifies them and records the copy as the home without moving the folder, and follows the copy's folder name while the fleet's name still equals the base name of `OLD`; a later `hand fleet move --yes PATH` then finds those paths. If the database records a home other than the folder it is in and `--from` is missing, the command refuses and names the home to pass; a database that records no home (it predates the column) is refused the same way, and `--from` supplies the folder its paths name.

The move is two steps. The rewrite commits a `fleet.moving` event before the folder is renamed; the rename then moves the folder, the registry follows, and a `fleet.moved` event records the move. If the process stops between them, `hand fleet move --yes PATH` finishes it without rewriting the database or the `fleet.moved` history again; in the old folder `hand orient` shows the interrupted move, and if the folder already moved, `hand init` in the new folder adopts it and `hand orient` then shows the line (before that, the registry error names the pending move and the command that finishes it). If the rename keeps failing, the printed message names the pre-move database to copy back over `hand.db` (and the `-wal` and `-shm` files to remove) to undo the rewrite, on the first run and on a resume; that copy is the database as it was before the rewrite, so stop every command that writes to the old folder first, because copying it back discards everything written since. `fleet.moved` rows are history and are never rewritten by a later move. The blocker check reads `watch.pid` and the shared `board.pid`, so a missing or stale pid file, or a board started with another `$SECONDHAND_HOME`, is not seen.

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
├── scratch/tN-aM      one scratch folder per attempt
└── board.token        the board's token for this fleet
```

`hand init` rewrites `AGENTS.md`, `CLAUDE.md` and the skills every time, so put your own preferences in `memory/operator.md` instead. `hand orient` shows the supervisor up to 4000 bytes of that file on every turn.

## Import another fleet

`hand fleet import SRC INTO DST` merges one fleet into another as an archive, not as a rewrite. `SRC` and `DST` are fleet IDs or names registered under `~/.secondhand/fleets`. `--dry-run` prints the manifest and writes no row, file or marker; SQLite may still create `hand.db-shm` and `-wal` beside a database it opens, so a read-only mount can refuse the dry run. A ref shaped like a fleet ID is read as an ID only, and an unregistered one is an error; a name resolves through the registry, which opens every registered fleet. It refuses the same fleet, either fleet with a live supervisor or a `launching`/`running` attempt, a schema that is not the current one, and a source already imported (the `imported-into` marker in the source home, or a `fleet.imported` event in the destination). An import lock is held on both the source home and the destination home from the preflight through the file phase, and the marker and event are checked again inside the database transaction, so concurrent invocations import once, whether they name one destination or two, and the others exit with a conflict.

The import:

- adds each source project whose repository the destination does not already have; a project with the same name and repository is reused, comparing the stored and source paths as absolute, symlink-resolved paths, and the same name with another repository is refused, because its tasks cannot be mapped safely;
- adds the source tasks at `t + offset`, where the offset is the destination's current highest task ID, keeping title, goal, status and both timestamps. Only `tN` references in `task.title` and `task.goal` are shifted; ref-like tokens that are part of a path, a hash or a longer word, including one that touches a Unicode letter or digit, are listed under `ambiguous` and left exactly as they were. A rewritten title over 200 characters is refused, naming the source task;
- copies `memory/`, `deliverables/`, `plans/`, `inbox/`, `reports/` and `briefs/` when present, never moving them, so the source home stays complete. A directory it creates is mode 0700. `memory/operator.md` becomes `memory/operator.<source id>.md`, and `memory/projects/<project>.md` follows the destination name of a project reused under another name; a destination path with different content is left alone and listed under `collisions`;
- exports every source report as `reports/archive-<source id>/rN-<slug>.md`, with its destination task, source task, source attempt, created time and body. Report, attempt, decision, timer, event and supervisor input rows are not imported, so `source_attempt` names an attempt that exists only in the source;
- records one `fleet.imported` event with the source ID, the counts, the offset and `text_sha256`, a hash of the inserted project rows and task rows only, not of the exported report bodies. Those rows carry the new task IDs, so the hash is bound to the offset and changes whenever the destination's highest task ID does. The source also gets an `imported-into` marker naming the destination and the same hash.

The database is one transaction. The manifest's database part (counts, offset and hash) is printed as soon as it commits, before the files are copied; the file part follows the copy, so a file failure still leaves the database manifest and the marker. If stdout fails after the commit, the marker and the files are still written and the command then exits 1. A rerun is refused. To read the imported reports, read the archive folder; to work the imported tasks, use the destination fleet.

A dry run cannot tell whether a destination is writable: it reports a missing file as copied without opening it, so a destination content directory that refuses writes still shows `copied` and exits 0, and the real run then fails part of the file phase after the commit. Before the real run, check that every content directory the destination already has (`memory/`, `deliverables/`, `plans/`, `inbox/`, `reports/`, `briefs/`) accepts a file write and a removal. The offset is the destination's highest task ID at the moment of the real run, not the one the dry run printed: a destination that gained tasks since then shifts the tasks further, and the hash moves with it, so recompute the offset then, and run the dry run twice immediately before the real run.

Hand keeps shared state in `~/.secondhand`, or `$SECONDHAND_HOME`. There, `fleets/` holds a file per fleet ID that names its folder, and `worktrees/` holds every worker worktree, outside every fleet:

```text
~/.secondhand/
├── fleets/<fleet id>                  file naming the fleet folder
├── worktrees/<fleet id>/tN-aM/        one worktree per attempt
├── luvus/                             pinned Luvus copies and pin.json
├── iroh/                              the hand-iroh endpoint's key, hand-iroh.key
├── board.iroh                         the board's --iroh setting and the sidecar's last state
└── board.addr                         where the running board listens
```

Each attempt works on the branch `hand/<fleet id>/tN-aM` in the project's repository. `hand attempt clean aN` removes the worktree, the attempt's `scratch/tN-aM` folder and a Claude worker's scratch directory, and keeps the branch. The scratch folder moves with the fleet home, but worktrees and the peer secret stay outside it until the layout change; it is outside `reports/` and `shots/`, so the artifact counts and the database backups never include it. The watcher does the same by itself, 7 days after an attempt ended, when its tree is clean, its commits are pushed and no ignored file looks like config (other ignored files, such as build caches, go with the worktree: a path a worker wants to keep must be outside it); otherwise the Board shows a Needs item with the reason. `hand orient` and the Board warn when `reports/` and `shots/` pass 20 GB or hold files older than 30 days, and `hand gc --older-than 30d` lists them and deletes only with `--yes`. The `retention` object in `routing.json` sets the limits ([Retention](vocabulary.md#retention)).
