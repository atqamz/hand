# Hand vocabulary

This page defines the terms of Hand as its code implements them. The design and its reasons are in [`spec.md`](spec.md). What a command accepts and prints is owned by the command itself and its tests.

## People and agents

### Operator

The one person Hand is built for. The operator owns intent and every irreversible choice: they talk to the supervisor, answer decisions, and approve what the supervisor proposes. Their standing preferences live in the operator memory file.

### Supervisor

The single agent the operator talks to in a fleet. It captures each request as a task, writes plans and briefs, starts attempts, reads reports, and asks the operator through decisions. It does not write the code itself; workers do.

Normally Hand runs it: the managed supervisor lives in the background in the fleet's Luvus session, started from the fleet home in full-auto mode, and the operator reaches it from the board. Each launch gets a ref `sN`, and a new session's launch message begins `You are supervisor sN of the Hand fleet NAME`; a resumed one gets no launch message, so `hand orient` prints `you: sN` for the running supervisor's own pane (from `$LUVUS_PANE_ID`) and `you: not the supervisor` for any other pane. It prints no `you:` line when the variable is unset. A session that sees a running supervisor and no `you: sN` line is not the supervisor. At most one supervisor is live at a time.

Resuming continues the same harness session under a new `sN`. `hand orient` names the current `sN` and adds the first one of that session, as in `s6 claude running (resumes s1)`. `hand supervisor show` names the current one and adds a `resumes:` line with the first. After the relaunch settles, Hand types one message into the pane: `[hand v1 resume] you are now s6 (resumes s1)`.

A supervisor has the same six states as an attempt. It is `stopped` after `hand supervisor stop` or a model switch, and `interrupted` after a reboot or a Luvus server restart.

An agent the operator opens by hand in the fleet folder is not the managed supervisor. It waits with `hand wait` instead of receiving wakes, and it stands down while a managed supervisor runs.

### Worker

The harness process that runs one attempt. It works in the attempt's worktree, never in the fleet home, and it sends its result back with `hand report add` from inside that worktree. Hand finds the attempt from the worktree's path.

### Harness

The agent CLI that Hand launches as a supervisor or a worker. There are four: `claude` (Claude Code), `codex` (Codex), `opencode` (opencode) and `agy` (Antigravity). Each can run the supervisor or a worker, in full-auto mode:

| Harness | Launch flags | Model and effort |
|---|---|---|
| `claude` | `--dangerously-skip-permissions` | an alias (`opus`, `sonnet`, `haiku`, `fable`) or a `claude-*` name; effort `low`, `medium`, `high`, `xhigh` or `max` |
| `codex` | `--dangerously-bypass-approvals-and-sandbox --disable hooks` | checked against Codex's model cache, `$CODEX_HOME/models_cache.json` |
| `opencode` | `--standalone --auto` | none; opencode uses the model from its own configuration |
| `agy` | `--dangerously-skip-permissions`, with `-i BRIEF` for a new session or `--conversation ID` to resume | a model id from `agy models`, which names the effort level (`gemini-3.8-flash-low`); no effort |

`--disable hooks` turns off every Codex hook in managed sessions, including hooks the user set up on purpose, so anyone relying on hooks for auditing or policy checks loses them.

### Agent state

Luvus's reading of the agent in a pane: `idle`, `working`, `done` or `blocked`. Hand's messages go through Luvus's fenced input, which refuses them while the agent is not ready for input, for example at a permission prompt. For a worker, the watcher turns `blocked` into the `attempt.blocked` wake, and `done`, or `idle` after the worker worked or blocked, into `attempt.quiet`. When the attempt's newest event is already `attempt.quiet` or `attempt.idle`, or is a `done` or `stuck` report, the supervisor already knows, so the watcher records that turn as `attempt.idle` instead. A turn that ends after a `progress` report stays `attempt.quiet`: it is the likely false done. Declining a permission prompt, or Claude Code's own two-minute auto-deny, ends the worker's turn this way.

A worker that hits its harness's usage limit does not exit: it goes idle at its prompt. When a turn ends with a limit line on the screen, the watcher records `attempt.limited` with that line instead of `attempt.quiet`, unless that line, without the time Hand adds, is the one the attempt's latest `attempt.limited` recorded. That line then belongs to an earlier turn. A turn end whose screen Hand cannot read is retried when the watcher reconnects, and is not recorded as quiet. `attempt.limited` always wakes the supervisor and is never recorded as `attempt.idle`, even right after a `done` report, because the limit is new. A turn that ends after it is `attempt.quiet`. The lines are claude's `You've hit your … limit · resets …`, codex's `You've hit your usage limit … try again at …`, and agy's `Individual quota reached … Resets in DURATION`, to which Hand adds the reset time in UTC. opencode has none yet. Claude Code continues by itself at the reset.

A turn that stays `working` for 60 minutes without a break records one `attempt.long`, with the detail `working for 60m`. The watcher checks on its reconcile tick, reads no screen, and records it once per turn: the next `working` to `idle` or `blocked` change ends the turn, and a later turn can record it again. A restarted watcher rebuilds the turn's start from the attempt's events: the `attempt.running`, or the first `attempt.sent` or `attempt.keys` after the last `attempt.blocked`, `attempt.quiet`, `attempt.idle` or `attempt.limited`. It neither restarts the 60 minutes nor records a second `attempt.long` for that turn. A turn that began with no such event, because the worker resumed working on its own, counts from the restart. `attempt.long` always wakes the supervisor and is never recorded as `attempt.idle`.

## Fleets and files

### Hand

The CLI, one Go binary. It owns state, worktrees, the watcher and the board. It calls itself by its binary's name, so a build installed as `hand-next` writes `hand-next` into the fleet's `AGENTS.md`, its skill, its help lines and its errors.

### Channel

Where a `hand` binary comes from. `hand version` prints it next to the version and the commit:
- `source`: a local `go build`;
- `edge`: the rolling build of `main` that passed CI, published as the `edge` pre-release;
- `stable`: the binaries attached to a `vX.Y.Z` release.

`install.sh` installs stable by default, and edge with `HAND_INSTALL_VERSION=edge`. On Windows, `install.ps1` does the same, also with `-Edge` or `-Version`, from `hand-windows-amd64.zip` and its `.sha256`. `hand update` stays on the channel of the running binary, and `hand update --channel edge|stable` moves to the other one.

### Update

