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

The coding-agent CLI that Hand launches as a supervisor or a worker. There are four: `claude` (Claude Code), `codex` (Codex), `opencode` (opencode) and `agy` (Antigravity). Each can run the supervisor or a worker, in full-auto mode:

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

`install.sh` installs stable by default, and edge with `HAND_INSTALL_VERSION=edge`. On Windows, `install.ps1` does the same with `-Edge` or `-Version`, from `hand-windows-amd64.zip` and its `.sha256`. `hand update` stays on the channel of the running binary, and `hand update --channel edge|stable` moves to the other one.

### Update

What `hand update` does, in order:
1. downloads and checks the newest build of the channel;
2. pins the Luvus version that build was tested with, when it is newer than the pin;
3. backs up each fleet's `hand.db` and the old binary to `$SECONDHAND_HOME/backups/`, keeping two of each;
4. replaces the binary;
5. for each fleet:
   - stops its watch unit;
   - switches its Luvus server to the pin when the fleet is quiet (no live attempt, an idle supervisor, no queued input, and no live pane besides the supervisor's and the shell Luvus opens when it starts), otherwise reports the switch as pending and names any such pane;
   - starts the watch unit again; a transient watch unit is gone once stopped, so it starts that watcher the way `hand supervisor start` does;
6. restarts the board unit;
7. runs `hand init` in each fleet.

Before its first change it writes `$SECONDHAND_HOME/update.json`, a journal of the steps still to run, and removes it at the end. If an update dies part way, the next `hand update` finishes those steps first: it starts the watch units and watchers, resumes a supervisor it stopped, restarts the board and runs `hand init`. That report reads `status: repaired`.

A backup restores with the units stopped: remove the stale `hand.db-wal` and `hand.db-shm`, then copy the backup over `hand.db`.

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
- `board.token`, made the first time the board serves the fleet;
- run-time files such as `luvus/`, `locks/`, `watch.lock` and `watch.log`.

### Shared folder

`~/.secondhand`, or `$SECONDHAND_HOME`. It is not a fleet. `fleets/<fleet id>` is a file that holds the path of that fleet's home (older installs have a symlink, which `hand init` rewrites as a file), `worktrees/<fleet id>/` holds every worker worktree outside every fleet, `luvus/` holds the **Luvus pin**, `board.addr` records where the running board listens, and `board.pid` holds the running board's `PID MARKER`. A fleet home also holds `watch.pid` for its running watcher. `hand update` on Windows stops the processes these files name.

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

The text the supervisor gives a worker with `hand attempt start --prompt-file BRIEF.md`: the goal, the constraints, the exact acceptance check, and what to commit. Hand appends the instructions for reporting back. Luvus takes at most 16 KiB per argument, so the brief and that footer must fit in 16 KiB. The footer also asks the worker to wait for CI and reviews inside one blocking foreground command, never to end its turn to wait, and to report only when done or stuck.

### Attempt

`aN`. One worker's run on one task. `hand attempt start` needs an `active` task with no live attempt. Hand then:

1. makes a worktree at `worktrees/<fleet id>/tN-aN` in the shared folder, on the new branch `hand/<fleet id>/tN-aN`, from `--base` (default `HEAD`) of the project's repository (the `worktrees/` folders are created with mode `0700`, and Hand's own git calls run no repository hooks and no `core.fsmonitor` command, so a hook a worker plants never runs inside Hand; other repository-config commands such as `diff.external`, filter smudge and `core.sshCommand` are not neutralised, and the real boundary is the Safe use guidance in the README);
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

`hand attempt clean aN` removes an ended attempt's worktree and keeps its branch. It refuses uncommitted changes, and commits on no branch, tag or remote, unless `--discard` is given.

### Report

`rN`. A worker's account of its attempt, added with `hand report add --status STATUS` from inside its worktree. Only a `running` attempt can report, and the body is 1 byte to 64 KiB.

| Status | Meaning |
|---|---|
| `progress` | a milestone; work goes on |
| `done` | the worker finished |
| `stuck` | the worker cannot continue |

A report stays unread until someone acknowledges it: the supervisor with `hand report ack rN`, or the operator with **Mark read** on the board. `hand task done` is refused while any report of the task is unread.

### Decision

`dN`. A question the supervisor asks the operator about one `inbox` or `active` task: `hand decision ask tN "QUESTION"`, or `hand decision ask --file - tN` with a longer question on stdin. The question's first line is its headline, at most 120 characters; the lines after it are the body, shown with the board's markdown, and a numbered option in the body becomes a button that fills the answer. The `secondhand` skill makes it the only way the supervisor asks the operator anything.

| Status | Meaning |
|---|---|
| `open` | waiting for the operator |
| `answered` | the operator answered on the board or with `hand decision answer dN "ANSWER"` |
| `withdrawn` | the supervisor withdrew it with `hand decision withdraw dN`, or its task became `done` or `abandoned` |

## Runtime

### Luvus

