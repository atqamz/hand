# Commands

Run `hand` with no command to list the commands, and a command with no subcommand, such as `hand attempt`, to list its subcommands. Put flags before the positional arguments; only `hand open` also takes `--delivered` after the ref, and `hand task done` its `--deliverable`; `hand task deliver` takes its values after the ref. `--home DIR`, given before the command, picks the fleet.

| Command | Purpose |
|---|---|
| `hand init [--name NAME] [DIR]` | Make a fleet, or refresh, rename or adopt a moved one. |
| `hand fleet list` | List the registered fleets and whether each home is found. |
| `hand project add NAME PATH`, `hand project list` | Register a repository by its absolute path, and list projects. |
| `hand project trust NAME` | Accept the project's current git settings that name a program, after Hand refused to run git because they changed; prints each value. |
| `hand task add --goal TEXT PROJECT TITLE` | Capture a request as an `inbox` task. |
| `hand task start tN`, `hand task done [--deliverable PATH_OR_URL] tN`, `hand task abandon tN` | Move a task to `active`, `done` or `abandoned`. `--deliverable`, repeatable, records a file or link the supervisor produced itself: an http(s) URL or an absolute path; any other value is refused, and then nothing is recorded and the task stays as it was. |
| `hand task deliver tN PATH_OR_URL...` | Record a deliverable on an `active` or `done` task, for a late or missing one: one `task.delivered` event per value, the same value rule as `--deliverable`, and the task's status, finish date, plan and reports stay as they were. A task in the inbox or abandoned is refused, and so is a bad value (exit 2), and then nothing is recorded, not even the good values of the same call. A value the task already has as a `task.delivered` event is skipped and listed under `already recorded`, so a backfill can run again; a value that only appears in a report's `Delivered:` line is recorded anyway, and the readers show it once. |
| `hand task list [--status LIST] [--q TEXT] [--since DATE] [--until DATE] [--limit N]`, `hand task show tN` | List tasks newest first (default `inbox,active`; `--status` takes the board's words: `inbox`, `active`, `done`, `abandoned`, `finished` for done and abandoned, and `all` alone for every status), or show one with its plan, attempt, report, deliverables (newest first) and decisions. `--q` searches every status unless `--status` is given, and keeps tasks where every word, in any case, is in the title or goal or is the whole ref (`t12`). `--since` and `--until` take a UTC `YYYY-MM-DD` and filter on the day a task finished (`done` or `abandoned`; its last update otherwise). Rows of finished tasks carry that day in a `finished` column. |
| `hand plan set --body-file PATH tN`, `hand plan show tN` | Add a plan revision (or `--body TEXT`), and show the current one. |
| `hand attempt start --profile NAME --prompt-file PATH tN` | Start a worker in a new worktree from the repository's fetched default branch (`origin/HEAD`, else `origin/main`, else `origin/master`, else its current branch; from a remote ref, unpushed local commits are left out, so pass `--base HEAD` to keep them); `--harness`, `--model` and `--effort` replace `--profile`, `--base REF` picks another start point, and `--continue aN` checks out the branch of `aN`, an ended and cleaned attempt of the same task, so pushes reach its PR. |
| `hand attempt list [--task tN] [--limit N]`, `hand attempt show aN` | List attempts, or show one with its live agent state, or the last screen the watcher read from one that ended. |
| `hand attempt read [--lines N] aN` | Print a running worker's screen, as an indented multi-line block, and its revision. |
| `hand attempt keys --revision N aN KEY...` | Answer a worker's screen, only if it is still at that revision. |
| `hand attempt send --text TEXT aN` | Send a worker a message (or `--file PATH`, or `--file -` for stdin), only while it is at its prompt. |
| `hand attempt stop aN`, `hand attempt clean [--discard] aN` | Stop a worker, and remove an ended attempt's worktree while keeping its branch. |
| `hand report add --status STATUS --file -` | Report from inside a worker's worktree: `progress`, `done` or `stuck`, from stdin, a file, or `--text`. |
| `hand report list [--task tN] [--attempt aN] [--unacked]`, `hand report show rN` | List and read reports. |
| `hand report ack [--by NAME] rN` | Acknowledge a report. |
| `hand decision ask [--file PATH\|-] tN [QUESTION]` | Ask the operator about a task; the first line is the headline (at most 120 characters). |
| `hand decision answer [--by NAME] dN ANSWER`, `hand decision withdraw dN` | Answer or withdraw a decision. |
| `hand decision list [--all]`, `hand decision show dN` | List open decisions (or all), and read one with its answer. |
| `hand route list` | Show the routing profiles and the harnesses. |
| `hand luvus pin [BIN]` | Pin a copy of a Luvus binary (default: `luvus` on `PATH`) for every fleet under this `SECONDHAND_HOME`. |
| `hand luvus show` | Show the pinned Luvus and the version of the fleet's running server (from a UHP `ping`), with `match: yes`, `no`, `not running` or `unknown`. |
| `hand orient` | Print the bounded fleet summary the supervisor starts every turn with. |
| `hand inbox size` | Show the attachments in the fleet's `inbox/`: how many, their size, the age of the oldest and of the newest, and the trash. |
| `hand inbox prune --older-than DURATION [--dry-run]` | Move the attachments older than DURATION (days, like `30d`) to `inbox/.trash/`, and delete the files in the trash for more than 7 days; `--dry-run` only lists them. See [Clean the inbox](inbox.md). |
| `hand wait [--after CURSOR] [--timeout DURATION]` | Block until a wake event arrives, for a supervisor opened by hand. |
| `hand wake --after DURATION\|--at TIME\|--every DURATION [--while tN] [--note TEXT]` | Arm a timer (refused without a running managed supervisor) that wakes it once after a delay, once at an RFC 3339 time, or repeatedly; `--while tN` cancels it when that task is done or abandoned. See [Timers](supervisor.md#timers). |
| `hand wake list`, `hand wake cancel wN` | List the armed timers, or cancel one. |
| `hand watch [--every DURATION] [--notify=false]` | Run the fleet's watcher. |
| `hand watch stop` | Stop this fleet's watcher, and only if `watch.pid` still names the process that wrote it. A running board starts a watcher again within 30 seconds when the fleet needs one: run `hand board stop` first to keep it down. See [Stop the board or a watcher](autostart.md#stop-the-board-or-a-watcher). |
| `hand supervisor start [--profile NAME]` | Start the managed supervisor; `--harness`, `--model` and `--effort` replace `--profile`, and with no flags it copies the last one. |
| `hand supervisor resume`, `hand supervisor stop`, `hand supervisor show` | Continue the last session, stop it, or show its state, screen revision and number of queued messages. |
| `hand supervisor send --text TEXT` | Queue a message for the supervisor (or `--file PATH`, or `--file -` for stdin). |
| `hand supervisor keys --revision N KEY...` | Answer the supervisor's blocked screen with `enter`, `esc`, `up`, `down`, `1`, `2` or `3`. |
| `hand supervisor interrupt` | Press Escape in the supervisor's terminal. |
| `hand supervisor force` | When Luvus misreads the supervisor as blocked but its screen shows an empty Claude Code prompt, type the queued messages (or else the wakes) into it. |
| `hand supervisor switch --model M --effort E` | Switch the model or effort after this turn; or `--profile NAME`, or `--cancel`. `--harness H` switches to another harness between turns. |
| `hand board [--addr ADDR] [--update-every DURATION]` | Serve the board for every fleet. |
| `hand board stop` | Stop the running board, and only if `board.pid` still names the process that wrote it. Watchers keep running. See [Stop the board or a watcher](autostart.md#stop-the-board-or-a-watcher). |
| `hand open [--print] [REF]`, `hand open tN --delivered` | Open the fleet page, a task, decision, report or attempt, or a task's newest deliverable, starting the board first when none runs. `--print` prints the login link instead, for another device or browser. |
| `hand attach [supervisor]`, `hand attach aN` | Open the fleet's Luvus session, the supervisor's terminal, or a worker's. |
| `hand unit board` | Print the board's systemd user unit (Linux only; on macOS and Windows it exits 2, see [Autostart](autostart.md#run-the-board-at-login)). It starts with the graphical session. Your `PATH`, and a `SECONDHAND_HOME` if set, go into the unit. |
| `hand update [--channel edge\|stable] [--check] [--keep-luvus] [--auto]` | Update the binary from its channel, back up and refresh every fleet, restart each fleet's watcher and the board, and pin the tested Luvus. |
| `hand version` | Print the version, the channel (`source`, `edge` or `stable`), the commit, the state schema and the tested Luvus. |
| `hand doctor` | Check this machine and the fleet without repairing anything, one `check,state,fix` row each; exits 1 when a check fails. |