What `hand update` does, in order:
1. downloads and checks the newest build of the channel;
2. pins the Luvus version that build was tested with, when it is newer than the pin;
3. backs up each fleet's `hand.db` and the old binary to `$SECONDHAND_HOME/backups/`, keeping two of each;
4. replaces the binary;
5. for each fleet:
   - stops its watcher by `watch.pid`;
   - switches its Luvus server to the pin when the fleet is stale, quiet and settled (see Luvus switch below), otherwise reports `pending: <reason>`;
   - starts that watcher again the way `hand supervisor start` does, when it stopped one;
6. stops the board by `board.pid` and starts it again the way `hand open` does, when it stopped one;
7. runs `hand init` in each fleet.

`hand update --check` walks steps 5 and 6 on the same path without changing anything: it fills the same `units` rows, each marked `would run`, and stops no process, no supervisor and no Luvus server and runs no `hand init`.

#### Luvus switch

On every OS a fleet's server is stale when its UHP `ping` version differs from the pin's. A server that does not answer is `not running` and a server that answers without a version is `unknown`; neither is switched, and `hand update --check` lists such a fleet under that name and plans no switch for it. A stale fleet is switched only when it is:
- quiet (issue #774): no live attempt, an idle supervisor, no queued input, and no live pane besides the supervisor's and the shell Luvus opens when it starts;
- settled: no event in the fleet newer than 15 minutes, and, for `hand update --auto`, no `luvus.failed` event for this pin version;
- not the fleet this update runs inside: a `hand update` whose environment has `LUVUS_SESSION` equal to the fleet's session stays `pending` for that fleet. The board's timer scrubs that variable, so it may switch the fleet later.

The steps, where any failed check leaves the fleet `pending: <reason>` and changes nothing more than the watcher's stop and start:
1. check quiet and settled;
2. stop the fleet's watcher by `watch.pid` and journal it;
3. read the supervisor pane's screen `content_revision`, wait 30 seconds, read it again, and check quiet again: typing or output keeps the fleet pending;
4. `hand supervisor stop` (journaled), then check quiet again; a queued input or a new pane resumes the supervisor and keeps the fleet pending;
5. stop the server: `server.stop`, then wait up to 10 seconds for the process in `server.pid` (in the session folder that `luvus session list --json` reports) to exit, and end it itself (on Windows its whole process tree) only when the stop was acknowledged or `ping` no longer answers; a recycled process ID, whose start marker differs from the one in `server.pid`, is never signalled, and a server that answers `ping` but refuses `server.stop` is never forced;
6. start the server from the pin (`luvus.Ensure`) and require that `ping` reports the pin's version;
7. resume the supervisor (a new `sN` on the same session), start the watcher, and append the event `luvus.switched` with the detail `OLD -> NEW`.

A failure at step 4 to 6 (the supervisor's stop, the server's stop or start, a wrong version) appends `luvus.failed` with the detail `PIN: reason` (a server that comes back with another version is one), resumes the supervisor and starts the watcher again as the journal's repair path does, and the update exits non-zero. The board's timer, which passes `--auto`, never tries the same pin again; a manual `hand update` does. Both events show in `hand orient`'s recent events, as one line in the board's chat (`Luvus 0.14.3 → 0.15.0 (switched while quiet)`), in the `fleets` row (`switched`, `pending: <reason>`, `failed: <reason>`) and in `update.log`.

Accepted losses: a `hand attach` viewer is disconnected, and text typed but not sent in the supervisor's pane is lost when the screen stayed still for 30 seconds. No worker loses work, since a switch needs no live attempt and no pane Hand did not start.

While `update.lock` is held, the board starts no watcher, so it cannot start one in the middle of a switch.

Stopping is the same on every OS: read the pid file (`PID START-MARKER BINARY`), skip it when the process no longer matches its start marker, skip it (reported as `skipped: runs BINARY`, and the fleet's Luvus switch stays pending) when it records another binary than the one being updated, then send SIGTERM and wait up to 10 seconds (Windows: `TerminateProcess`); a process that is still alive fails the step and is not started again. A pid file from a build that recorded no binary counts as the updated one. The process exits cleanly, so a systemd unit with `Restart=on-failure` does not bring it back. An installed `secondhand-board` unit is started again by the board's start; an installed watch unit stays inactive while the transient `secondhand-watch-<fleet id>` unit runs the watcher. `watch.lock` and the board's listen address keep a second watcher or board from running: the second exits (status 3 for the watcher, which systemd does not retry).

Before its first change it writes `$SECONDHAND_HOME/update.json`, a journal of the steps still to run, and removes it at the end. If an update dies part way, the next `hand update` finishes those steps first: it starts the watchers, resumes a supervisor it stopped (a supervisor whose resume failed after a Luvus switch stays in the journal until a resume succeeds), starts the board and runs `hand init`. That report reads `status: repaired`.

`hand update --keep-luvus` runs the same steps but never switches a Luvus server: every fleet whose server differs from the pin stays `pending: --keep-luvus`, and when the binary is already current and no newer Luvus needs pinning it returns `up to date` before it stops anything. Without `--keep-luvus`, a current binary and no newer Luvus still return `up to date` when no fleet is stale; otherwise `hand update` visits only the stale fleets: it stops no board, runs no `hand init` and leaves every other fleet's watcher alone, and its `fleets` rows show `skipped: current` under `init`. `hand update` holds a lock on `$SECONDHAND_HOME/update.lock` for the whole run (not `--check`): a second run exits at once with exit code 3. The journal is written to a temporary file, synced and renamed, and its folder is synced on Linux and macOS.

#### Update timer

`hand board --update-every DURATION` (0, the default, is off; 10 minutes at least) makes the board loop that keeps the watchers alive also start `hand update --channel CHANNEL --auto` as a detached child every DURATION, with its output in `$SECONDHAND_HOME/update.log` and `LUVUS_*` variables other than `LUVUS_HOME` scrubbed from its environment. It starts none while the previous child lives or `update.lock` is held, and none from a build whose channel is `source` (the board says why once). The board records DURATION in `board.update-every`, so the board an update restarts keeps it; a board started without the flag removes the file. With the timer off nothing switches a Luvus server by itself.

A backup restores with the units stopped: remove the stale `hand.db-wal` and `hand.db-shm`, then copy a backup over `hand.db`: the newest `hand.db.<stamp>` from `hand update`, or a daily `hand-YYYYMMDD.db` from the watcher, both under `$SECONDHAND_HOME/backups/<fleet id>/`.

### Secondhand

The family and storage name around Hand: the shared folder `~/.secondhand`, the `secondhand` skill, and the `secondhand-*` Luvus sessions and systemd units.

### Fleet

One supervisor's world: a fleet home with its projects, tasks, attempts, Luvus session and watcher. Several fleets can run side by side, and one board serves them all. `hand fleet list` shows the registered fleets and whether each home is `ok`, `missing`, `moved` or unreadable.

### Fleet ID and fleet name

The first `hand init` in a folder gives the fleet an ID that never changes: `f` followed by 12 hex digits. The ID names the fleet's link in the shared folder, its worktree folder, its Luvus session, its branches and its board path. The name is what people see. It defaults to the folder's name, and `hand init --name NEW` changes it.

### Fleet home

Any folder that holds `hand.db`, which `hand init` creates. Commands find the fleet home in this order:

1. `--home DIR`, given before the command;
2. the working directory or the nearest folder above it that holds `hand.db`;
3. `$HAND_HOME`.

A `HAND_HOME` that differs from the fleet home found from the working directory is refused while you are inside a fleet. A fleet home cannot sit inside another fleet or inside the shared folder. To move a fleet, `mv` its folder and run `hand init` in the new place. A copy of a fleet home is refused until one of the two copies is deleted.

A fleet home holds:

- `hand.db`: the SQLite state;
- `AGENTS.md` and `CLAUDE.md`, which Hand owns and rewrites on every `hand init`;
- the `secondhand` skill under `.claude/`, `.agents/`, `.grok/` and `.pi/`;
- `memory/`: the operator memory;
- `routing.json`: the routing profiles;
- `inbox/`: the files attached on the board, and its trash (see Inbox folder and trash);
- `board.token`, made the first time the board serves the fleet;
- run-time files such as `luvus/`, `locks/`, `watch.lock` and `watch.log`.

### Inbox folder and trash

`inbox/` in the fleet home holds the files the operator attaches on the board, named `YYYYMMDD-HHMMSS-N.ext` or `YYYYMMDD-HHMMSS-N-<name>`, with or without an extension (see Supervisor input). Hand deletes nothing there except through a prune:

- `hand inbox size` counts those files, sums their size and gives the ages of the oldest and the newest, by modification time, and the same count and size for the trash.
- `hand inbox prune --older-than Nd` (at least `1d`) moves each such file older than N days from `inbox/` into the trash, `inbox/.trash/` (mode 0700), by a rename on the same disk, and stamps it with the time of the move. It then deletes each such file in the trash older than 7 days by that stamp. `--dry-run` lists both and changes nothing.
- A prune touches only regular files with those names, directly inside `inbox/` or, for the expiry, `inbox/.trash/`. It never touches a symlink, a folder, an `.upload-*` file the board is still writing, or a file with another name, and it refuses an `inbox/` or `inbox/.trash` that is a symlink rather than a real folder. A file it cannot move or delete is reported, and the others still go. A prune that is not a dry run holds the fleet's `locks/inbox.lock`, so a second one at the same time exits 3.
- A prune that moves files records the event `inbox.pruned` with their count and size: `3 files, 12 MiB`. Deleting expired trash records no event, since each file's move was recorded.
- `"inbox": {"keep": "30d"}` in `routing.json` makes the watcher run that prune once a UTC day (see Watcher). `"off"`, the default, prunes nothing.

### Shared folder

`~/.secondhand`, or `$SECONDHAND_HOME`. It is not a fleet. `fleets/<fleet id>` is a file that holds the path of that fleet's home (older installs have a symlink, which `hand init` rewrites as a file), `worktrees/<fleet id>/` holds every worker worktree outside every fleet, `luvus/` holds the **Luvus pin**, `board.addr` records where the running board listens, `board.log` holds the output of a board `hand open` started detached, `board.start.lock` makes two simultaneous `hand open` calls start one board, and `board.pid` holds the running board's `PID MARKER BINARY`. `update.lock` is the lock a `hand update` holds, `update.log` the last automatic update's output, and `board.update-every` the board's `--update-every` for the board `hand update` restarts. A fleet home also holds `watch.pid` for its running watcher. `hand update` stops the processes these files name.

### Operator memory

Plain files under `memory/` that Hand never rewrites. `hand init` creates `memory/operator.md` once; it holds the operator's standing constraints, and `hand orient` prints up to 4000 bytes of it. `memory/projects/NAME.md`, when it exists, holds one project's lessons, and the supervisor reads it before writing a brief.

### Skill

The instructions `hand init` installs for the supervisor. The `secondhand` skill describes the supervision loop. The `secondhand-migrate` skill is installed only beside Hand 0.7 data, and it guides one migration.

## Work

### Project

A short name (lowercase letters, digits and hyphens) for a git repository at an absolute path on this machine: `hand project add api /home/you/src/api`. Hand does not clone or copy the repository. It adds worker worktrees and branches to that repository with `git worktree add`. Every task belongs to one project.

### Task

`tN`. The durable record of one operator request, with a project, a one-line title of 1–200 characters, and a goal that says what done looks like. The supervisor creates it with `hand task add` before any other work, so nothing lives only in chat.

| State | Meaning | Reached with |
|---|---|---|
| `inbox` | captured, not started | `hand task add` |
| `active` | work has begun; only now can attempts start | `hand task start` |
| `done` | finished; refused while an attempt is live or a report is unread | `hand task done` |
| `abandoned` | dropped; refused while an attempt is live | `hand task abandon` |

An `inbox` task can become `active` or `abandoned`, and an `active` task can become `done` or `abandoned`. `done` and `abandoned` are final, and reaching either withdraws the task's open decisions.

### Plan and plan revision

The supervisor's written plan for a task. `hand plan set --body-file PLAN.md tN` adds a new revision, numbered `p1`, `p2` and so on within that task. The newest revision is the current plan, and older ones are kept. Only an `inbox` or `active` task takes a new revision. `hand plan show tN` prints the current plan.

### Brief

The text the supervisor gives a worker with `hand attempt start --prompt-file BRIEF.md`: the goal, the constraints, the exact acceptance check, and what to commit. Hand appends the instructions for reporting back. Luvus takes at most 16 KiB per argument, so the brief and that footer must fit in 16 KiB. On Windows the footer asks the worker to write the report to a file outside the worktree and run `hand report add --status done --file PATH`, which works in PowerShell and Git Bash, instead of piping a here-document. The footer also asks the worker to wait for CI and reviews inside one blocking foreground command, never to end its turn to wait, and to report only when done or stuck.

### Attempt

`aN`. One worker's run on one task. `hand attempt start` needs an `active` task with no live attempt. Hand then:

1. makes a worktree at `worktrees/<fleet id>/tN-aN` in the shared folder, on the new branch `hand/<fleet id>/tN-aN`, from `--base` of the project's repository. Without `--base` Hand runs `git fetch origin` there (20 s at most) and starts from the branch `origin/HEAD` points to, else `origin/main`, else `origin/master`; a repository with no `origin`, or with none of those refs, starts from its current branch. When the start is a remote ref, unpushed local commits are not in the worktree unless `--base HEAD` is given. If the fetch fails, the worktree starts from the last fetched ref, or from the current branch when there is none, and `hand attempt start` prints a `warning:` line saying which and why. With `--continue aM` there is no new branch: the worktree checks out `aM`'s existing branch, and the new attempt records that it continues `aM`, which `hand attempt show` and the board's task page name. `aM` must belong to the same task and be ended and cleaned, and `--base` does not apply (the `worktrees/` folders are created with mode `0700`, and Hand's own git calls run no repository hooks and no `core.fsmonitor` command, so a hook a worker plants never runs inside Hand; other repository-config commands such as `diff.external`, filter smudge and `core.sshCommand` are not neutralised, and the real boundary is the Safe use guidance in `docs/safe-use.md`);
2. starts the harness there in a Luvus terminal, with the brief as its prompt.

A task keeps all its attempts. At most one is live, that is `launching` or `running`.

| State | Meaning |
|---|---|
| `launching` | recorded; the worktree and terminal are being made |
| `running` | the worker's terminal is live in a pane |
| `exited` | the terminal or its process ended by itself |
| `interrupted` | the Luvus server restarted, or the terminal's root process changed |
| `stopped` | `hand attempt stop` ended it |
| `failed` | the launch failed, or did not finish within two minutes |

`launching` becomes `running` or `failed`, and `running` becomes `exited`, `interrupted` or `stopped`. The watcher and every `hand attempt`, `hand supervisor` and `hand attach` command first check the live attempts against Luvus.

When the watcher ends an attempt, it keeps the last screen it read from that attempt's pane, if it read one: about 2 KB from the end, with escape sequences and control characters removed. `hand attempt show aN` prints it as `last_screen`. The screen is stored only in the attempt row. It never appears in an event, a wake, `hand orient` or the board, because a worker can print anything. An attempt the watcher did not end, or whose pane it never read, has no `last_screen`. The screen is read when a turn ends, so it is that turn's last screen and not the pane's final state.

`hand attempt clean aN` removes an ended attempt's worktree and keeps its branch. It refuses uncommitted changes, and commits on no branch, tag or remote, unless `--discard` is given.

### Report

`rN`. A worker's account of its attempt, added with `hand report add --status STATUS` from inside its worktree. Only a `running` attempt can report, and the body is 1 byte to 64 KiB.

| Status | Meaning |
|---|---|
| `progress` | a milestone; work goes on |
| `done` | the worker finished |
| `stuck` | the worker cannot continue |

A report stays unread until someone acknowledges it: the supervisor with `hand report ack rN`, or the operator with **Mark read** on the board. `hand task done` is refused while any report of the task is unread.

### Deliverable

Whatever a worker or the supervisor delivered: a PR or merge request on any forge, a document or page URL, or the path of a file or folder. The report footer asks the worker to end its report with one line `Delivered: <link or absolute path>` for each. A task the supervisor did itself has no report, so it passes each with `hand task done --deliverable PATH_OR_URL tN` (repeatable), which records one `task.delivered` event per value in the same transaction as `task.done`; the same rule applies, but a bad value is refused with an error that names it, and then nothing is recorded and the task is not finished. Hand reads report deliverables on demand, with no stored column: `Delivered:` lines first, and only for a report with none, the change-request URLs in its body (GitHub `/pull/N`, Gitea and Forgejo `/pulls/N`, GitLab `/-/merge_requests/N`, Bitbucket `/pull-requests/N`). A value that is neither an http(s) URL nor an absolute path (a POSIX or Windows path, judged by its text) is ignored. Every reader takes both sources together, newest first: `hand task show`, the board's task card (the newest) and task page, and `hand open tN --delivered`. The board labels each `PR`, `MR`, `link` or `file`; a `file` chip links to the [document viewer](#board) when the path is inside the fleet home and the viewer serves it, and stays text when the path is outside the fleet home, gone, or refused. `hand open tN --delivered` opens the newest, in the document viewer when its `file` chip would link there, and never a UNC path.

### Decision

`dN`. A question the supervisor asks the operator about one `inbox` or `active` task: `hand decision ask tN "QUESTION"`, or `hand decision ask --file - tN` with a longer question on stdin; a question holds at most 8000 characters. The question's first line is its headline, at most 120 characters; the lines after it are the body, shown with the board's markdown. A numbered line in the body that reads `N. Label — consequence (recommended)` is an option: the board lists the options as rows (the label in bold, the consequence under it, a Recommended badge) with an optional note and one Send, and answers with `N. Label`, then a blank line and the note. The note is optional, except that an option whose label starts with `Other` needs one. The consequence and the badge are optional, and a body without numbered lines gets a plain answer box. The `secondhand` skill makes it the only way the supervisor asks the operator anything.

| Status | Meaning |
|---|---|
| `open` | waiting for the operator |
| `answered` | the operator answered on the board or with `hand decision answer dN "ANSWER"` |
| `withdrawn` | the supervisor withdrew it with `hand decision withdraw dN`, or its task became `done` or `abandoned` |

## Runtime

### Luvus

The terminal runtime Hand runs every agent in ([RizRiyz/luvus](https://github.com/RizRiyz/luvus)). Hand speaks UHP 1.x to it over a Unix socket (on Windows a named pipe, whose address `luvus session list --json` reports and whose server must run as the same user) and uses only exact-argv terminals, agent status, fenced input and events. Hand keeps its own state and worktrees, and never uses Luvus's task ledger or worktree commands.

Each fleet has its own Luvus session, `secondhand-<fleet id>`. In a systemd user session, Hand starts that session's server in a transient scope (`systemd-run --user --scope`), so restarting the watcher or the board never stops an agent. Without a user session, or when `systemd-run` fails, Hand starts the server directly.

### Luvus pin

The copy of a Luvus binary that Hand runs, kept under `$SECONDHAND_HOME/luvus/<version>-<sha8>/luvus` (`luvus.exe` on Windows) and chosen by `$SECONDHAND_HOME/luvus/pin.json`. Every fleet under one `SECONDHAND_HOME` uses it, so a system upgrade never changes the Luvus a fleet runs.

- `hand init` pins the `luvus` on `PATH` when there is no pin, and downloads the tested version, checked against its SHA-256, when there is none on `PATH` either. `hand luvus pin` without `BIN` does the same when there is no pin. `hand luvus pin [BIN]` pins another binary, and old copies stay for rollback.
- The server start and `hand attach` check the copy's sha256 and refuse a copy that changed on disk.
- A new pin reaches a running server only when that server restarts. `hand luvus show` prints the server's version (from a UHP `ping`) and whether it is the pinned copy: `yes`, `no`, `not running` or `unknown`. Restarting it ends every live pane, so Hand does it only for a quiet, settled fleet (Luvus switch, under Update): `hand update` or the board's timer switches such a fleet, or you stop the server yourself, let the next `hand` command start the pinned copy, and resume the supervisor.

### Pane

The Luvus view of one terminal. The running supervisor and every running attempt each have a pane. Hand reads the screen and sends keys through it, and `hand attach supervisor` or `hand attach aN` opens it in your terminal.

### Blocked screen and screen revision

A screen that waits for an answer, such as a trust or permission prompt, puts the agent in the `blocked` state. `hand attempt read aN` prints the screen as an indented multi-line block (`screen: |`, then the lines, with escape sequences and control characters other than newline and tab removed) and its revision. `hand attempt keys --revision N aN KEY...` sends keys only if the screen is still at that revision, so a stale answer is never typed. The supervisor's own screen works the same way, through `hand supervisor show` and `hand supervisor keys --revision N KEY...`, with the keys `enter`, `esc`, `up`, `down`, `1`, `2` and `3`. Claude Code's auto-deny countdown moves the revision every second, so the board also sends `--screen DIGEST`, a digest of the screen with the countdown taken out: when the revision moved but the digest still matches, the keys go out once at the new revision. A key press on the supervisor's screen is recorded as a `supervisor.keys` event.

### Watcher

`hand watch`, one per fleet. It:

- follows Luvus's events and checks the live attempts at least every 30 seconds (`--every`);
- records the `attempt.blocked`, `attempt.quiet`, `attempt.idle`, `attempt.limited` and `attempt.long` events from Luvus's agent status, and on every start catches up on a blocked screen or an ended turn it missed while it was down;
- delivers queued messages and wakes to the managed supervisor, and alerts once each time a supervisor reaches a usage limit, counting a limit as over once a screen read shows no limit line or another supervisor takes over;
- wakes the managed supervisor with `supervisor.stalled` while it idles with unwatched work (see Unwatched and stalled), and with `timer.fired` when a timer comes due (see Timer: it needs a managed supervisor);
- snapshots the fleet's `hand.db` once a day (UTC) with `integrity_check` to `$SECONDHAND_HOME/backups/<fleet id>/hand-YYYYMMDD.db`, and keeps the newest 7, retrying a failed snapshot only after the next UTC day starts or the watcher restarts; `hand update` keeps its own `hand.db.<stamp>` files in that folder, and neither removes the other's;
- prunes the inbox folder once a UTC day when `routing.json` sets `inbox.keep`, as `hand inbox prune --older-than KEEP` does, and writes the day to `inbox/.trash/.pruned` so a restart that day does not prune again; it logs only the files that failed, which wait for the next day's run; while `routing.json` is invalid it says so once and does not prune;
- resumes an interrupted or exited supervisor when it starts, if `routing.json` turns on `supervisor.autoresume`;
- alerts when an attempt ends, blocks, hits a usage limit or goes quiet (not for `attempt.idle`, which wakes nobody), when a decision is asked while it runs, and once per stall episode, on the first stall wake at or after 60 minutes idle;
- sends each alert through the `notify` command in `routing.json` (an argv; `HAND_NOTIFY_KIND`, `HAND_NOTIFY_REF`, `HAND_NOTIFY_TEXT` and `HAND_NOTIFY_URL` in its environment), else through `notify-send` (on macOS, `osascript`; on Windows, none), unless `--notify=false`.

The running board keeps it alive. `hand unit board` prints the board's systemd user unit on Linux, and exits 2 on macOS and Windows.

`hand supervisor start`, `resume` and `switch` start it when its `watch.lock` is free: as the transient user unit `secondhand-watch-<fleet id>` (`Restart=on-failure`, `RestartSec=5`, `RestartPreventExitStatus=3`) in a systemd user session, otherwise detached with its output in `watch.log`. `hand orient` reports `watch: running` or `watch: missing` from the same lock, without creating it. The running board starts it the same way, at startup and every 30 seconds, for each fleet that has a live supervisor or that has `autoresume` in `routing.json` and an ended supervisor to resume (the last one that started is `interrupted` or `exited`), so a crashed or killed watcher comes back on macOS and Windows too and after a reboot once the board is up. A fleet with neither gets none, a fleet whose home is gone or whose `hand.db` will not open is skipped, and a failed start is written to the board's log once per change of error. That probe holds the lock for an instant, so a starting watcher tries it five times, 20 ms apart, before it gives up.

### Event and cursor

Every state change is one transaction that also appends an event with a sequence number, a kind such as `task.active` or `attempt.reported`, a task and a detail. A cursor is the sequence number of the last event seen. `hand orient` prints the current one.

### Wake

A message that tells the supervisor something changed, that a timer it armed came due, or that it idles with unwatched work, so it never polls and spends no tokens while idle.

- **Managed supervisor:** Hand sends its pane a message whose first line is `[hand v1 wake]`, followed by one `KIND DETAIL` line for each event since the supervisor's wake cursor. A line about a task ends with that task, e.g. `(t1 "Fix the login redirect")`. Hand sends a wake only while the supervisor is idle or done, and only after every queued operator message has gone out. While the supervisor's screen shows its harness's usage-limit line, Hand holds wakes and leaves the cursor where it is, because the harness would answer them with the limit error. It holds them too when it cannot read that screen. The hold lifts once the line leaves the screen. Operator messages still go out.
- **Supervisor opened by hand:** it runs `hand wait --after CURSOR`, which returns the wake events and the next cursor. It has no timers: `hand wake` is refused without a managed supervisor, so it waits with `hand wait --timeout`.

The wake kinds are:

| Kind | Meaning |
|---|---|
| `attempt.reported` | a worker added a report |
| `attempt.quiet` | the worker's turn ended; the detail says whether it reported since its last quiet turn, or `api error: <line>` when claude's screen has a line starting `API Error:` |
| `attempt.idle` | the worker's turn ended again, or after its `done` or `stuck` report |
| `attempt.limited` | the worker's turn ended at its harness's usage limit; the detail is the limit line |
| `attempt.long` | the worker's turn has run 60 minutes without a break; the detail is `working for 60m` |
| `attempt.blocked` | the worker waits at a screen that needs an answer |
| `attempt.exited` | the attempt ended by itself; the reason reads `terminal exited (code N)` or `terminal exited (signal NAME)` when the watcher saw Luvus's `terminal.exited` event within 1 second of noticing the terminal gone, else `terminal exited`; a `hand` command that notices the exit first records the bare reason |
| `attempt.interrupted` | the attempt was cut off by a Luvus restart or a changed process |
| `attempt.failed` | the attempt's launch failed |
| `decision.answered` | the operator answered a decision |
| `supervisor.stalled` | the supervisor idles with unwatched work; the detail names the tasks and the idle time |
| `timer.fired` | a timer came due; the detail is `wN`, how late it fired when that is a minute or more, and its note |

`attempt.idle` and a `progress` report never wake the supervisor on their own, and `attempt.idle` sends no desktop or notify-command alert either; `attempt.limited` and `attempt.long` always do. They ride in the next wake with the event that does, or go out once 50 of them wait.

Wakes reach the managed supervisor through the watcher, so `hand watch` must run for the fleet.

### Unwatched and stalled

A fleet's work is **unwatched** when at least one task is `active`, no attempt is live, no decision is open and no timer is armed that is due within 6 hours while a managed supervisor runs: no event will ever wake the supervisor for it. Its **idle time** counts from the fleet's last event other than `supervisor.stalled` and `supervisor.delivered`, which only records that something already recorded reached the supervisor. `hand orient` lists such tasks as `unwatched[N]` (at most 15, then `unwatched_more`) with `unwatched_idle`, and prints neither otherwise.

The watcher records `supervisor.stalled`, a wake, when the work is unwatched and the supervisor is running and idle: Luvus reads it as `idle` or `done`, its screen revision has not changed for 30 seconds (the test the Luvus switch uses), no message waits for it, its screen shows no usage-limit line, and no model switch is pending. A stopped, blocked, limited or switching supervisor gets none. An episode's first stall comes when the idle time reaches `after`; each later one waits twice as long as the one before, up to `every`: by default 20 minutes, then 40, then every 60. Any other event ends the episode, so the idle time and the delays start again. A turn holds the next stall until `after` has passed since the watcher last saw the supervisor working. The detail names the tasks and the idle time: ``s3: t139 and t142 are active, no attempt is running, idle 23m. Start a worker, arm a timer with `hand wake`, or finish the task.`` The first stall wake of an episode at or after 60 minutes idle also alerts the operator, once.

`routing.json` sets the delays with `"stall": {"after": "20m", "every": "60m"}`, each from `1m` to `24h` (one left out keeps its default), and `"stall": "off"` turns the stall wake off. Without `stall` the defaults apply. A bad value makes `routing.json` invalid with an error that names it; the watcher says so once in its output and keeps the defaults.

### Timer

`wN`. A wake the supervisor arms for later with `hand wake`, for something outside Hand such as a PR review: once after a delay (`--after 30m`), once at a time (`--at <UTC time, RFC 3339>`), or repeatedly (`--every 30m`). `--while tN` ties it to a task: finishing or abandoning the task cancels it. `--note` says what to check, in one line of at most 200 characters. Timers belong to the managed supervisor: `hand wake` is refused while no managed supervisor runs (a supervisor on the current Luvus server, started by `hand supervisor start` or `resume`), and a supervisor opened by hand waits with `hand wait --timeout` instead. `hand wake list` and `hand wake cancel` work without one. A timer is due at least 1 minute ahead and repeats at most once a minute, and a fleet holds at most 20 armed timers. It is stored in `hand.db` as `armed`, `fired` or `cancelled`, so it survives a watcher restart, a supervisor switch and a resume. `hand wake list` shows the armed ones, `hand wake cancel wN` cancels one, and `hand orient` lists them as `timers` (id, due time, note).

The watcher fires a due timer while the managed supervisor runs, on its start and on every reconcile tick, by recording `timer.fired`, a wake, whose detail is `w3: check PR 909`. A timer that came due while the watcher or the supervisor was down fires once when both are back, marked with how late it is: `w3, 2h10m late: check PR 909`. A repeating timer then moves to its next due time, or to `every` after now when that has passed, so it never fires twice for one gap. `hand wake`, a fire and a cancel record `timer.armed`, `timer.fired` and `timer.cancelled` with the timer's task. An armed timer keeps the work from being unwatched only while a managed supervisor runs and it is due within 6 hours (the watch horizon); with no managed supervisor, or one from an earlier Luvus server, no armed timer counts, the work shows as `unwatched`, and `hand orient` marks each timer `(no managed supervisor: not fired)`. A timer armed further out, such as `--at` a month away, does not stop the stall wake, and `hand orient` marks it `(beyond the horizon)` while the stall wake names the nearest one (`t1 is active, no attempt is running, idle 20m; w2 is armed but due in 31d`). A repeating timer counts while its next due time is within the horizon, and a timer that comes within it starts counting then. The watcher logs a fire as `timer.fired w3: check PR 909`, the form of the event. Hand never asks a forge or CI anything for a timer: it only wakes the supervisor.

### Supervisor input

`iN`. A message queued for the supervisor, from the board's message box or `hand supervisor send --text TEXT`. It is at most 16 KiB of UTF-8 with no control characters other than newline and tab, and it may not start with `[hand v1`, which Hand keeps for its own messages. Inputs are delivered in order as soon as the supervisor can take them.

An input can carry files. Each one the operator pastes, drops or picks on the board, of any type and up to 10 MiB, is copied into the fleet's `inbox/` folder as `YYYYMMDD-HHMMSS-N-<safe name>`: the original base name with every run of characters outside `[A-Za-z0-9._-]` turned into `_`, leading dots and dashes stripped, and at most 80 bytes with the extension kept. An image (png, jpeg, webp or gif, by its bytes, not its name) takes the extension of its type, and any other file named like an image gets `.bin` appended. The board holds them as chips beside the message box and, on send, appends a line `[image: PATH]` for an image and `[file: PATH]` for any other file after the operator's text, one per file; older images are named `YYYYMMDD-HHMMSS-N.ext`. The supervisor reads that path with its own tools, as data. Chat shows each such line from this fleet's `inbox/` as a thumbnail for an image and a file card for anything else, and a dashed card once the file is gone. A prune may later move the file to the trash, and it matches only these names Hand gives, never another file in `inbox/` (see Inbox folder and trash).

### Model switch

A pending change of the supervisor's model or effort that keeps its harness and its conversation: `hand supervisor switch --model M --effort E`, `--profile NAME`, or the model menu on the board. Hand applies it when the current turn ends, by stopping the supervisor and resuming the same session with the new model under a new `sN`. `--cancel` drops a pending switch. opencode keeps its model in the session and cannot switch.

`hand supervisor switch --harness H` (or a profile on another harness) changes harness instead. It is refused while the supervisor is working; between turns it stops the supervisor and starts the next one on the new harness, with a launch message that names the one it replaces. The new supervisor starts a new session and orients from `hand orient`; the board keeps the earlier session in Chat under a divider. `hand route list` and the board's model menu list each harness's models and efforts: claude's aliases, codex's models cache, and none for opencode. agy's come from `agy models`, with no efforts.

### Routing profile

A named harness, model and effort in `routing.json` in the fleet home. `hand init` writes a starter of five `claude` `sonnet` profiles that differ only in effort (`quick` low, `default` medium, `high` high, `deep` xhigh, `max` max) when the file is missing, and never overwrites it. `hand route list` shows the profiles and whether each is valid on this machine: its model and effort are known and its harness is found on `PATH`, by the same lookup a start uses. A start from the board searches the board's `PATH`, which it keeps from when it started, and its error says so. `--profile NAME` picks one for `hand attempt start`, `hand supervisor start` and `hand supervisor switch`. The same file holds `supervisor.autoresume`, the watcher's `notify` command and its `stall` delays. `hand route list` also prints a track record per harness, model and effort over ended attempts: how many, and the rates of first-try finish, re-attempt, stuck reports, interventions and wakes per attempt, and the median minutes to the first `done` report.

## Surfaces

### Orient

`hand orient`: a bounded summary of the fleet, rendered from state alone. It shows the home, the fleet, the supervisor, the task counts, the cursor, the unwatched tasks and their idle time when there are any, the armed timers when there are any (one due beyond 6 hours is marked `(beyond the horizon)`, and every one is marked `(no managed supervisor: not fired)` while none runs), `inbox_folder` with the count and size of the inbox folder's files when there are more than 500 or they pass 100 MiB (with a help line pointing to `hand inbox size`), the active tasks with their plan, attempt (with `quiet`, or `blocked:` and the screen's hint, when that is its newest signal), report and open decisions, then the open decisions, the unread reports, the inbox, recent events and the operator memory. A title, question, hint or report summary that is too long is cut at a word boundary, so a URL is never cut in half. It stays under 6000 bytes: when it would not fit, it drops rows and says which command lists the rest. The supervisor runs it at the start of every turn and after every wake.

### Doctor

`hand doctor`: a check of this machine and the fleet it runs in, for when something does not work. It prints the binary's version, channel and path, git's version, the pinned Luvus and its server's version, then a `checks` table of `check,state,fix` rows, where the state is `ok`, `warn`, `fail` or `info` and every row that is not `ok` names its fix. The checks: `hand` (this binary is the one on `PATH`; a source build), `git` (found; `core.longpaths` on Windows), `harness NAME` for each harness found on `PATH` (codex's model cache, `agy models`), `systemd` on Linux (a user session, information only), `luvus pin` (pinned, unchanged and runnable), and inside a fleet `schema`, `fleet` (registered under `~/.secondhand`), `routing.json`, `profile NAME` (the check `hand route list` makes), `luvus server` (its version against the pin), `board` (a board for this fleet answers at `board.addr`) and `watcher`. Outside a fleet it skips the fleet checks and says so. It exits 1 when a check fails, else 0. It repairs nothing, starts no server, agent or board, and spends no tokens. It opens `hand.db` as every command does, so a database of an older schema is migrated then. It reads no credential, so it cannot tell whether a harness is logged in, and it checks the shell's `PATH`, not the board's.

### Board

`hand board`: a local web page from the same binary, and the operator's interface. One board process serves every registered fleet. `/<fleet id>/` is one fleet's page, and `/` lists the fleets while the board listens on a loopback address; on a network address `/` shows no list, so open a fleet by its link. The default address is `127.0.0.1:7777` (`--addr`). `hand open` starts a board when none answers its `/<fleet id>/proof` check: `systemctl --user start` of an installed `secondhand-board` unit, else a transient unit of that name, else a detached process (macOS, Windows, no user manager) logging to `board.log`; it waits up to 10 seconds for the proof.

A fleet's page has two parts. At 900px and wider, the work pane (Needs, then Tasks) sits beside the supervisor pane, with a separator between them that the operator drags to size the chat (the board remembers one width); below that Needs, Tasks and Chat are three tabs, which merge into the masthead on short screens. The [rail](#rail) lists the pages and the fleets:

- **Needs:** what waits on the operator, most severe first: the supervisor's blocked screens; a worker's blocked screen or its turn that ended without a report, once no supervisor is running or the supervisor has left it for 10 minutes; a worker stopped at a usage limit, at once and until it works again, and the supervisor while its screen shows a limit line; failed, exited or interrupted attempts; "Work is waiting for a supervisor" while work waits on one; the idle supervisor once a stall wake went out (`Supervisor idle for 1h12m with 3 active tasks and no running attempt`), until the work is watched, another event arrives or the supervisor works; a supervisor that stopped unexpectedly; open decisions; unread reports; an attempt whose turn has run 60 minutes (`attempt.long`). The items sit in bands, one head per kind (Blocked, Failed, Decision, Report and so on); the top item is open and the rest are one-line rows. Below them sits the task list, grouped under Active and Inbox, with each attempt's agent state. With nothing waiting it reads "All clear";
- **Chat:** the supervisor pane: the conversation, read from the harness's own session record without tool calls or thinking (the supervisor's replies on the pane, the operator's messages as bubbles on the right, Hand's own events as one-line notes), plus the message box and the supervisor controls. The rail's Conversation page shows the same conversation on a page of its own.

