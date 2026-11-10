# Fleets

A fleet is any folder `hand init` has run in. Commands find it from `--home DIR` (given before the command), from the working directory or any folder inside it, or from `$HAND_HOME`. Run as many fleets as you like: each has its own Luvus session, worktrees and watcher, and one board serves them all. `hand fleet list` shows them.

Rename a fleet with `hand init --name NEW`. A move follows the folder while the name still equals the old folder's base name (on Windows, ignoring case); a name that differs from it, including one set with `--name`, stays.

To move one on the same filesystem, run `hand fleet move --dry-run PATH`, then `hand fleet move --yes PATH`. The dry run lists every cell whose recorded home changes, the absolute and `~/` counts, and the hits the verification would still find; it writes nothing to `hand.db`, but it can create or change `hand.db-shm` and `hand.db-wal` and needs a writable folder. `--yes` rewrites every text column in one transaction in the plain, `file://`, `\/` and JSON `\u002f` spellings of the old home (and on Windows its slash and ASCII case variants), renames the fleet to the new folder's name in the same transaction when its name still equals the old folder's base name (on Windows, ignoring case; recording `fleet.renamed`), scans the database for the old home independently of the rewrite, refuses while a spelling it recognises remains and names each cell with the manual fix, runs `foreign_key_check` and `integrity_check`, and writes `hand.db.pre-move-<time>` beside the database first; that copy is taken after Hand migrates an older database to the current schema. Then it closes the database, moves the folder and points the registry at it. The fleet ID, refs, branch names and worktree paths do not change. The rewrite is literal and scans every text column, so prose that names the old home is rewritten too, and the column's own limits still apply. A destination whose folder name cannot be a fleet name (over 64 bytes, or control or invalid UTF-8 bytes) refuses the move, as `hand init` does. After the folder moves, Hand rewrites the `gitdir:` prefix of each worktree gitfile under `~/.secondhand/worktrees/<fleet id>/` that names the old home and leaves the rest of the file byte-identical; it reports how many it rewrote, how many were already correct and which worktrees it left broken (one with no `.git` file, or a target that does not exist after the rewrite), and the move fails when one is left broken. The dry run reports the same counts and writes no gitfile. After the move, run `hand init` in the new folder to repair the worktrees of projects that were inside the old home and to refresh the skills.