The terminal runtime Hand runs every agent in ([RizRiyz/luvus](https://github.com/RizRiyz/luvus)). Hand speaks UHP 1.x to it over a Unix socket (on Windows a named pipe, whose address `luvus session list --json` reports and whose server must run as the same user) and uses only exact-argv terminals, agent status, fenced input and events. Hand keeps its own state and worktrees, and never uses Luvus's task ledger or worktree commands.

Each fleet has its own Luvus session, `secondhand-<fleet id>`. In a systemd user session, that session's server runs in the user unit `secondhand-luvus-<fleet id>`, so restarting the watcher or the board never stops an agent. Without a user session, Hand starts the server directly.

### Luvus pin

The copy of a Luvus binary that Hand runs, kept under `$SECONDHAND_HOME/luvus/<version>-<sha8>/luvus` (`luvus.exe` on Windows) and chosen by `$SECONDHAND_HOME/luvus/pin.json`. Every fleet under one `SECONDHAND_HOME` uses it, so a system upgrade never changes the Luvus a fleet runs.

- `hand init` pins the `luvus` on `PATH` when there is no pin. `hand luvus pin [BIN]` pins another binary, and old copies stay for rollback.
- The server start and `hand attach` check the copy's sha256 and refuse a copy that changed on disk.
- A new pin reaches a running server only when that server restarts. `hand luvus show` says whether the server is the pinned copy. Restarting it ends every live pane, so it stays a manual step at a quiet time: stop `secondhand-luvus-<fleet id>`, let the next `hand` command start the pinned copy, and resume the supervisor.

### Pane

The Luvus view of one terminal. The running supervisor and every running attempt each have a pane. Hand reads the screen and sends keys through it, and `hand attach supervisor` or `hand attach aN` opens it in your terminal.

### Blocked screen and screen revision

A screen that waits for an answer, such as a trust or permission prompt, puts the agent in the `blocked` state. `hand attempt read aN` prints the screen and its revision. `hand attempt keys --revision N aN KEY...` sends keys only if the screen is still at that revision, so a stale answer is never typed. The supervisor's own screen works the same way, through `hand supervisor show` and `hand supervisor keys --revision N KEY...`, with the keys `enter`, `esc`, `up`, `down`, `1`, `2` and `3`. Claude Code's auto-deny countdown moves the revision every second, so the board also sends `--screen DIGEST`, a digest of the screen with the countdown taken out: when the revision moved but the digest still matches, the keys go out once at the new revision. A key press on the supervisor's screen is recorded as a `supervisor.keys` event.

### Watcher

`hand watch`, one per fleet. It:

- follows Luvus's events and checks the live attempts at least every 30 seconds (`--every`);
- records the `attempt.blocked`, `attempt.quiet`, `attempt.idle`, `attempt.limited` and `attempt.long` events from Luvus's agent status, and on every start catches up on a blocked screen or an ended turn it missed while it was down;
- delivers queued messages and wakes to the managed supervisor, and alerts once each time a supervisor reaches a usage limit, counting a limit as over once a screen read shows no limit line or another supervisor takes over;
- resumes an interrupted or exited supervisor when it starts, if `routing.json` turns on `supervisor.autoresume`;
- sends desktop notifications through `notify-send` (on macOS, `osascript`; on Windows, none) unless `--notify=false`.

`hand unit watch` prints a systemd user unit for it, with the caller's `PATH`.

`hand supervisor start`, `resume` and `switch` start it when its `watch.lock` is free: as the transient user unit `secondhand-watch-<fleet id>` in a systemd user session, otherwise detached with its output in `watch.log`. `hand orient` reports `watch: running` or `watch: missing` from the same lock, without creating it. That probe holds the lock for an instant, so a starting watcher tries it five times, 20 ms apart, before it gives up.

### Event and cursor

Every state change is one transaction that also appends an event with a sequence number, a kind such as `task.active` or `attempt.reported`, a task and a detail. A cursor is the sequence number of the last event seen. `hand orient` prints the current one.

### Wake

A message that tells the supervisor something changed, so it never polls and spends no tokens while idle.

- **Managed supervisor:** Hand sends its pane a message whose first line is `[hand v1 wake]`, followed by one `KIND DETAIL` line for each event since the supervisor's wake cursor. A line about a task ends with that task, e.g. `(t1 "Fix the login redirect")`. Hand sends a wake only while the supervisor is idle or done, and only after every queued operator message has gone out. While the supervisor's screen shows its harness's usage-limit line, Hand holds wakes and leaves the cursor where it is, because the harness would answer them with the limit error. It holds them too when it cannot read that screen. The hold lifts once the line leaves the screen. Operator messages still go out.
- **Supervisor opened by hand:** it runs `hand wait --after CURSOR`, which returns the same events and the next cursor.

The wake kinds are:

| Kind | Meaning |
|---|---|
| `attempt.reported` | a worker added a report |
| `attempt.quiet` | the worker's turn ended; the detail says whether it reported since its last quiet turn |
| `attempt.idle` | the worker's turn ended again, or after its `done` or `stuck` report |
| `attempt.limited` | the worker's turn ended at its harness's usage limit; the detail is the limit line |
| `attempt.long` | the worker's turn has run 60 minutes without a break; the detail is `working for 60m` |
| `attempt.blocked` | the worker waits at a screen that needs an answer |
| `attempt.exited` | the attempt ended by itself; the reason reads `terminal exited (code N)` or `terminal exited (signal NAME)` when the watcher saw Luvus's `terminal.exited` event within 1 second of noticing the terminal gone, else `terminal exited`; a `hand` command that notices the exit first records the bare reason |
| `attempt.interrupted` | the attempt was cut off by a Luvus restart or a changed process |
| `attempt.failed` | the attempt's launch failed |
| `decision.answered` | the operator answered a decision |

`attempt.idle` and a `progress` report never wake the supervisor on their own; `attempt.limited` and `attempt.long` always do. They ride in the next wake with the event that does, or go out once 50 of them wait.

Wakes reach the managed supervisor through the watcher, so `hand watch` must run for the fleet.

### Supervisor input

`iN`. A message queued for the supervisor, from the board's message box or `hand supervisor send --text TEXT`. It is at most 16 KiB of UTF-8 with no control characters other than newline and tab, and it may not start with `[hand v1`, which Hand keeps for its own messages. Inputs are delivered in order as soon as the supervisor can take them.

An input can carry images. Each one the operator pastes, drops or picks on the board is saved in the fleet's `inbox/` folder as `YYYYMMDD-HHMMSS-N.ext`, and the message gets a line `[image: PATH]`. The supervisor opens that path with its harness's image reading. Chat shows each such line from this fleet's `inbox/` as a thumbnail.

### Model switch

A pending change of the supervisor's model or effort that keeps its harness and its conversation: `hand supervisor switch --model M --effort E`, `--profile NAME`, or the model menu on the board. Hand applies it when the current turn ends, by stopping the supervisor and resuming the same session with the new model under a new `sN`. `--cancel` drops a pending switch. opencode keeps its model in the session and cannot switch.

`hand supervisor switch --harness H` (or a profile on another harness) changes harness instead. It is refused while the supervisor is working; between turns it stops the supervisor and starts the next one on the new harness, with a launch message that names the one it replaces. The new supervisor starts a new session and orients from `hand orient`; the board keeps the earlier session in Chat under a divider. `hand route list` and the board's model menu list each harness's models and efforts: claude's aliases, codex's models cache, and none for opencode. agy's come from `agy models`, with no efforts.

### Routing profile

A named harness, model and effort in `routing.json` in the fleet home. `hand init` writes a starter of five `claude` `sonnet` profiles that differ only in effort (`quick` low, `default` medium, `high` high, `deep` xhigh, `max` max) when the file is missing, and never overwrites it. `hand route list` shows the profiles and whether each is valid on this machine. `--profile NAME` picks one for `hand attempt start`, `hand supervisor start` and `hand supervisor switch`. The same file holds `supervisor.autoresume`. `hand route list` also prints a track record per harness, model and effort over ended attempts: how many, and the rates of first-try finish, re-attempt, stuck reports, interventions and wakes per attempt, and the median minutes to the first `done` report.

## Surfaces

### Orient

`hand orient`: a bounded summary of the fleet, rendered from state alone. It shows the home, the fleet, the supervisor, the task counts, the cursor, the active tasks with their plan, attempt (with `quiet`, or `blocked:` and the screen's hint, when that is its newest signal), report and open decisions, then the open decisions, the unread reports, the inbox, recent events and the operator memory. It stays under 6000 bytes: when it would not fit, it drops rows and says which command lists the rest. The supervisor runs it at the start of every turn and after every wake.

### Board

`hand board`: a local web page from the same binary, and the operator's interface. One board process serves every registered fleet. `/<fleet id>/` is one fleet's page, and `/` lists the fleets while the board listens on a loopback address; on a network address `/` shows no list, so open a fleet by its link. The default address is `127.0.0.1:7777` (`--addr`).

A fleet's page has two parts. At 1280px and wider, Needs is a rail beside Chat; below that they are two tabs, which merge into the masthead on short screens:

- **Needs:** what waits on the operator, most severe first: the supervisor's blocked screens; a worker's blocked screen or its turn that ended without a report, once no supervisor is running or the supervisor has left it for 10 minutes; a worker stopped at a usage limit, at once and until it works again, and the supervisor while its screen shows a limit line; failed, exited or interrupted attempts; "Work is waiting for a supervisor" while work waits on one; a supervisor that stopped unexpectedly; open decisions; unread reports. Below them sits the task list, grouped under Active and Inbox, with each attempt's agent state. With nothing waiting it reads "All clear";
- **Chat:** the conversation, read from the harness's own session record without tool calls or thinking, as a thread of cards (the supervisor on the left, the operator on the right), plus the message box and the supervisor controls.

Each task and each decision also has its own page. Every action answers with a short receipt, and Chat shows key presses and the supervisor's starts, stops and switches as Hand lines. The board is a projection of state, so restarting it loses nothing. The supervisor controls work only while the board listens on a loopback address. On a network address, only answering decisions and marking reports read still work.

### Board token

`board.token` in each fleet home. It guards that fleet's page, and the browser exchanges it for a cookie scoped to that one fleet. `hand open` logs the browser in with it without printing it; `hand open --print` prints a login link that holds it, for another device or browser.

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