Each task and each decision also has its own page; from the fleet page, tasks, decisions, reports and outside links open in a new tab, and the rail, the crumbs and the pager navigate in place. The Tasks list under Needs keeps active and inbox tasks and the five most recent finished ones; **Browse finished tasks (N)** opens `/<fleet id>/tasks`, the Tasks page: a search over title, goal and ref (every word must match, in any case) that covers every task; filters for status (default (every status once there is a query, finished without one), or one chosen status), project (when the fleet has several) and a UTC date range, and results newest first, grouped by day with a count, 50 to a page. `hand task list --q` searches every status the same way. A task's finish date is the time of its `task.done` or `task.abandoned` event; a task still open dates by its last update. `?all=1` on a fleet page still lists every task. Every action answers with a short receipt, and Chat shows key presses and the supervisor's starts, stops and switches as Hand lines. The board is a projection of state, so restarting it loses nothing. The supervisor controls work only while the board listens on a loopback address. On a network address, only answering decisions and marking reports read still work.

`/<fleet id>/doc/<path>` is the document viewer: a read-only page for a file or folder inside the fleet home, on a loopback or a network address. It renders Markdown up to 1 MiB in document mode (relative links, relative images and task lists, still without raw HTML), shows other text of any size as numbered lines (a file up to 8 MiB whole, in chunks of 500 lines; a larger one as a window of about 4 MiB that opens at its end, moved by `?at=BYTE` or `?line=N`, with `?find=TEXT` listing up to 200 matching lines over the whole file, and `?from=BYTE&line=N` returning the complete lines appended since BYTE as the Follow feed, or the header `X-Hand-Reset` when the file shrank or was replaced), serves an image inline when both its bytes and its name say image, previews JSON (pretty up to 16 MiB), CSV and TSV (a header and a table of the first 2,000 rows), diffs, HTML (a sandboxed frame without scripts), SVG (an `<img>`), PDF (a same-origin frame), and video and audio (only when the sniffed type and the extension agree, with HTTP Range), each with a Source, Text or As written toggle where the file has text, through `?raw` headers set per type (`sandbox` CSP, `nosniff`, no `allow-scripts` or `allow-same-origin`), lists a folder (folders first, then names in any case order with the exact name as the tie-break, dot entries hidden, every entry in pages of 500 by `?page=N`, a last page for a number past the end, and a refused symlink as a row with its reason and no link), gives an image its own page with `?view` (Fit) and `?view=actual`, steps a file's page to the neighbouring files of its folder (`Previous` and `Next`, with ← and →, skipping folders and refused entries), and offers every file as a download; a file whose first 8 KiB holds a NUL or invalid UTF-8 shows a notice instead of text. A text response never reads an unbounded file into memory and never writes unbounded HTML, and the line writer escapes every byte: ANSI colour (SGR 30 to 37 and 90 to 97, bold) becomes `x-` classes, other CSI and OSC sequences are dropped, C0 and C1 controls and bidirectional controls show as visible marks (boxed for the first 20,000 in a response, plain symbols after that, so the HTML stays within a few times the file's size), and a line is cut at 256 KiB. It refuses dot paths, the files at the top of the fleet home (`hand.db`, `board.token`, `AGENTS.md` and the rest), `luvus/`, `locks/`, `projects/`, anything that is not a regular file or a folder, and any path whose real path, after symlinks, leaves the fleet home; the same rules decide whether a `file` deliverable links to it, whether `hand open tN --delivered` opens it there, and whether an absolute path in a reply, decision, report, plan or document becomes a link (at most 50 paths in one message; a path that is missing or refused stays text).