`~/<old folder>` becomes `~/<new folder>` only when the old folder is exactly `$HOME/<old folder>` and the new one is exactly `$HOME/<new folder>`; for any other destination it becomes the absolute destination. A `~/<old folder>` that is not the old home is not rewritten: the dry run lists it, `--yes` refuses, and the error names each cell so you can edit it to the destination by hand. Letters, digits, `_`, `-`, `.`, `+`, spaces and non-ASCII bytes are name bytes, so a path followed by any of them is a longer name (`fleet-hand.bak`, `fleet-hand backup`) and a sibling is never rewritten; a `.` followed by whitespace or the end of the cell ends the path, and `/` and `\` separate it from what follows.

To move to another filesystem, copy the folder (a plain `cp -a` of a stopped fleet), delete or move the original out of the way, and run any `hand` command in the copy: it [adopts the copy](#a-fleet-on-a-removable-disk) by itself. `hand fleet move --rewrite-only --yes --from OLD` still does the same rewrite for one named home and also follows the copy's folder name while the fleet's name still equals the base name of `OLD` (on Windows, ignoring case). If the database records a home other than the folder it is in and `--from` is missing, `fleet move` refuses and names the home to pass; a database that records no home (it predates the column) is refused the same way, and `--from` supplies the folder its paths name.

The move is two steps. The rewrite commits a `fleet.moving` event before the folder is renamed; the rename then moves the folder, the registry follows, and a `fleet.moved` event records the move. If the process stops between them, `hand fleet move --yes PATH` finishes it without rewriting the database or the `fleet.moved` history again; in the old folder `hand orient` shows the interrupted move, and if the folder already moved, `hand init` in the new folder adopts it and `hand orient` then shows the line (before that, the registry error names the pending move and the command that finishes it). If the rename keeps failing, the printed message names the pre-move database to copy back over `hand.db` (and the `-wal` and `-shm` files to remove) to undo the rewrite, on the first run and on a resume; that copy is the database as it was before the rewrite, so stop every command that writes to the old folder first, because copying it back discards everything written since. `fleet.moved` rows are history and are never rewritten by a later move. The blocker check reads `watch.pid` and the shared `board.pid`, so a missing or stale pid file, or a board started with another `$SECONDHAND_HOME`, is not seen.

A plain `mv` leaves every recorded path behind until the first `hand` command in the new place, which adopts it; `hand init` there repairs worktrees of projects that were inside the old home, and a move through `hand fleet move` does both in one step.

A fleet folder looks like this:

```text
~/fleets/demo/
├── AGENTS.md          Hand's; rewritten by every hand init
├── CLAUDE.md          Hand's; points to AGENTS.md
├── .claude/ .agents/ .grok/ .pi/
│                      the secondhand skill, for Claude Code, Codex, Grok and Pi
├── hand.db            the fleet's state
├── fleet.home         the homes this fleet has lived at
├── .fleet.home.lock   serialises writes to fleet.home
├── fleet.host         which host has the fleet open; only while a watcher runs
├── routing.json       fleet settings
├── memory/
│   ├── operator.md    your standing preferences
│   └── projects/      one file of lessons per project
├── inbox/             files attached on the board, and .trash/ for pruned ones
├── scratch/tN-aM      one scratch folder per attempt
└── board.token        the board's token for this fleet
```

`hand init` rewrites `AGENTS.md`, `CLAUDE.md` and the skills every time, so put your own preferences in `memory/operator.md` instead. `hand orient` shows the supervisor up to 4000 bytes of that file at the start of a session and after a resume.

## Import another fleet

`hand fleet import SRC INTO DST` merges one fleet into another as an archive, not as a rewrite. `SRC` and `DST` are fleet IDs or names registered under `~/.secondhand/index`. `--dry-run` prints the manifest and writes no row, file or marker; SQLite may still create `hand.db-shm` and `-wal` beside a database it opens, so a read-only mount can refuse the dry run. A ref shaped like a fleet ID is read as an ID only, and an unregistered one is an error; a name resolves through the registry, which opens every registered fleet. It refuses the same fleet, either fleet with a live supervisor or a `launching`/`running` attempt, a schema that is not the current one, and a source already imported (the `imported-into` marker in the source home, or a `fleet.imported` event in the destination). An import lock is held on both the source home and the destination home from the preflight through the file phase, and the marker and event are checked again inside the database transaction, so concurrent invocations import once, whether they name one destination or two, and the others exit with a conflict.

The import:

- adds each source project whose repository the destination does not already have; a project with the same name and repository is reused, comparing the stored and source paths as absolute, symlink-resolved paths, and the same name with another repository is refused, because its tasks cannot be mapped safely;
- adds the source tasks at `t + offset`, where the offset is the destination's current highest task ID, keeping title, goal, status and both timestamps. Only `tN` references in `task.title` and `task.goal` are shifted; ref-like tokens that are part of a path, a hash or a longer word, including one that touches a Unicode letter or digit, are listed under `ambiguous` and left exactly as they were. A rewritten title over 200 characters is refused, naming the source task;
- copies `memory/`, `deliverables/`, `plans/`, `inbox/`, `reports/` and `briefs/` when present, never moving them, so the source home stays complete. A directory it creates is mode 0700. `memory/operator.md` becomes `memory/operator.<source id>.md`, and `memory/projects/<project>.md` follows the destination name of a project reused under another name; a destination path with different content is left alone and listed under `collisions`;
- exports every source report as `reports/archive-<source id>/rN-<slug>.md`, with its destination task, source task, source attempt, created time and body. Report, attempt, decision, timer, event and supervisor input rows are not imported, so `source_attempt` names an attempt that exists only in the source;
- records one `fleet.imported` event with the source ID, the counts, the offset and `text_sha256`, a hash of the inserted project rows and task rows only, not of the exported report bodies. Those rows carry the new task IDs, so the hash is bound to the offset and changes whenever the destination's highest task ID does. The source also gets an `imported-into` marker naming the destination and the same hash.

The database is one transaction. The manifest's database part (counts, offset and hash) is printed as soon as it commits, before the files are copied; the file part follows the copy, so a file failure still leaves the database manifest and the marker. If stdout fails after the commit, the marker and the files are still written and the command then exits 1. A rerun is refused. To read the imported reports, read the archive folder; to work the imported tasks, use the destination fleet.

A dry run cannot tell whether a destination is writable: it reports a missing file as copied without opening it, so a destination content directory that refuses writes still shows `copied` and exits 0, and the real run then fails part of the file phase after the commit. Before the real run, check that every content directory the destination already has (`memory/`, `deliverables/`, `plans/`, `inbox/`, `reports/`, `briefs/`) accepts a file write and a removal. The offset is the destination's highest task ID at the moment of the real run, not the one the dry run printed: a destination that gained tasks since then shifts the tasks further, and the hash moves with it, so recompute the offset then, and run the dry run twice immediately before the real run.

Hand keeps shared state in `~/.secondhand`, or `$SECONDHAND_HOME`. There, `index/` holds a bookmark per fleet ID that names its folder, and `worktrees/` holds every worker worktree, outside every fleet. A bookmark is written to a temporary file and renamed into place, so the registry works on a filesystem without hard links, such as exFAT:

```text
~/.secondhand/
├── index/<fleet id>                   file naming the fleet folder
├── index.lock                         serialises bookmark and record writes
├── worktrees/<fleet id>/tN-aM/        one worktree per attempt
├── luvus/                             Luvus copies of an older hand, read until the fleet has its own
├── models/                            the model lists' freshness gate, shared by every fleet
├── backups/hand.<stamp>               the old hand binaries that `hand update` keeps
├── iroh/                              the hand-iroh endpoint's key, hand-iroh.key
├── board.iroh                         the board's --iroh setting and the sidecar's last state
├── board.generation                   the running board's PID and start marker
├── board.generation.lock              serialises writes to board.generation
└── board.addr                         where the running board listens
```

What belongs to the fleet lives in its folder, so the folder can move to another mount or machine:

```text
<fleet home>/
├── luvus/<os>-<arch>/pin.json         the pin, with a path relative to this folder
├── luvus/<os>-<arch>/<version>-<sha8>/luvus   the pinned copy that runs
├── backups/                           hand.db.<stamp> from `hand update`, hand-YYYYMMDD.db from the watcher
└── cache/models/                      the model lists this fleet last read
```

The machine keeps worktrees, the index, the pid and address files, the Board's peer secret and iroh key, and the old `hand` binaries, because they name this machine's processes or paths, or belong to a Board that serves every fleet on it. `hand doctor` prints that list in its `machine-local` row. The first command in a fleet, `hand init` and `hand update` copy a pre-existing `luvus/`, `backups/<fleet id>/` and `models/` of `~/.secondhand` into the fleet, print what they copied, and leave the originals: a Luvus server that already runs from the machine copy keeps its binary until Hand's own quiet switch restarts it, and an older `hand` that you roll back to still finds its backups there. The pinned copy runs from the fleet's disk. exFAT and FAT have no execute bit, and Linux and macOS show every file there as executable unless the volume is mounted `noexec`, so Hand ignores a `chmod` that the filesystem refuses and trusts the check it makes anyway: it runs `luvus --version` on the copy before it pins it. A `noexec` volume fails that check, the machine pin of an older `hand` stays in use if there is one, and the fix is to mount the volume with `exec`.

Each fleet home also records itself in `fleet.home`, a JSON object such as `{"homes":["/old/a","/new"],"current":"/new"}`: `current` is the home the fleet is in, and `homes` keeps every earlier one, so a moved folder and a copy of it can be told apart. The first command in a fleet writes the record, `hand init` updates it (a fleet with no record yet that moved by a plain rename keeps the bookmark's old home in `homes`), `hand fleet move` writes it before the move or `--rewrite-only` when it is missing, and a move appends the old home. An empty or torn record is replaced on the next write rather than refused, and `.fleet.home.lock` beside it serialises writes from commands that use different `SECONDHAND_HOME` roots.

`hand doctor`'s `modes` row reads the permission bits of a file it creates, so a network share that reports 0600 without enforcing it (a CIFS `noperm` mount) can pass it. A fleet belongs on a local disk, not on a network share.

Each attempt works on the branch `hand/<fleet id>/tN-aM` in the project's repository. `hand attempt clean aN` removes the worktree, the attempt's `scratch/tN-aM` folder and a Claude worker's scratch directory, and keeps the branch. The scratch folder moves with the fleet home, but worktrees and the peer secret stay on the machine; it is outside `reports/` and `shots/`, so the artifact counts and the database backups never include it. The watcher does the same by itself, 7 days after an attempt ended, when its tree is clean, its commits are pushed and no ignored file looks like config (other ignored files, such as build caches, go with the worktree: a path a worker wants to keep must be outside it); otherwise the Board shows a Needs item with the reason. `hand orient` and the Board warn when `reports/` and `shots/` pass 20 GB or hold files older than 30 days, and `hand gc --older-than 30d` lists them and deletes only with `--yes`. The `retention` object in `routing.json` sets the limits ([Retention](vocabulary.md#retention)).

## A fleet on a removable disk

A fleet home is the durable state, so it can live on an SSD or a stick and follow you between machines: plug the disk in, `cd` into the fleet, and run any `hand` command.

```text
cd /run/media/me/DISK/fleet
hand fleet adopt --check     # optional: count what the first command will rewrite
hand orient                  # adopts the fleet at this path, then runs
```

The first command that opens the fleet compares the home recorded in `fleet.home` and in the `fleet` row with the folder it runs in. When they differ and no old home still holds a `hand.db` of this fleet, it adopts the fleet: one transaction rewrites every occurrence of every known old home, literally and in the same spellings as a move, in every text column (`attempt.argv`, `report.body`, `supervisor_input.body`, `event.detail`, `decision.question`, `supervisor.argv` and any other), sets the `fleet` row's home, checks foreign keys and integrity, and appends a `fleet.adopted` event naming the old homes, the cell count and the occurrence count; then the record and the bookmark in `~/.secondhand/index` follow. A copy of the database is written to `backups/pre-adopt-<time>` first. It stays only when the adoption changed something; it is removed when nothing changed and when the rewrite failed, so a failing fleet does not collect one copy per command (it also stays when the record or the bookmark could not be updated after the commit). `fleet.adopted` rows are history and are never rewritten, and a supervisor session recorded under an old home starts fresh at the next `hand supervisor resume`, as after a move. A `~/<old folder>` that the rewrite cannot attribute to the old home is left as it is, not refused. The adoption does not rename the fleet.

It keeps a move's boundary rule on purpose: a path followed by a space or a name byte (`/old/fleet backup`, `/old/fleet-x`, `/old/fleet.bak`) is a longer name, not the fleet, so such a cell keeps the old path and the verification does not see it; edit it by hand if it names the fleet. A rewrite that a column cannot hold (a title past 200 characters, a unique value) fails with the table, column and ref of the cell and changes nothing; edit that cell, then run again. `--check` rehearses the whole rewrite on a scratch copy of the database and reports the same failure.

A home recorded on another system is rewritten too: a `C:\Users\me\fleet` home adopted on Linux or macOS matches its backslash, escaped, slash and `file://` spellings with the drive letter in either case, and the separators after the old home inside the matched path become `/`. A Linux or macOS home adopted on Windows keeps the `/` after the new home, which Windows accepts. A path under `$HOME` of the machine that wrote it (`~/fleet`) is left alone unless the old home is a direct child of this machine's `$HOME`.

`hand fleet adopt [--from OLD]... [--check]` does the same by hand. `--from` adds a home the record does not know, such as the home of a fleet whose database predates the record; repeat it for several. `--check` counts cells and occurrences per column, lists what would block the adoption, and writes nothing to `hand.db`. A run with nothing to rewrite prints `adopt: current` and writes nothing, so a second run is a no-op. `hand doctor`, `hand fleet list` and `--check` never rewrite.

The adoption refuses, and changes nothing:

- while an old home still holds a `hand.db` with this fleet's ID: the disk is mounted twice or the fleet was copied, so delete one of the two homes ("two homes"). Another fleet's database at an old path, a file that is not a Hand database and a folder this user cannot enter do not count; a `hand.db` Hand cannot read is named in the refusal;
- while the watcher (it holds `watch.lock`), the supervisor (a live row whose process still runs) or the board (it runs and the index names this fleet) runs on this machine: stop it with `hand watch stop`, `hand supervisor stop` or `hand board stop`. `hand supervisor stop` and `hand supervisor interrupt` never adopt, so they work in a moved fleet; they still refuse when an old home holds a copy. Attempts do not block it;
- while the new home is inside an old home (a fleet at a disk's root moved into a folder of that disk): the rewritten paths would still name the old home, so move the fleet out of it;
- while a `hand fleet move` is pending: finish it with `hand fleet move --yes TO`.

What travels and what stays:

- The fleet folder is durable: `hand.db`, `fleet.home`, `memory/`, `inbox/`, `plans/`, `deliverables/`, `reports/`, `shots/`, `briefs/`, `scratch/`, `projects/`, `luvus/`, `backups/`, `cache/`, `routing.json`, `board.token`, `AGENTS.md` and the skills.
- The machine keeps what names its processes and paths, and rebuilds it: worktrees (`~/.secondhand/worktrees`), pid and address files, the Board's peer secret and iroh key, the `hand` binaries, the harness transcripts. Uncommitted work in a worktree stays on the machine that made it, so stop and clean attempts, and push their branches, before you unplug.

Limits:

- exFAT and FAT have no POSIX modes, no hard links and no symlinks. Hand ignores a refused `chmod`, and `hand doctor`'s `modes` row warns that `board.token` and `hand.db` are readable by anyone who can mount the disk. Repositories under `projects/` lose their executable bits and cannot hold two paths that differ only in case. ext4, btrfs and APFS volumes work and enforce modes, but only on systems that read them.
- Encryption belongs to the volume: LUKS on Linux, FileVault or an encrypted APFS volume on macOS, BitLocker on Windows, or VeraCrypt to read one volume on all three. A lost unencrypted disk is a lost `board.token`, report history and every path in `hand.db`.
- One machine at a time. Do not put a fleet on a network share (SQLite's WAL needs coherent shared memory), do not open it on two machines at once, and close the watcher, board and supervisor before unplugging. [Hand detects a second host](#which-host-has-the-fleet-open) only through the marker below, which a disk cannot update once it is unplugged.

### Which host has the fleet open

The watcher writes `fleet.host` in the fleet folder when it starts and rewrites it every 15 seconds: the host name, the boot id of the running system, the watcher's pid and the time. The file is written to a temporary name and renamed, so it works on exFAT. A clean stop removes it. A fleet with no watcher has no marker, and `hand doctor`, `hand fleet list` and `hand fleet adopt --check` never write it.

The boot id is `/proc/sys/kernel/random/boot_id` on Linux, `kern.bootsessionuuid` on macOS, and on Windows `ticks:<unix second of the boot>`, computed from `GetTickCount64`; two Windows ids are the same boot when they are within 5 seconds. The Windows form moves with the wall clock, so only `fleet.host` uses it, never a pid file.

Every `hand` command that opens the fleet reads the marker and decides:

- **This machine**: the boot id is the same (whatever the host name, which a laptop's network can change), or the host name is the same and the boot id is too. Nothing happens, however old the marker is. A busy watcher that misses beats, a clock that jumps and a laptop that suspends all end here, so the three fleets of one machine are never locked out and no attempt is interrupted.
- **This host, an earlier boot** (a reboot or a crash): the old watcher is gone, unless a process holds `watch.lock`, which means a watcher is running now and the boot id read differently (a clock change under the Windows form). With the lock free, the command takes over.
- **Another host, last beat 90 seconds ago or less**: the command refuses, naming the host, its watcher's pid and the beat, and `hand fleet unlock`, and writes nothing. A marker dated in the future (the other clock runs ahead) counts as fresh.
- **Another host, older than 90 seconds**: the command takes over.

Taking over removes the marker and, in one transaction, writes a `fleet.took_over` event and marks the live attempts ended, reason `the fleet moved to host X`: a running one `interrupted` (`attempt.interrupted`), a launching one `failed` (`attempt.failed`), except an attempt whose process still runs on this machine. The command says so on stderr. `hand supervisor stop` and `hand supervisor interrupt` refuse like the rest, also on a copy that is not adopted yet, but never take over. A refusal is judged before an adoption of a moved or copied fleet, so it writes nothing: the database, its events and backups stay as they were. `hand init` judges the marker the same way and refuses while another host has the fleet open. The worktrees and processes of those attempts stay on the host that made them.

A `fleet.host` that cannot be read (a permission error) is warned about and counts as absent. `hand fleet unlock` clears the marker, readable or not, writes a `fleet.unlocked` event and leaves the attempts as they are. It refuses while this machine's watcher holds `watch.lock`: run `hand watch stop` first.

After a suspend or a clock jump on the same machine, nothing changes: the next beat, at most 15 seconds after the process runs again, rewrites the marker, and no command in between refuses. On another host, a suspended laptop stops beating, so after 90 seconds its fleet may be taken over; when it wakes, its watcher reads a marker that names another host at its next beat, stops with exit code 3 (`another host took this fleet over`) and leaves the new owner's marker alone. That holds once the new owner's watcher has written its marker; a takeover by another command leaves no marker, so an old owner still beating writes its own again. Two hosts really open at once is a non-goal. The staleness limit compares the other host's clock with this one's: if the other clock runs more than 90 seconds behind, a live fleet reads as stale, so keep the clocks of the hosts you use within a minute (any network time sync does) and close the watcher before you unplug.

`hand doctor` has two rows, shown only when they apply: `open on another host` (`fail`: a marker from another host within 90 seconds) and `unclean close` (`warn`: an older marker from another host, or one of this host whose watcher is not running, a crash or a reboot).

On Linux and macOS the pid files carry the boot too: `watch.pid` and `board.pid` keep their line `<pid> <start time> <binary>` and add a second, `boot <boot id> <host>`. `hand watch stop`, `hand board stop` and `hand update` signal a process only when its start time, binary and boot id match this machine's, so a pid recorded on another host or before a reboot is never signalled. `watch stop` and `board stop` remove such a file when it names this host (an earlier boot, the process is gone) and leave it when it names another host, whose watcher may be running. Windows writes no boot id there: its start times are absolute. A file from an older build has none and is checked by start time and binary as before. An older `hand` reads the second line as part of the binary path, finds another binary and leaves the process alone, so rolling back with a watcher running is safe.