### Rail

The Board's navigation: a 56px column of icons at the left edge from 900px, and below that a sheet opened from the masthead's menu button. It holds Board (with the live Needs count), Tasks, Conversation and the fleets. A name shows on hover, on focus, or for every item at once from the label button. It lists the current fleet and only the other fleets whose token the browser holds and that fleet still accepts; it shows no other fleet's Needs count. On a network address it lists only its own fleet.

### Board token

`board.token` in each fleet home. It guards that fleet's page, and the browser exchanges it for a cookie named for that one fleet, which only that fleet accepts. The cookie goes to every page of the board, which is how the rail knows the other fleets the browser can open. `hand open` logs the browser in with it without printing it; `hand open --print` prints a login link that holds it, for another device or browser.

### TOON

The compact text format of every command's output: `key: value` fields, `name[N]{fields}:` tables, and `help[N]:` hints that name the next command.

## Refs

Every record has a short ref that commands, the board and wakes share.

| Ref | Record | Numbered |
|---|---|---|
| `tN` | task | across the fleet |
| `pN` | plan revision | within its task |
| `aN` | attempt | across the fleet |
| `rN` | report | across the fleet |
| `dN` | decision | across the fleet |
| `sN` | supervisor launch | across the fleet |
| `iN` | supervisor input | across the fleet |
| `wN` | timer | across the fleet |

## Changed since 0.7

Hand 0.7 ([`0.7`](https://github.com/atqamz/hand/blob/0.7/docs/vocabulary.md)) used some terms that the redesign dropped or changed:

- **Supervisor** was the operator's own interactive session. It is now the managed background agent `sN`. A session opened by hand still works, but stands down while a managed supervisor runs.
- **Supervisor harness and worker harness** were separate registries. There is now one list of harnesses, `claude`, `codex`, `opencode` and `agy`, used for both roles. Grok and Pi are no longer launched, although `hand init` still installs the skill for them.
- **Fleet identity** in `state/hand.db` is now the fleet ID in `hand.db` at the root of the fleet home, and fleets also have a name.
- **Fleet registry** (`~/.secondhand/registry.db`) is replaced by the pointer files in `~/.secondhand/fleets/`.
- **Current invocation context** has no term of its own. The fleet home is still found from the working directory or `HAND_HOME`, but the working directory now wins: a `HAND_HOME` naming another fleet is refused while you are inside one. `--home DIR` is new.
- **Duplicate fleet** remains a refusal: a copied fleet home is refused until one copy is deleted.
- **Supervisor orientation** is still `hand orient`. Monitor targets and currentness tokens are gone, and it prints an event cursor instead.
- **Monitor target, currentness token and wake hint** are replaced by events, the cursor and the `[hand v1 wake]` message.
- **Legacy Herdr namespace** is gone: Herdr and Treehouse are replaced by Luvus and plain `git worktree`.
- **Task kind** (`scout`, `ship`) and **execution class** (`mechanical`, `standard`, `deep`) are dropped. A task has only a goal. Whether it ships code is up to the brief.
- **Brief** front matter (`execution_class`, `planned_against`, `model`, `effort`) is dropped. A brief is plain text, and the harness, model and effort come from the attempt's flags or profile.
- **Profile** is now a routing profile in `routing.json`. **Route**, the mapping from task kind and execution class to a profile, is dropped. `hand route list` lists the profiles, and the supervisor names one per attempt.
- **Project** was cloned, adopted or created under the fleet home. It is now a name for an existing repository, which Hand uses in place.
- **Delivery mode and gate** (`direct-pr`, `local-only`, `no-mistakes`) are dropped, and Hand has no merge, deliver or teardown step.
- **Worker** is recognised by its worktree's path. `HAND_ROLE=worker` is gone.
- **Report** was a scout's file, `data/<id>/report.md`. It is now a record of any attempt, with a status, and it is read and acknowledged through Hand.
- **Plan**, **decision** and **supervisor input** are new records with their own refs.
