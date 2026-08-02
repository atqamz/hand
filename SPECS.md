# Secondhand

Talk to one agent. Ship with a crew.

CLI: `hand`
Language: Go
Default backend: herdr
No persona, no role-play. Pure functionality.

## Problem

Running one coding agent is easy.
Running three in parallel on different tasks across different projects turns you into a tab-juggler: babysitting sessions, copy-pasting context, forgetting which terminal had the failing test.

Firstmate solved this with an "agent distro" - a directory of instructions and shell scripts that turns a general-purpose agent into a fleet supervisor.
The concept works. The execution ballooned to 34k lines of shell across 89 scripts, 1,082 functions, 8k lines of prose instructions, 5 backend adapters, a Twitter bot, and a multi-home federation system - all in 6 days.

Three fundamental problems emerged:

1. **Session clobbering.** The supervisory agent's main session drowns in operational noise: bootstrap digests, hook injections, guard warnings, status polling, watcher rearms. The 21K-token always-loaded instruction file eats context every turn. Long sessions trigger malformed tool calls at ~500k context. The captain's chat becomes a system log instead of a command center.
2. **Shell brittleness.** macOS bash 3.2 causes silent failures in spawn and brief scaffolding. Locale inheritance breaks checksums and state reads. BSD vs GNU tool detection fails with mixed toolchains. Content-hashing terminal panes for state detection is fundamentally fragile.
3. **Self-imposed complexity traps.** Session locks deadlock recovery. Continuity hooks block the commands needed to fix the problems they detect. The watcher/guard/hook system creates more problems than it solves.

Secondhand keeps the concept and rebuilds the execution as a single Go CLI binary.

## Core principles

1. **One binary owns orchestration.** The agent calls CLI commands. The CLI owns lifecycle correctness, state management, and process supervision. No shell scripts.
2. **AGENTS.md stays tiny.** Target ~25 lines of rules. The CLI's `--help` carries operational detail. If the agent needs 500 lines of instructions to operate the tool, the tool is wrong.
3. **herdr-native.** herdr provides semantic agent state (working/idle/blocked/done/unknown) and push events. Use them instead of regex-scraping terminal output. herdr's own agent state carries no task-outcome signal (see "Agent state"); the report channel is what actually tells hand whether a task finished.
4. **Text editing stays with the agent.** The backlog is a markdown file. The agent reads and edits it directly. No CLI wrapper for text operations.
5. **No feature without friction.** Every feature in firstmate that doesn't have a proven use case is cut. Features get added when their absence causes real pain.
6. **A fleet home is any directory `state/hand.db` marks as one.** `hand init` creates the file up front; put `hand` on PATH and launch the agent there. Only `hand` ever writes it, so a project clone under `projects/` carrying its own generic top-level `data/` and `state/` cannot capture the walk up. A home initialized before this marker existed falls back to the marker it was initialized with, `data/dashboard.md` plus `state/`, so an operator upgrading in place never has to re-run anything by hand; new homes do not depend on the dashboard file at all, so deleting it (atqamz/secondhand#62) is safe. Maintainers dogfood a fleet home inside the secondhand repo checkout itself, with runtime state gitignored alongside the tracked code, but the CLI has no opinion about the two: `HAND_HOME`, or an ancestor of the working directory, is all it looks for.
7. **The dashboard is the memory.** `data/dashboard.md` is the living document the CLI maintains. The agent reads it for context. The user watches it for visibility. No session digests, no bootstrap scripts, no 187-line status dumps.
8. **No hooks, no guards, no callbacks.** The CLI fails closed on bad operations. Errors are CLI output, not injected hook messages. The agent reads errors and decides. No magic.

## Architecture overview

```
              user
                |  chat: requests, decisions, "merge it"
                v
    +---------------------------+
    | supervisory agent         |
    | reads AGENTS.md + data/   |
    | edits data/backlog.md     |
    | calls `hand` commands     |
    +--+----------+----------+--+
       |          |          |
       v          v          v
    [task-1]   [task-2]   [task-N]    herdr tabs
    [worker]   [worker]   [worker]    one autonomous agent each
       |          |          |
       v          v          v
    treehouse worktrees (isolated git checkouts)
       |
       +-- ship: branch -> PR -> merge -> teardown
       |
       +-- scout: investigate -> report.md -> teardown
```

The supervisory agent is any supported harness (claude, codex, pi, grok, opencode) launched inside a fleet home.
It reads AGENTS.md, understands the `hand` CLI, and manages the fleet.

Workers are autonomous agents launched by `hand spawn` into herdr tabs with treehouse worktrees.
They follow the brief, do the work, and report through herdr's agent state.

## Machine state and the prose corpus

A fleet home holds two kinds of state, and they have different owners.

**Machine state is authoritative in sqlite**, at `state/hand.db`: the task registry, PR state, herdr pane and tab ids, `hand watch`'s report offsets, the project registry, and holds (see `hand hold set` and "Holds" under "State management").
It is what `hand` writes and what `hand` reads back.
Nothing derives it from a file, and no view is assembled by re-reading a rendering of it.

**The prose corpus is authoritative in files**, under `data/`: briefs, scout reports, the backlog's prose, and whatever else a human or a worker writes there.
sqlite holds only a *derived* index of it, at `state/index.db`, for `hand search`.

The boundary is the design, more than the engine is.
It is drawn where recovery cost differs: machine state is cheap to reconstruct and expensive to disagree about, while the corpus is the opposite.

Three rules keep the derived half honest:

1. **A corrupt index costs a rebuild and never the corpus.** `state/index.db` can be deleted outright at any time; the next `hand search` rebuilds it, and `hand search --rebuild` forces the rebuild against an index that is present but wrong.
2. **Nothing may depend on the index being correct in order to recover.** The index is read by exactly one command, and the rebuild reads the corpus directly, never the index and never machine state - a `state/hand.db` that is missing or unreadable does not stop a supervisor from searching their way back to what the fleet was doing.
3. **Prose bodies are not schematized.** The index holds a path, an mtime, a size, a title and the full text for matching. The body stays the file's, and no command parses a brief into columns.

### Which to believe when they disagree

`state/<id>.status` - the worker-to-supervisor report channel - survives this design unconditionally, and it is a projection, not an input.
The database never holds a second copy of it.

**When the database and a `.status` file disagree about what a worker said, believe the file.**
Not because the database is unreliable, but because of what the two failure modes cost.
The fleet has twice run a stale `hand` binary while every signal it produced read healthy, and both recoveries were `cat` on those status files.
A `.status` file is readable by `cat`, `tail -f`, an editor, and a human with no tooling at all; the database is readable by a working `hand`, which is the thing that was broken both times.
There is deliberately no `hand dump`: a dump command is one more thing that depends on the binary, and so is no help in the case that actually happens.

The database is authoritative for everything the file does not carry - what `hand` recorded, decided, or observed - which is most of machine state.
The file is authoritative for what the worker said.

### Not Postgres, and no daemon

sqlite in rollback journal mode, one short-lived process per command, one writer at a time.
No server, no connection pool, no background process holding the database open.
`hand watch` is still the only long-running process, and it holds no lock between ticks.
This keeps a fleet home a directory that can be copied, backed up and inspected with ordinary tools, which the whole design depends on.

## Directory layout

A standalone fleet home has no tracked section: `hand init` lays down only the gitignored
runtime below, and `hand` runs against it from wherever it lives on disk. The tracked section
exists because secondhand's own maintainers dogfood a fleet home inside the repo checkout,
alongside the code that implements it.

```
secondhand/                 # maintainer's in-repo fleet home = repo checkout
  # tracked (committed)
  main.go                   # entry point
  cmd/                      # cobra command implementations
    root.go
    init.go
    spawn.go
    status.go
    send.go
    teardown.go
    merge.go
    pr.go
    watch.go
    project.go
    promote.go
    search.go
    notify.go
  internal/
    home/                   # fleet home definition and resolution
      home.go               # IsHome marker check, HAND_HOME/ancestor-walk Resolve
    herdr/                  # herdr client library
      client.go             # API calls: create tab, get state, send keys
      types.go              # herdr data types
    store/                  # machine state in sqlite (see "Machine state and the prose corpus")
      store.go              # schema, task, project and hold rows, meta keys
      schemaversion.go      # PRAGMA user_version gate and registered ALTER TABLE steps
      lock.go               # named flocks over state/, shared by state, the import and the schema migration
      migrate.go            # one-way import of pre-sqlite state/<id>.json and data/projects.md
      index.go              # derived full-text index over the prose corpus
    state/                  # task state management, a thin facade over store
      task.go               # read/write/list task rows
      hold.go               # set/clear/read/list hold rows (see "Holds")
      types.go              # Task and Hold struct aliases
      report.go             # read/classify state/<id>.status (see "Report channel")
      pr.go                 # PR URL validation and extraction
    worktree/               # treehouse integration
      worktree.go           # get, return, status, collision check
    brief/                  # brief parsing
      brief.go              # read the brief's declared model/effort (see "Brief format")
    watcher/                # fleet supervision
      watcher.go            # poll/push event loop
      events.go             # event classification
    project/                # project registry
      project.go            # add, list, remove, resolve
      pr.go                 # shared PR validation: repo-slug match, gh existence check
    harness/                # agent launch templates
      harness.go            # per-harness launch command construction
    dashboard/              # living dashboard maintenance
      dashboard.go          # read/write/update data/dashboard.md
    completion/             # durable teardown completion record
      completion.go         # append/list state/completions.jsonl
    agentsmd/               # generated AGENTS.md template
      agentsmd.go           # write and refresh the generated span
    atomicfile/             # shared write-to-temp-then-rename helper
      atomicfile.go         # atomic file replacement
  go.mod
  go.sum
  AGENTS.md                 # agent instructions (~25 lines of rules)
  CLAUDE.md -> AGENTS.md    # symlink for Claude Code compatibility
  README.md
  LICENSE
  .gitignore

  # gitignored runtime (created by `hand init`)
  state/                    # machine state, the report channel, and the durable completion store
    hand.db                 # authoritative machine state: tasks, PR state, pane ids, report offsets, projects, holds
    index.db                # derived full-text index over data/, safe to delete at any time
    migrated/               # pre-sqlite state/<id>.json files, moved aside once imported
    <id>.status             # worker-to-supervisor report channel, worker-written, hand-read-only
    events.log              # recent watcher events, bounded rotating log
    completions.jsonl       # durable teardown completion records, one JSON object per line, uncapped
  data/
    dashboard.md            # living fleet dashboard, auto-maintained by `hand`
    backlog.md              # plain markdown task queue, agent-edited
    projects.md             # project registry projection (see "Project registry format")
    <id>/                   # per-task data directory
      brief.md              # task instructions written by the supervisory agent
      report.md             # scout task deliverable, written by the worker
  projects/                 # git clones, read-only to supervisory agent
  config/                   # local user preferences (optional)
    harness                 # default harness for workers (default: claude)
    model                   # default model for workers (optional)
    effort                  # default effort for workers (optional)
    notify                  # notification command template (optional)
    stale-threshold         # seconds before a task is considered stale (default: 300)
    watch-interval          # poll interval for `hand watch` (default: 5s)
    parked-paused-bound     # seconds a paused-and-silent task may sit before it's parked (default: 3600)
    parked-other-bound      # seconds a silent task in any other state may sit before it's parked (default: 1200)
```

## CLI specification

### `hand init [path] [flags]`

Initialize secondhand runtime directories in the current working directory.
Creates `state/`, `data/`, `projects/`, `config/` if they don't exist.
Creates `data/backlog.md`, `data/projects.md`, and `data/dashboard.md` with skeleton content, and creates `state/hand.db` if it does not already exist - the fleet-home marker `IsHome` checks for (see "Core principles").
Idempotent: safe to run multiple times.
This is the one command that does not resolve its home: it creates the one its argument or the working directory names.
When `HAND_HOME` is set and names some other directory it still initializes the requested target, and warns on stderr that every other command will use `HAND_HOME` instead.

```
hand init
hand init --setup
```

Flags:
- `--setup`: run interactive first-time setup. Discovers available harnesses on PATH (claude, codex, pi, grok, opencode) and available tools (treehouse, herdr, no-mistakes, gh), then asks the user for the default worker harness, model, and effort and writes `config/harness`, `config/model`, and `config/effort`.

Output:
```
initialized secondhand home at /path/to/secondhand
```

Output with `--setup`:
```
found harnesses: claude codex pi grok
found tools: treehouse herdr no-mistakes gh
default worker harness: claude
default worker model: sonnet
worker effort: low
wrote config/harness config/model config/effort
initialized secondhand home at /path/to/secondhand
```

Errors:
- Filesystem permission errors.

---

### `hand project add <repo-url> [flags]`

Clone a git repository into `projects/` and register it in the store.

```
hand project add https://github.com/org/repo
hand project add https://github.com/org/repo --mode no-mistakes
hand project add git@github.com:org/repo.git --name custom-name
```

Flags:
- `--mode <mode>`: delivery mode. One of `no-mistakes`, `direct-pr`, `local-only`. Default: `direct-pr`.
- `--name <name>`: override the project name (default: derived from repo URL).

Behavior:
1. Validate the URL is a git remote.
2. Derive project name from URL (last path segment minus `.git`), or use `--name`.
3. Refuse if a project with that name already exists.
4. `git clone` into `projects/<name>`.
5. If `--mode no-mistakes`, run `no-mistakes init` inside the clone.
6. Initialize treehouse for the project: `treehouse init` inside the clone if no `treehouse.toml` exists.
7. Add the project to the store and rewrite the `data/projects.md` projection.
8. Update `data/dashboard.md` projects section.
9. If clone or init fails, clean up partial state (remove the clone dir, don't append to registry).

Output:
```
added project nsr (https://github.com/yes2games/nsr) mode=direct-pr
```

Errors:
- URL is not a valid git remote.
- Project name already registered.
- Clone fails (auth, network).
- no-mistakes init fails.

---

### `hand project list`

List registered projects from the store.

```
hand project list
hand project list --json
```

Output (human):
```
nsr         https://github.com/yes2games/nsr          direct-pr
yes2infra   https://github.com/yes2games/yes2infra    no-mistakes  (gate: not initialized)
```

Output (JSON):
```json
[
  {"name": "nsr", "url": "https://github.com/yes2games/nsr", "mode": "direct-pr"},
  {"name": "yes2infra", "url": "https://github.com/yes2games/yes2infra", "mode": "no-mistakes", "gate_issue": "not initialized"}
]
```

A `no-mistakes`-mode project whose gate cannot currently be honoured (not initialized, or the
`no-mistakes` binary itself unreachable) gets a `(gate: <issue>)` suffix in human output and a
`gate_issue` field in JSON output (omitted when there is no issue). See "Gate preflight" for what
this checks and why. Every `no-mistakes`-mode project pays one `no-mistakes status` call per
`hand project list` invocation; other modes pay nothing.

---

### `hand project remove <name>`

Unregister a project. Does NOT delete the clone under `projects/`.
Refuses if any active task references this project.

```
hand project remove nsr
```

Output:
```
removed project nsr (clone retained at projects/nsr)
```

Errors:
- Project not found in registry.
- Active tasks reference this project.

---

### `hand spawn <id> <project> [flags]`

Spawn a worker agent in an isolated worktree.

```
hand spawn fix-login nsr
hand spawn fix-login nsr --harness claude
hand spawn investigate-crash nsr --scout
```

Flags:
- `--scout`: mark as scout task (deliverable is a report, not a PR).
- `--harness <name>`: agent harness to launch. Default: value from `config/harness`, or `claude`.
- `--model <name>`: model override for harnesses that support it. Default: the brief's declared `model`, else `config/model`.
- `--effort <level>`: effort level for harnesses that support it. Default: the brief's declared `effort`, else `config/effort`.
- `--skip-gate-check`: dispatch into a `no-mistakes` project even if its gate is not initialized (see "Gate preflight").

Model and effort resolve most-specific-first: the flag, then the brief's `---` declaration (see
"Brief format"), then the config default, then unset. A resolved effort under a harness with no
effort flag (anything but claude) is a warning on stderr, not a failure: the spawn proceeds with
the effort recorded in state and ignored by the launch command.

Behavior:
1. Validate project exists in registry.
2. If the project's mode is `no-mistakes`, run the gate preflight check (see "Gate preflight");
   refuse before touching any task or worktree state if it comes back not initialized or
   unreachable, unless `--skip-gate-check` is set.
3. Validate no active task with this ID exists.
4. Validate no hold is set on this ID (see "Holds" under "State management"). A hold survives the teardown of the task it was set on, so reusing the id for new work would reattach the previous incarnation's open question to an unrelated task; the error names `hand hold clear <id>` as the remedy.
5. Validate `data/<id>/brief.md` exists (the agent must write it before spawning).
6. Acquire a treehouse worktree: `treehouse get --lease --json --lease-holder hand:<id>`, run inside the project clone (treehouse resolves the pool from cwd).
7. **Collision guard:** cross-check the acquired worktree path against all active tasks' recorded worktree paths in the store. If the path matches another active task, return the worktree to treehouse and fail with an error naming the conflicting task. This prevents the stale-lease-after-crash bug (firstmate #947).
8. Acquire the task's herdr tab in the project's workspace.
   - Workspace naming: one workspace per project, named after the project.
   - Tab naming: task ID.
   - If the project's workspace does not exist yet, create it at the worktree's cwd. herdr has no
     way to create an empty workspace - it always creates a root tab and pane alongside it - so
     this reuses that root tab as the task's tab (renamed to the task ID) instead of creating a
     second one, which would leave the root tab behind as an orphan shell in the workspace.
   - If the workspace already exists, create a new tab in it for the task.
9. Construct the harness launch command from the template (see harness section).
10. Send the launch command to the herdr pane.
11. Confirm the worker actually started: poll the pane until herdr reports a live agent on it and
   no first-run dialog is left, answering any known dialog along the way, or the poll window
   elapses (see Harness launch templates).
12. Write the task's row with all metadata.
13. Update `data/dashboard.md` with the new task.

Any failure before step 12 leaves nothing behind: the worktree lease returns to treehouse and the
herdr side is rolled back.
A workspace this command created is closed whole: the task's tab is that workspace's own
auto-created root tab, so there is nothing else in it to preserve.
A workspace that already existed is shared with other tasks, so it keeps running and only loses
the tab this command added.
Once the task's row is written the task owns its worktree and tab, so a later failure such as
the dashboard update never tears down a task that is already running.

Output:
```
spawned fix-login project=nsr kind=ship harness=claude worktree=/home/user/.treehouse/nsr-abc/1/nsr
```

Errors:
- Project not registered.
- `no-mistakes` gate not initialized for a `no-mistakes`-mode project (names the exact remedy
  command; see "Gate preflight"). Skipped entirely with `--skip-gate-check`.
- `no-mistakes` binary missing or not runnable, distinct from the above (see "Gate preflight").
- Task ID already active.
- Task ID has an open hold (names `hand hold clear <id>` as the remedy).
- Brief not found at `data/<id>/brief.md`.
- Treehouse worktree acquisition failed (pool exhausted, git error).
- Worktree collision with another active task (names the conflicting task).
- Herdr tab creation failed (herdr not running, session error).
- Harness not recognized.
- Worker never confirmed started within the poll window, or is waiting on a first-run prompt hand
  refuses to answer (pane content and the blocking prompt included in the error).

Task row written to the `task` table in `state/hand.db`, one column per field below:
```json
{
  "id": "fix-login",
  "project": "nsr",
  "kind": "ship",
  "harness": "claude",
  "model": "sonnet",
  "effort": "low",
  "worktree": "/home/user/.treehouse/nsr-abc123/1/nsr",
  "brief": "data/fix-login/brief.md",
  "herdr": {
    "session": "default",
    "workspace_id": "wA",
    "tab_id": "wA:tB",
    "pane_id": "wA:pC"
  },
  "pr": "",
  "merged": false,
  "merged_at": "",
  "report_offset": 0,
  "pr_merged_observed": false,
  "done_verified": false,
  "created_at": "2026-07-24T10:00:00Z",
  "status_changed_at": "",
  "status_changed_for": "",
  "last_report_state": "",
  "last_report_note": ""
}
```

---

### `hand status [id] [flags]`

Show fleet overview or single-task detail.

```
hand status
hand status fix-login
hand status --json
hand status fix-login --full
```

Flags:
- `--json`: output as JSON. Always carries the reported line and history untruncated - a machine consumer wants the whole field, and silently truncating a JSON field is a data-loss bug, not a rendering choice.
- `--full`: in the single-task view, show the reported line and history untruncated and skip the history dedup below, reproducing the output from before atqamz/secondhand#65 exactly.

Behavior (fleet overview):
1. List every task in the store.
2. For each, query herdr for current agent state.
3. Append the worker's own last classified report to the same column as ` (reported: <state>)`, whatever the pane is doing. A pane state and a report answer different questions, so both print: a worker that appends `paused:` while its harness keeps running used to render as a bare `working`, showing the pane and hiding the only party that had said why. `working` is the one report that stays unadorned, because it is what the column already says. ` (unreported)` still requires a not-busy pane (herdr's `idle` or `done` - see "Agent state" below), since a busy pane that has not reported yet is not a stop anyone has to explain. A report file that exists but can't be read appends ` (report unreadable)`, never ` (unreported)` - an I/O fault is not evidence the worker never reported.
4. If the task has a recorded PR, append its merge state to the same column: ` (merged)` when `hand` performed the merge, ` (merged, external)` when `hand` only observed it - `hand watch`'s own `gh` poll saw it merged, or gate-opened-PR detection recorded a PR that was already merged. It is appended whatever the agent state is, since a merged PR is a fact about the PR rather than about the pane.
5. Print one line per task, with a header row.
6. List every hold in the store (see "Holds" under "State management") and print a `held:` block below the task table, one line per hold, skipped entirely when nothing is held. A hold names any id, not only a live task's, so a torn-down task's still-open hold keeps appearing here after its task row is gone. A failure to read the holds fails the whole command rather than degrading to an empty list - reading no holds back must never be mistaken for nothing being held.

The `last report` column is the mtime of `state/<id>.status`, and `(none)` when the worker has never written one.
It is deliberately not the task's age: the two used to be conflated, so a task spawned hours ago read as hours stale next to a status file its worker had touched minutes earlier - a reporting worker that looked abandoned.
`age` measures the task, `last report` measures the channel.

Output (fleet overview):
```
id           project  kind   state                                     age     last report
fix-login    nsr      ship   working                                   2h ago  3m ago
dark-mode    nsr      ship   blocked                                   45m ago 40m ago
build-wait   nsr      ship   working (reported: paused)                20m ago 5m ago
stuck-task   nsr      ship   idle (unreported)                         1h ago  (none)
paused-task  nsr      ship   idle (reported: needs-decision)           30m ago 12m ago
investigate  nsr      scout  done (reported: done)                     10m ago 9m ago
shipped-fix  nsr      ship   done (reported: done) (merged, external)  5m ago  4m ago

held:
  fix-login         operator  two ways to fix this, needs a call          2h ago
  torn-down-task    operator  question never answered                    1d ago
```

A hold row that can't be trusted at face value - an unrecognized kind, a `blocked` hold with no `blocked_on`, or an `operator` hold carrying one - is still printed, never dropped, with `inconsistent: <why>` in place of its detail: an external write to `state/hand.db` is the only way such a row exists, since `hand hold set` validates before writing, and filtering it out here would silently drop the row most worth seeing.

Behavior (single task):
1. Read the task from the store.
2. If the task is a `ship` task with no PR recorded and its project is registered and not `local-only`: look for a PR on the project's repo whose head ref is the task's current branch (never matched on title, issue number, or task id), and record it under the task and in the Active Tasks PR column if found - a no-mistakes gate's own `pr` step opens a PR directly, bypassing `hand pr`, so `pr` can go unrecorded for genuinely landed work. A branch carrying several PRs resolves by preference tier - merged, then open, then closed-unmerged - and only when the winning tier holds exactly one PR: a tier with more than one match is ambiguous, and so is a merged PR coexisting with an open one on the same head ref - an open PR is live evidence the branch may carry unlanded work, so that mix refuses rather than resolving to the merged PR. A `scout` task is skipped: its deliverable is `data/<id>/report.md`, never a PR. This is a best-effort, non-blocking lookup (a held task lock, an unreachable `gh`, an ambiguous branch, or a task with no branch all just leave the command reporting what it read) so a fleet-wide `hand status` never pays this cost.
3. Query herdr for current agent state and recent output.
4. Read the last 5 lines of the task's report channel (see "Report channel"). A report file that exists but can't be read degrades exactly as it does in the fleet overview: the `Reported` line reads `report unreadable: <error>` and the rest of the detail view still prints, rather than the command failing and showing nothing.
5. Read the hold on this id, if any (see "Holds" under "State management"). Unlike the report channel, a failure to read it fails the command - the same reasoning as the fleet overview's `held:` block.
6. Print detailed view, including the most recent reported line and a labeled history block.

Output (single task):
```
Task:        fix-login
Project:     nsr
Kind:        ship
Harness:     claude
Model:       sonnet
State:       working
Worktree:    /home/user/.treehouse/nsr-abc/1/nsr
Herdr:       default / wA:tB
Created:     2h ago
Last report: 3m ago
PR:          (none)
Reported:    needs-decision: two ways to fix the race, ask-user found both risky
Report file: /home/user/secondhand/state/fix-login.status
Held:        waiting on migrate-schema: needs the new column before this can proceed

Report history (reported by worker, not verified current truth):
  working: added the retry loop
```

The `PR:` line reads `(none)` with no PR recorded, and otherwise carries the same merge suffix the fleet overview appends: `PR:          https://github.com/org/repo/pull/42 (merged, external)`.

The `Held:` line is present only when this id has a hold, and reads the reason alone for an `operator` hold or `waiting on <blocked_on>: <reason>` for a `blocked` one; an inconsistent row (see the fleet overview above) prints `inconsistent: <why>` instead.

atqamz/secondhand#65: a worker's report prose has run several KB for a single task, and rendering it in full doubled the cost by repeating the latest entry - once as `Reported:`, again as the last line of `Report history`. Without `--full`:
- The `Reported` line and every history line are capped to 200 runes (a character budget, not a word or line count, since the point is bounding rendered size). The cut lands after the state-vocabulary prefix (`working:`, `paused:`, `blocked:`, `needs-decision:`, `done:`, `failed:`) - the prefix is never part of what's cut - and a cut line always carries a trailing `... [+N chars]` marker naming how much was dropped, so a short report is never mistaken for a truncated one. `done: <PR url>` stays intact under this budget in the common case, since the worker convention puts the URL immediately after the prefix and 200 runes covers it comfortably; a URL buried after long prose is the same brief-authoring problem the write side already owns (see "Report channel").
- `Report history` drops the entry already shown on the `Reported:` line above it, so the same report is never printed twice in one invocation.
- A `Report file:` line names the absolute path to `state/<id>.status`, so nothing is lost: the full text stays on disk and the path to it is one line away.

`--full` restores the exact pre-#65 shape: both lines untruncated, the latest entry repeated in history, and no `Report file:` line.

`--json` is never truncated or deduped, `--full` or not - see the JSON section below.

The "Report history" label is deliberate: these lines are the worker's own claims about itself, not something `hand` has verified, same caution as the `done`-vs-`reported-done` distinction in `hand watch`.

Output (JSON, single task):
```json
{
  "id": "fix-login",
  "project": "nsr",
  "kind": "ship",
  "harness": "claude",
  "agent_state": "working",
  "worktree": "/home/user/.treehouse/nsr-abc/1/nsr",
  "herdr": {"session": "default", "tab_id": "wA:tB", "pane_id": "wA:pC"},
  "pr": "",
  "merged": false,
  "pr_merged_observed": false,
  "created_at": "2026-07-24T08:00:00Z",
  "last_report_at": "2026-07-24T09:57:00Z",
  "reported": {"state": "needs-decision", "note": "two ways to fix the race, ask-user found both risky"},
  "report_history": ["working: added the retry loop", "needs-decision: two ways to fix the race, ask-user found both risky"],
  "held": {"id": "fix-login", "kind": "blocked", "reason": "needs the new column before this can proceed", "blocked_on": "migrate-schema", "set_at": "2026-07-24T09:00:00Z"}
}
```

`reported` and `report_history` are omitted when the task has no report file yet, and so is `last_report_at`. `held` is omitted when this id has no hold; an inconsistent hold (see the fleet overview above) adds an `inconsistent` field naming why instead of being omitted.

Fleet-overview JSON wraps the per-task rows rather than returning a bare array, so holds - which can outlive the task that had them - have somewhere to sit alongside it:

```json
{
  "tasks": [{"id": "fix-login", "...": "one row per task, reported only, no history"}],
  "holds": [{"id": "fix-login", "kind": "blocked", "reason": "needs the new column before this can proceed", "blocked_on": "migrate-schema", "set_at": "2026-07-24T09:00:00Z"}]
}
```

Errors:
- Task ID not found.
- Herdr unreachable (graceful degradation: show state as "unknown").
- The hold store can't be read (fails the command; never degrades to an empty `holds`/no `held` - see "Holds" under "State management").

---

### `hand send <id> <message>`

Send a text message to a running worker's herdr pane.

```
hand send fix-login "focus on the auth middleware, not the test framework"
```

Behavior:
1. Read the task's row for herdr pane coordinates.
2. Check herdr pane exists and agent is present.
3. Wait for composer to be empty (agent not mid-response).
4. Submit the message text.

Output:
```
sent to fix-login
```

Errors:
- Task not found.
- Herdr pane doesn't exist (agent died, tab closed).
- Composer not empty after timeout (agent busy, message queued or refused).

---

### `hand hold set <id> --kind <kind> --reason <reason> [--blocked-on <id>]`

Record that an id is waiting on something (atqamz/secondhand#63). See "Holds" under "State management" for the design this command exposes.

```
hand hold set fix-login --kind operator --reason "two ways to fix this, needs a call"
hand hold set fix-login --kind blocked --reason "waiting on the migration task" --blocked-on migrate-schema
```

`<id>` is any id, not only a live task's - see "Holds" for why. It is validated with the same charset as a task id (`state.ValidateID`), never read back against the task table.

Flags:
- `--kind`: `operator` (waiting on a human) or `blocked` (waiting on another id). Required.
- `--reason`: why the id is waiting. Required.
- `--blocked-on`: the id being waited on. Required for `--kind blocked`, refused for `--kind operator`.

Behavior:
1. Validate `--kind`, `--reason`, and the `--blocked-on` pairing.
2. Upsert the hold row - a second `hand hold set` on the same id replaces the previous kind, reason, and blocked-on rather than requiring a clear first, and refreshes `set_at` to when it was last set.

Output:
```
hold set on fix-login (kind=operator)
```

Errors:
- Invalid `--kind` (exit 2).
- Missing `--reason` (exit 2).
- `--blocked-on` missing for a `blocked` hold, or given for an `operator` hold (exit 2).

---

### `hand hold clear <id>`

Clear the hold on an id.

```
hand hold clear fix-login
```

Behavior:
1. Delete the hold row. Leaves no residue - a subsequent `hand status` shows nothing held for this id.

Output:
```
hold cleared on fix-login
```

Errors:
- No hold set on `<id>` (exit 3).

---

### `hand teardown <id> [flags]`

Clean up a completed task. Fail-closed: refuses if work isn't properly landed.

```
hand teardown fix-login
hand teardown investigate --force
```

Flags:
- `--force`: skip landed-work checks (requires explicit authorization).

Behavior (ship task):
1. Check the worktree for uncommitted changes. A dirty worktree is not an automatic refusal: if every uncommitted change is a tracked modification whose current content already matches the local default branch's tip byte-for-byte, the dirt is redundant with what already landed and teardown proceeds past it (atqamz/secondhand#79 - the no-mistakes gate's own review-fix round can leave a file edited but uncommitted in the worktree, and that edit sometimes reproduces content the gate's own merged fix already carries). The comparison is content-identical, not path-identical: a same-named file with different content, or a path that merely exists in the base, both still refuse. Both layers a `git status --porcelain` line reports are compared, index and working tree, each where it reports a change: an `MM` path whose working copy matches the base still holds a third, differing version staged in the index, and that staged content is uncommitted work too. Untracked files are never safe - there is nothing in the base to compare them against - so their mere presence refuses regardless of what else is safe. Every failure to resolve, read, or parse fails closed into the refusal, so no dirt is ever discarded unverified. Resolution is local-only, no fetch: a stale local ref just means a real safe case is missed and falls through to the refusal below, never the reverse. When it does refuse, the error carries the worktree's `git status --porcelain` output so the operator can see what is dirty rather than deciding blind, capped at the first 20 entries plus a count of the rest (atqamz/secondhand#65 is the same lesson for report rendering).
2. Check work is landed:
   - If mode is `local-only`: verify the branch is merged into the default branch.
   - Otherwise, if `pr` is not yet set in state and the project is registered: look for a PR on the project's repo whose head ref is the task's current branch, and record it under the task if found (same gate-opened-PR detection `hand status` performs, including the preference-tier rule for a branch with several PRs; see that command's spec). Detection failing because there is nothing to find (no clone on disk, `gh` unreachable) is not itself an error - it falls through to the same refusal below as if no PR existed. An ambiguous branch is different: teardown refuses outright rather than falling through to "no PR recorded" - that message means unlanded, and guessing which of an ambiguous set to trust is the failure this rule exists to remove. The refusal names every PR on the head ref and its state, including one in a losing tier that did not itself trigger the refusal, since the operator has to resolve the whole branch, not just the pair that tripped the rule. A branch carrying both a merged PR and an open one is one such ambiguous case: the open PR is live evidence of unlanded work, so it is never silently resolved to the merged PR.
   - If `pr` is set in state (recorded by `hand pr`, or just detected above): verify the PR is merged via `gh pr view`. A detected PR that is closed without merging is refused exactly like one `hand pr` recorded.
3. Close the herdr tab.
4. Return the worktree to treehouse: `treehouse return <path>`. `--force` is added whenever step 1 proceeded past dirt it judged safe, as well as under the command's own `--force`: treehouse refuses to clean a dirty worktree without it and there is nothing here to answer its prompt, so an unforced return would either abort after the tab is already closed or hand the pool a slot that is still dirty.
5. Append a completion record to `state/completions.jsonl` (see "Completion store" below).
6. Remove the task's row and the task's report channel `state/<id>.status`.
7. Update `data/dashboard.md`: move task to Recent Completions (keep last 10).
8. Keep `data/<id>/brief.md` for history (the agent can prune old briefs).

The report channel goes because it is the volatile wake log, not a deliverable: a task respawned under a used ID starts at `report_offset` 0, so a surviving log would be replayed as this run's - re-raising decisions already resolved, absorbing a genuine unexplained stop as one already seen, and auto-recording a PR URL out of the previous run's `done` line onto a task nobody recorded it for. The durable deliverables under `data/<id>/` survive teardown, as before; keeping a torn-down task's wake history would be its own feature with its own reason, not a side effect of cleanup.
A hold on the id is not removed either, deliberately - it is not task-scoped, so it outlives the row and keeps an unanswered question visible, which is also why `hand spawn` then refuses the id until `hand hold clear` (see "Holds" under "State management").

Behavior (scout task):
1. Check `data/<id>/report.md` exists (the report is the deliverable).
2. Close the herdr tab.
3. Return the worktree to treehouse.
4. Append a completion record to `state/completions.jsonl`.
5. Remove the task's row and `state/<id>.status`.
6. Update `data/dashboard.md`.

Behavior with `--force`:
- Skip steps 1-2 for ship tasks, skip step 1 for scout tasks.
- Still closes herdr tab and returns worktree.

Teardown removes several resources in sequence, and any step can fault, so the command has to be runnable a second time: a resource already released is that step's goal already reached, not an error, and never something `--force` should be needed for.
A tab herdr no longer lists counts as closed.
A worktree already back in its pool counts as returned - that is treehouse's own answer, since `treehouse return` on an already-returned path is a no-op success, and it is not inferred from the path being gone: a returned worktree keeps its pool slot directory, so nothing can tell it from a leased one by looking.
The removals are ordered the same way - the report channel goes before the task's row, since the report removal is the one that can fail on a permissions or I/O fault and doing it first leaves the row, and with it the retry, intact.

#### Completion store

The completion record is appended before the task's row is removed, not after, because the record is derived from the task state that removal would take out from under it. That ordering has to hold under a fault on either side of it, and the two sides fail in different, deliberate directions:

- If the append itself fails, the command returns before the task's row is touched. Nothing was recorded, but the task is untouched too, so the whole command is simply retryable.
- If a later step fails - state removal, the dashboard render - the record already written is not thereby wrong, and it is already durable. Everything it claims (work landed, worktree returned) was independently true earlier in the same run regardless of what a later bookkeeping step does. What a retry does then depends on which step faulted: a failed state removal leaves the task's row in place, so the retry replays the whole command and appends a second, functionally duplicate record - a harmless duplicate traded for never silently losing a completion, on purpose. A failed dashboard render comes after the state removal succeeded, so a retry stops at the task-not-found check with nothing left to re-verify; the completion is on disk and only the dashboard row is left to redo.

`state/completions.jsonl` exists as a sibling to `state/events.log` rather than a share of it: `events.log`'s writer reads the whole file, appends, and rewrites it via a temp-file rename, which is fine for its single long-lived writer (`hand watch`) but loses a line outright if a second process's read-modify-write race lands its rename over the first's. Teardown is a short-lived process that can genuinely overlap a running `hand watch`, so the completion store instead takes a dedicated lock and performs one `O_APPEND` write per record - no read, no rename, nothing for a second writer to clobber.

The store is deliberately uncapped. `data/dashboard.md`'s Recent Completions section caps at 10 entries, but that is a rendering choice (see "Dashboard" below) - the store behind it keeps every record `hand teardown` has ever appended, since it is the durable history that survives `data/dashboard.md` being deleted (atqamz/secondhand#62) or its cap being reached. Each line is a complete JSON object (`id`, `project`, `kind`, `outcome`, `detail`, `torndown_at`), readable without parsing markdown.

Output:
```
teardown fix-login complete
```

Errors:
- Task not found.
- Uncommitted changes in worktree, unless every change is content-identical to the local default branch's tip in both the index and the working tree (without `--force`); the error carries a capped `git status --porcelain` of the worktree.
- PR not merged (without `--force`).
- Ambiguous PR head ref: the task's branch carries several PRs that do not resolve to a single usable winner - no preference tier holds exactly one match, or a merged PR coexists with an open one (without `--force`).
- Report not found for scout task (without `--force`).
- Treehouse return failed (worktree locked, path no pool manages).
- Herdr tab close failed (graceful: warn and continue).
- Completion record append failed (lock or I/O fault): task state is left untouched, so a retry is safe.

---

### `hand merge <id> [flags]`

Merge a task's completed work.

```
hand merge fix-login
hand merge fix-login --squash
hand merge local-task --local
```

Flags:
- `--squash`: squash merge (default for PR merges).
- `--merge`: merge commit instead of squash.
- `--rebase`: rebase merge.
- `--local`: fast-forward merge for local-only tasks (merges task branch into default branch in the project clone).

Behavior (PR merge, default):
1. Read the task's row for the PR URL.
2. Refuse if no PR is recorded.
3. Refuse if the PR is already merged (a no-mistakes gate, or `hand teardown`/`hand status`'s own gate-opened-PR detection, can record a PR `hand merge` never merged itself).
4. Check PR CI status via `gh pr checks`.
5. Refuse if checks are not green.
6. Run `gh pr merge <number> --repo <owner/repo> --squash` (or specified method).
7. Update the task's row with merge status.
8. Run `hand project sync <project>` to fast-forward the project clone.
9. Re-render the dashboard.

Behavior (local merge, `--local`):
1. Read the task's row for worktree and project.
2. Refuse if worktree has uncommitted changes.
3. Determine the task branch from the worktree.
4. In the project clone: `git merge --ff-only <task-branch>`.
5. Refuse if fast-forward is not possible (diverged branches).
6. Update the task's row with merge status.

No derived section carries a merge, so neither path renders on account of the merge itself.
The PR path renders because it runs `hand project sync`, which renders unconditionally like every other mutating command; `--local` runs no sync and so writes no dashboard.

Output:
```
merged fix-login: https://github.com/org/repo/pull/42
```

Output (local):
```
merged fix-login: local fast-forward into main
```

Errors:
- Task not found.
- No PR recorded in task state (for PR merge).
- PR checks not green.
- PR already merged.
- PR has merge conflicts.
- gh merge fails.
- Fast-forward not possible (for local merge).
- Uncommitted changes in worktree (for local merge).

---

### `hand pr <id> <url>`

Record a task's pull request URL. The normal path to a recorded PR is automatic: `hand watch` records it as soon as a single PR URL appears on the task's report channel (see "Report channel"). `hand pr` exists for the worker or supervisory agent to record it explicitly - before the report channel catches it, or when the worker's harness has no way to reach the report channel.

```
hand pr fix-login https://github.com/org/repo/pull/42
```

Behavior:
1. Validate `<url>` matches `https://github.com/<owner>/<repo>/pull/<number>` exactly (anchored, no substring matching - a PR URL feeds `gh pr merge` and `gh pr view` downstream, so a loose match here is a command-injection-adjacent risk).
2. Read the task's row.
3. If the task already has this exact PR recorded, skip steps 5-7 (the URL is already on record, so there is nothing left to validate) but still re-render `data/dashboard.md` before reporting success. This reconciling repeat is why `hand pr <id> <url>` is a sound remedy for every `pr-not-recorded` event: a recording can fail after the task-state write and before the render, and a plain no-op here would exit `0` while leaving the dashboard's PR column empty with no signal left.
   There is no longer a "missing row" case to refuse: the row is derived from the task, so a task that exists has one and a re-render produces it (see "Derived sections").
4. If the task already has a *different* PR recorded, refuse - one task, one PR; correcting a wrong record is a deliberate `hand teardown`/`hand spawn` decision, not something `hand pr` overwrites silently.
5. Resolve the task's project and derive `owner/repo` from the project clone's own `origin` remote (`git config --get remote.origin.url`, not `git remote get-url`, so a local `url.<base>.insteadOf` rewrite never turns a genuine mismatch into a false match).
6. Refuse if the URL's `owner/repo` doesn't match the derived repo slug.
7. Confirm the PR exists via `gh pr view` (network check, 30s timeout) - shape validation in step 1 only proves the URL looks right, not that the PR is real.
8. Write `pr` into the task's row and re-render `data/dashboard.md`.

Steps 5-7 live in `project.ValidatePR` and are the *only* validation path: `hand watch`'s auto-record calls the same function, so a worker-supplied URL can never reach task state on weaker terms than an explicit `hand pr`.

Output:
```
recorded PR for fix-login: https://github.com/org/repo/pull/42
```

Output (reconciling repeat):
```
pr already recorded for fix-login: https://github.com/org/repo/pull/42 (dashboard reconciled)
```

Errors:
- Malformed PR URL (usage error, code `2`).
- Task not found.
- Task already has a different PR recorded.
- Project not registered.
- Cannot derive `owner/repo` from the project clone's origin remote.
- URL's repo doesn't match the project's repo.
- PR not found via `gh pr view` (network error or nonexistent PR).

---

### `hand watch [flags]`

Blocking watcher. Polls herdr agent states and prints actionable events to stdout.
Also logs events to `state/events.log` for crash recovery.

```
hand watch
hand watch --poll 10s
hand watch --until-event --timeout 30m
```

Flags:
- `--poll <duration>`: poll interval when push events aren't available. Default: value from `config/watch-interval`, or `5s`.
- `--until-event`: block until the first events, print them, exit `0`. See "Delivering an event to a supervisory agent" below.
- `--timeout <duration>`: with `--until-event`, give up after this long and exit `4`. Default: no timeout. Without `--until-event` it is a usage error (exit `2`), since a streaming watcher has no completion to bound.

Behavior:
1. List all active tasks from the store.
2. Subscribe to herdr's `agent_status_changed` push events if available.
3. Fall back to polling herdr agent states at `--poll` interval.
4. Classify each state change:
   - `idle-unreported <id>`: agent stopped being busy after working/blocked - herdr reports this as `idle` or `done` interchangeably (see "Agent state" below; hand's polling model observes `done`, essentially always, never `idle`) - but its report channel (see "Report channel") doesn't explain the stop: no report at all, or the last line was still `working`. Any other terminal report (`paused`, `blocked`, `needs-decision`, `done`, `failed`) already explains the stop, so that transition is absorbed silently instead.
   - `blocked <id>: <reason>`: agent reports blocked (herdr-level; herdr gives no free-text reason, so `<reason>` is a fixed string).
   - `failed <id>`: herdr pane died unexpectedly.
   - `stale <id>`: agent hasn't changed state for longer than the stale threshold (default 300s, configurable via `config/stale-threshold`).
   - `parked <id>: <last-report-line> (silent <age>)`: the report channel itself has stopped growing for longer than its bound - independent of `stale`, which only ever watches herdr transitions and has nothing to fire on when herdr registers no transition at all (a pane can sit healthy and quiet forever without one). The bound is chosen by the last classified report line: `done`/`failed` are exempt (already terminal, nothing to wait on), `paused` gets the long bound (`config/parked-paused-bound`, default 3600s) since naming what it's waiting on already explains the quiet, everything else - `working`, `blocked`, `needs-decision`, or no report at all - gets the short one (`config/parked-other-bound`, default 1200s) since that silence is unexplained. Edge-triggered like every other trigger: fires once per silence episode and only refires once the report file actually grows past the mtime it fired for. A parked worker and a crashed one are indistinguishable from the status file alone - both are a report line that stopped moving - so the event carries only the last line and its age and leaves the process check itself to the caller (`hand status <id>`, or the session directly).
   - `pr-merged <id>`: a recorded PR has been merged (checked periodically via `gh pr view`). Announced once ever: the observation is recorded as `pr_merged_observed` on the task after the line is printed, so a restart neither repeats it nor loses it to a crash between the two. The same marker is set outside the watcher when gate-opened-PR detection records a PR that is already merged (see `hand status`), so a watcher that first sees the task after that stays quiet about a merge it never observed.
   - `pr-not-recorded <id>: <url> (<reason>)`: a PR URL a worker embedded in a report line was attempted and the recording did not complete. The token says only that much, for any cause - refused validation, an unregistered or unresolvable project, an unreadable task record, a failed state write, a state write that landed but whose dashboard render failed - and `<reason>` is the underlying error, which is what says which. The whole cause is kept; only its line breaks are not, since `gh`'s multi-line stderr reaches these errors verbatim and an event is one line on stdout, one entry in `state/events.log`, and one bullet on the dashboard (continuation lines would parse back as separate events in both). The fix in every case is a human running `hand pr <id> <url>`: it reconciles, so it either repairs whatever half is missing or fails with the real underlying reason. The kind is deliberately not split by cause, since an enumeration of causes is forgotten the next time a new one appears.
   - `pr-record-unknown <id>: <url> (<reason>)`: the same URL was never attempted, because another command held the task lock at that moment. Whether it ended up recorded is genuinely unknown - the holder may be the `hand pr` recording that very URL - so this event asserts nothing about the outcome and points at `hand status <id>` to confirm, except when the task's own state can't be read, where it names that read failure instead of a remedy that would hit it too. Nothing is announced at all when the lock holder is found to have already recorded that same URL.
   - Both auto-record events are durable on stdout and in `state/events.log` (plus a stderr diagnostic) rather than only a transient stderr line, since the report line is consumed either way. Neither is a Pending Decision - see "Pending Decisions".
   - `working <id>: <note>` / `paused <id>: <note>` / `report-blocked <id>: <note>` / `needs-decision <id>: <note>` / `report-failed <id>: <note>`: a new line landed on the task's report channel, classified per "Report channel" above.
   - `reported-done <id>: <note>` / `done <id>: <note>`: a `done` report line landed; printed as `reported-done` until cross-checked against the task kind's completion evidence (a merged PR for ship, `data/<id>/report.md` for scout), then once as `done` when that evidence lands - which is usually a later tick (see "Report channel").
   - `malformed report <id>: <line>`: a report line didn't match the fixed vocabulary. Surfaced, never dropped, so a typo in the worker's report doesn't silently vanish.
   - Benign events (working herdr transitions, routine transitions): absorbed silently.
5. Print one line to stdout per actionable event.
6. Append each actionable event to `state/events.log` (bounded: keep last 200 lines, rotate on overflow).
7. Re-render `data/dashboard.md`, appending each actionable event to Recent Events.
8. Re-scan `state/` periodically to pick up newly spawned or torn-down tasks.
9. Exit cleanly on SIGINT/SIGTERM.
10. While tailing a task's report channel, a line carrying exactly one PR URL auto-records it if the task doesn't already have one, subject to the same validation `hand pr` enforces; a URL whose recording was attempted and did not complete is surfaced as `pr-not-recorded`, and one the watcher never got to attempt as `pr-record-unknown` (see "Report channel").
11. Every task-state write the poll loop makes - the bookkeeping it owns (`report_offset`, `pr_merged_observed`) and an auto-recorded PR - takes the task lock non-blocking, and is skipped when another command holds it. The poll loop never waits on the **task** lock, because that lock is held across unbounded network and git work (`hand merge` across `gh pr checks`/`gh pr merge`, `hand promote` across a `git push`): waiting on it can stall every other task indefinitely, and `flock` cannot honor a SIGINT/SIGTERM in the meantime. Bookkeeping is re-derivable and simply retries next tick. A skipped auto-record is announced as `pr-record-unknown` - never as `pr-not-recorded`, which covers every attempt that was made and did not complete - except when the lock holder turns out to have recorded that same URL, which is silent.
12. The poll loop *does* wait on the dashboard lock, and that is deliberate. It guards a bounded local read-modify-write with no network call, so a brief tick delay or slightly late shutdown is acceptable - and unlike the task lock's writes, a dashboard write is not entirely re-derivable: the derived sections would be rebuilt by the next render either way, but Recent Events is append-only, so skipping a write silently loses that event. Both locks are always taken task-first, dashboard-second, never the reverse.
13. Per-task bookkeeping is written back only after the tick's events are announced, never before. A marker persisted ahead of its line would, if the process died in between, suppress an announcement nothing can re-derive; a duplicate line is the cheaper failure.

#### Delivering an event to a supervisory agent

Detecting an event and delivering it are separate problems, and the streaming mode solves only the first.
`hand watch` never exits, and a supervisory agent's background-task runner re-invokes the agent when a process *exits*, so a streaming watcher's stdout is a file that is read only if the agent independently decides to look.
"Remember to check the watcher" is not a mechanism, and it is what failed on 2026-07-28.

`--until-event` makes the process exit the delivery:

1. Arm: connect to herdr and probe every active task's pane once, before anything else - both bounded by `--timeout` so a wedged herdr daemon cannot strand the wait past it before the poll loop even starts. Any task that *fails* its probe names it on stderr and exits `5` - never enters the baseline half-armed, since a task invisible to the very first probe has no herdr transition to ever fire on. Losing the race against `--timeout` instead is exit `4`, not `5`: the wait window is simply over, exactly as it is for a timeout in the poll loop, and no single task can be named as the cause the way `5` promises. Full per-tick probe-failure tracking and retry policy during the live poll that follows is a separate concern - see atqamz/secondhand#81.
2. Take the baseline silently: two ticks with stdout discarded. The first seeds tracking for every task, the second consumes whatever a previous watcher left unconsumed on the report channels, since `report_offset` survives a restart on purpose and those lines are new to the poll loop. This second tick is also the first to classify against durable evidence, so a task already `stale` or already `parked` before this process ever started is absorbed here exactly as an already-`done` one is (see "The startup state is never an event" below).
3. Poll. On the first tick that produces any event, write that tick's events to stdout and exit `0`.
4. On `--timeout`, whether it elapses during arming or during the poll, write nothing to stdout, name the elapsed timeout on stderr, and exit `4`.
5. On SIGINT/SIGTERM, the same as a timeout: nothing was delivered, so exit `4`, never `0`.

Each rule below closes one way the `tee` + `grep -m1` wrapper this replaces failed:

- **The startup state is never an event.** Only a change from the baseline exits. A worker that was already `done` when the watcher armed produces nothing on stdout, where the wrapper's `grep` matched it, exited, and left the pipeline half-alive with nobody reading the two real events that followed.
- **Every wake trigger is edge-triggered, `idle-unreported`, `stale`, and `parked` included.** A worker fires one on entering the condition and does not fire again until it leaves and re-enters, so no signal has to be excluded from the trigger to avoid a wake storm - which is how the wrapper came to exclude the exact signal it was built for.
- **Arming itself can fail loudly, distinct from every other exit.** A worker whose pane answers with a failure at arm time is not a quiet fleet (`4`) and not a delivered event (`0`) - it is `5`, naming the worker, because the caller would otherwise wait out the full `--timeout` for a cause it can never see on stdout. An arm probe that instead runs past `--timeout` names no worker and is `4`.
- **The exit code says which happened**: `0` an event was delivered, `4` no event (timeout or signal, wherever the timeout lands), `5` a named task's pane failed its arm-time probe, `1` the watcher itself failed, `2` a usage error. A caller can never read a crash or a quiet window as fleet news.
- **No pipeline for the caller to get wrong.** The exit is the whole mechanism.

Worst-case delay from a real transition to the exit that delivers it is one `--poll` interval (`config/watch-interval`, default 5s) plus that tick's own bounded work - the only unbounded-looking piece, a `gh pr view` check for each task with a recorded, not-yet-confirmed-merged PR, is itself capped at 30s per task and run one task at a time, so a fleet with several such tasks can push a single tick past the poll interval alone, but never past that per-task cap times the count. Once the event is written to stdout, the process exits immediately; nothing after that adds delay.

Baseline events are withheld from stdout only.
They still reach `state/events.log` and `data/dashboard.md`, because the report lines behind them are consumed either way and silently dropping a state change is worse than repeating one.
So the agent's loop is: arm the watcher, read `hand status` and `state/events.log` for current truth, then treat the next exit as the answer to "what changed since I armed".
Anything that lands between one exit and the next arming is in those same two places.

One invocation delivers one wake, and re-arming is the caller's own next step after acting on the exit, which it takes anyway with no human in it.

This covers the awake path only: an exit reaches a session that exists and re-arms.
It has no reach when no session is running, and `hand notify` - the channel that would - is not wired to
anything (see `hand notify` and atqamz/secondhand#80).
So a fleet left entirely unattended still goes unread; the difference is that an attended one no longer does.

#### What survives a `hand watch` restart

**Anything the watcher announces is persisted at the moment it announces it, never re-derived on restart.** Re-deriving is how an announcement gets silently skipped: evidence that lands while the watcher is down makes the restarted process conclude the line already went out. Every fact the poll loop carries across a restart, and which side of that rule it is on:

| Fact | Treatment |
|---|---|
| How far the report file is consumed | Persisted as `report_offset`, after the tick's events are announced. |
| A merge this watcher's own `gh` poll saw | Persisted as `pr_merged_observed`, after `pr-merged <id>` is printed. |
| The verified `done` announcement | Persisted as `done_verified`, after `done <id>` is printed. No dashboard section carries a merge, so `hand merge`'s evidence can appear while the watcher is down. |
| An auto-recorded PR URL | Persisted as `pr` on the task, and every outcome that isn't a silently self-resolving race is announced (`pr-not-recorded` / `pr-record-unknown`) and logged. |
| Last reported state and note | Persisted as `last_report_state` and `last_report_note`, written alongside `report_offset` on the same tick that consumed the line. Re-reading them from `state/<id>.status` on resume instead - the previous behavior - meant re-reading history the durable offset says has already been consumed, and re-derived them from a file `hand promote` deliberately leaves alone, so a promoted ship inherited the scout's last report as if it were its own. They explain a quiet pane (`parked`'s bound is selected by the state, and `hand status` renders the note) and gate the scout's deferred-`done` bookkeeping. |
| The identity of the task being tracked | Re-read as `created_at` and compared every tick: an ID torn down and respawned is a different task, so it is re-seeded from its own state rather than inheriting the previous run's. Inheriting it would suppress the new task's verified `done` forever, since the bookkeeping write-back stamps that inherited `done_verified` onto the fresh row, and would absorb its first unexplained stop. Same hazard as a surviving report channel, one layer in (see `hand teardown`). |
| The same bookkeeping across `hand promote` | Not covered by the identity check above, and the sharper problem: promote rewrites the task row in place, keeps `created_at`, and gives the task a *new herdr pane*. Every cached fact anchored to the old pane is therefore not evidence about the ship at all, however plausible it looks. See "Pane-anchored facts across `hand promote`" below for the field-by-field classification and for how the watcher forgets its cached copies. |
| Current herdr agent status, and the blocked flag derived from it | Re-derived, safely: a live pane property with no durable answer, seeded on first sight without emitting (transitions, not states, are events). A transition that happened while the watcher was down is not announced, but is not lost either: `hand status` shows a quiet pane as `(unreported)` or `(reported: <state>)` from the same report channel, and the stale timer below re-flags the task within one window. |
| Whether the last probe of the pane succeeded | Re-derived, safely: seeded unconditionally true on resume, the same value a first sighting gets, so the first probe after a restart is compared against a clean slate. It gates the once-only `failed` latch and `stale`'s detection, and true is the conservative seed for both - a still-unreachable pane re-announces `failed` on the first tick rather than staying silent. A live `hand promote` reseeds it the same way, and for the same reason; see below. |
| How long the current herdr status has been dwelt in | Persisted as `status_changed_at`, updated whenever a herdr status transition is actually observed - any transition, not only ones that raise an event - and re-seeded from `created_at` until the first one. This is what `stale`'s dwell is measured against. Seeding it from the resume time instead - the previous behavior - erased a real dwell on every restart, and since `--until-event` restarts on every delivered event by design, a fleet busy enough to re-arm faster than the threshold elapses could erase that dwell before it ever completed once, silencing `stale` for exactly the fleet it exists to watch (issue #75's Ruling 1). |
| Which status that dwell clock describes | Persisted alongside it as `status_changed_for`, and the timestamp is trusted only while the two agree. A timestamp on its own cannot prove the dwell it describes is still running: a status observed in a different pane is a new dwell even when it spells the same word, so a mismatch means the transition into the observed status happened at an unknown point since and the dwell can only honestly start now. Without this a restart after a `hand promote` read the restamped `status_changed_at` as the ship's own dwell in whatever status the ship happened to be in. |
| The stale timer's fired latch, as opposed to its dwell above | Re-derived, safely: cleared on every observed transition and reset to unfired on resume, so the worst a restart causes is one duplicate `stale <id>` after a further full threshold past a dwell that already fired once - it never suppresses a genuine re-announcement. The dwell it is measured against is not re-derived; see the row above. A restart is the safe direction, but a `hand promote` in a *live* watcher is not, and the latch is cleared there explicitly; see below. |
| Which silence episode `parked` already fired for | Re-derived, safely, the same way the stale latch above is: `ParkedFiredFor` is not persisted and starts unset on resume. A task already silent past its bound at arm time fires on the baseline's second tick - the same tick that first classifies against durable evidence for every other trigger - and is absorbed into the silent baseline exactly as an already-`stale` or already-`done` task is. What the bound is measured against - the report file's own mtime, or `created_at` for a task that has never reported - is untouched by a restart because neither one is process state to begin with; see "Delivering an event to a supervisory agent". |

Anything added to `TaskState` belongs in this table before it ships.

#### Pane-anchored facts across `hand promote`

`hand promote` keeps the task's `id` and `created_at` but hands it a **new herdr pane**.
Every cached fact anchored to the old pane is invalidated at that moment, so the governing question for each one is not "is it durable" but "was it anchored to the pane".
Both halves have to be dealt with, because neither is sufficient alone: promote clears the durable fields itself rather than leaving it to `hand watch`, since a watcher may not be running at all; and a watcher that *is* running holds an in-memory `TaskState` that passes the `created_at` identity check untouched and would write its cached copy straight back onto the freshly-rewritten JSON on the very next tick.
`forgetPaneScopedCache` is the single place that drops those cached copies.

Pane-anchored, and reset:

| Fact | Why it is not evidence about the ship |
|---|---|
| `done_verified` | The marker belongs to the scout's own verified `done`. The ship has not earned one, and carrying it would leave the ship run unable to ever announce its own, since the write-back only ORs the marker to true. |
| `status_changed_at` / `status_changed_for` | The scout's last observed transition happened in a pane the task no longer has. Carrying it would hand the ship a dwell already grown past `stale`'s threshold before its worker had run for a second. Promote restamps the timestamp to the promotion time and clears the status it was stamped for, which is what makes the ship's first observed status a fresh dwell rather than a resumed one. |
| `last_report_state` / `last_report_note` | The scout's last report describes work in that pane. It selects `parked`'s bound and feeds the scout's deferred-`done` bookkeeping, so an inherited one both mis-bounds the ship's silence and can hand it a `done` it never reported. |
| The `stale` and `blocked` fired latches | Each is what makes its announcement fire only once. A latch surviving the promote silences that announcement for the ship's own pane - the `stale` one until the ship transitions at least once, which a genuinely stuck ship never does. |
| Whether the last probe of the pane succeeded | It gates the once-only `failed` latch - the announcement fires on the true-to-false edge, so a false inherited from an unresolved probe error on the scout's pane swallows the ship's own first failure as no edge at all - and gates `stale` detection off entirely until some probe succeeds. It is reset to true, matching the unconditional true a brand-new task's tracking is seeded with, which is what lets a first sighting's very first probe failure fire `failed` with no grace period. |
| The cached herdr status the next probe is diffed against | The status a transition is measured *from*, so an inherited one invents or erases transitions in both directions: a scout cached as `working` turns the ship's first not-busy probe into `idle-unreported` for a pane never observed working, and a scout cached as `blocked` makes the ship's own `blocked` probe compare equal and never fire at all. It is reset to herdr's `unknown`, which matches neither branch, so the ship's first probe is the baseline a first sighting always is. This is not self-correcting in the same-status case, as was once assumed: equality is exactly what suppresses the announcement. |

Genuinely pane-independent, and carried:

| Fact | Why it survives |
|---|---|
| `report_offset`, and the report channel it indexes | Promote never touches `state/<id>.status`: the report stream is continuous across the promotion and the offset already points exactly where the ship's first line lands. Resetting it would replay the scout's consumed lines, the hazard the durable offset exists to prevent. |
| `pr`, `merged`, `pr_merged_observed` | Facts about the branch and its PR, not about any pane. |
| `created_at` | The task's identity, which promote deliberately preserves - this is one task's lifecycle, not two. |
| The `parked` fired latch | Keyed to the report mtime it fired for, not to a pane, and the report channel is itself carried. The ship's own silence is a new episode against a new mtime, so the latch cannot suppress it. |
| The report mtime `parked` measures silence from | Carried with the report channel, but *floored* at the pane-start instant: `status_changed_at`, or `created_at` before any status has ever been recorded. Promote leaves the scout's last append - and so its mtime - untouched while clearing the `last_report_state` that used to exempt the task from `parked` entirely, so an unfloored mtime would hand a pane seconds old the scout's whole accumulated silence and fire `parked` on it immediately. |

Two properties of the forget rule are load-bearing:

- **The trigger is the task's herdr pane id differing from the one the cache was built against, not a status or a timestamp.** A ship whose first probe happens to read the same status the scout last held raises no transition at all, so a status-conditioned rule misses it. A timestamp-conditioned rule is wrong in both directions: `status_changed_at` is legitimately reseeded to "now" on a resume that finds the observed status no longer matching `status_changed_for`, which is not a promote, and RFC3339 is second-granular, so a real restamp landing inside the same second as this watcher's own last write is invisible. Only the pane id changes exactly when, and only when, the pane changes.
- **It runs on every read of the task, including `syncTaskState`'s re-read under the task lock.** A promote can land after a tick's `state.List` snapshot but before that tick's write-back; writing the cached values back there would erase the restamp, and since that write-back also advances `report_offset`, the report line the tick had already consumed and cached would be lost with no way to re-derive it.

Forgetting the cached status is what makes a ship's first probe a baseline in the same sense a cold start's is - transitions, not states, are events - and a promote is a first sighting in every sense that matters, even though the task id and its tracking state are reused.

Output (stream):
```
idle-unreported dark-mode
blocked dark-mode: needs API key for third-party service
needs-decision fix-login: two ways to fix the race, ask-user found both risky
reported-done fix-login: PR https://github.com/org/repo/pull/42 checks green
stale investigate-crash
parked slow-migration: working: still on the migration (silent 42m)
failed api-refactor
pr-merged fix-login
```

The supervisory agent runs `hand watch` as a background task (via its harness's background-task mechanism) and acts on each printed line.
Streaming that way only reaches the agent if something prompts it to read; `--until-event` is how the watcher reaches it on its own (see "Delivering an event to a supervisory agent").

Event durability: if the supervisory agent's context compacts or the session restarts, events since the last read are in `state/events.log`. The agent can `hand status` to recover current truth and read `state/events.log` for recent history.

Errors:
- Herdr not running (fatal: exit `1`, the reachability probe answering with a failure). Under `--until-event` that probe is additionally raced against `--timeout` so a wedged daemon can't strand the wait, and *losing that race* is exit `4`, not `1` or `5`: the window closed with nothing delivered, which is what `4` means wherever in the process it happens, and stderr names herdr as what it was still waiting on. A signal during the same probe is `4` for the same reason.
- Individual task probe failure (graceful: report as "unknown" state). This graceful handling is the streaming path only; see atqamz/secondhand#81 for the gap it leaves (an unreachable task is never entered into tracking, so it is never stale-checked either).
- `--until-event` reaching its `--timeout`, or being signaled, without delivering an event: a line on stderr and exit `4`, never a silent exit `0`. This covers the timeout elapsing anywhere in arming - the herdr reachability probe as well as the per-task probe sweep - as well as during the poll: the window is over either way, and no one task is at fault.
- `--until-event` failing to arm because a task's herdr pane answers its probe with a failure: names the task on stderr and exits `5`, distinct from both `4` (no event: either arming succeeded and nothing happened, or the window closed mid-arm) and `0` (arming succeeded and something did). Unlike the streaming path's graceful "unknown" above, `--until-event` cannot tolerate an unprobeable task at all: a task invisible to the arm-time probe would never enter `states` and so could never produce the transition the caller is blocking on, silently degrading the wait into a guaranteed timeout. Full per-tick probe-failure tracking and any retry policy during the live poll that follows arming is out of scope here - see atqamz/secondhand#81.

---

### `hand project sync [name]`

Fast-forward project clones to their remote default branch.

```
hand project sync
hand project sync nsr
```

Behavior:
1. For each project (or named project):
   - `git fetch origin` in the clone.
   - If on default branch and clean: fast-forward to `origin/<default>`.
   - If dirty, on non-default branch, or diverged: skip with warning.
2. Prune local branches whose remote tracking branch is gone.
3. Re-render `data/dashboard.md`.

Output:
```
nsr: fast-forwarded to origin/develop (was 3 behind)
yes2infra: skipped (dirty working tree)
```

---

### `hand promote <id>`

Promote a completed scout task into a ship task.
Reuses the existing task ID and brief, acquires a fresh worktree, and spawns a new worker.

```
hand promote investigate-crash
hand promote investigate-crash --harness codex
```

Flags:
- `--harness <name>`: harness for the new ship worker. Default: value from `config/harness`.
- `--model <name>`: model override. Default: the brief's declared `model`, else `config/model`.
- `--effort <level>`: effort override. Default: the brief's declared `effort`, else `config/effort`.
- `--skip-gate-check`: dispatch into a `no-mistakes` project even if its gate is not initialized (see "Gate preflight").

Promote resolves model and effort exactly as `hand spawn` does, against the brief the agent
updated for the ship phase, so a scout brief that declared a tier keeps it through promotion
unless the brief or a flag says otherwise.

Behavior:
1. Validate the task exists and is a completed scout (has `data/<id>/report.md`, herdr pane is not busy - `idle` or `done`, which mean the same thing here, see "Agent state" - or unreachable/dead).
2. Create or update `data/<id>/brief.md` - the agent should update it with implementation instructions before calling promote, referencing the scout report.
3. If the project's mode is `no-mistakes`, run the gate preflight check (see "Gate preflight");
   refuse before acquiring any worktree if it comes back not initialized or unreachable, unless
   `--skip-gate-check` is set.
4. Acquire a fresh treehouse worktree (with collision guard).
5. Acquire the task's herdr tab in the project's workspace - same workspace-create-vs-reuse logic as `hand spawn` step 8, including reusing a freshly created workspace's own root tab instead of leaving it as an orphan.
6. Launch the worker and confirm it started (same as `hand spawn`).
7. Rewrite the task's row in place: `kind` changes from `scout` to `ship`, and `harness`, `model`, `effort`, `worktree` and the `herdr` coordinates describe the new worker. Every field anchored to the scout's pane is reset: `done_verified` to false, `status_changed_at` restamped to the promotion time with `status_changed_for` cleared, and `last_report_state` / `last_report_note` emptied. Every pane-independent field is carried, including `created_at` and the watcher's `report_offset` - see "Pane-anchored facts across `hand promote`", which classifies each of them and covers the matching in-memory cache a live `hand watch` has to drop.
8. Only now tear down the scout's herdr tab and return its worktree; a failure here is a warning, not an error.
9. Re-render `data/dashboard.md`, so the Active Tasks `kind` column reads `ship`.

The scout side is torn down last on purpose: the same rollback contract as `hand spawn` applies up
to step 7, so a promotion that fails partway still leaves the scout's pane and worktree intact
instead of stranding the task with nothing to look at.
The render is last of all, after that teardown, so a render fault cannot strand the scout's pane and
worktree behind a promotion the store has already recorded.

Output:
```
promoted investigate-crash: scout -> ship project=nsr harness=claude
```

Errors:
- Task not found.
- Task is not a completed scout.
- Scout report not found.
- `no-mistakes` gate not initialized, or the binary missing/not runnable, for a `no-mistakes`-mode
  project (same two distinct errors as `hand spawn`; see "Gate preflight"). Skipped entirely with
  `--skip-gate-check`.
- Worktree or herdr errors (same as `hand spawn`).

---

### `hand notify <message>`

Send an out-of-band notification. Uses the command template in `config/notify`.

```
hand notify "fix-login PR is ready for review"
```

Behavior:
1. Read `config/notify`. If absent, print to stdout only and return.
2. The notify config contains a shell command template. The message is available as the `$HAND_MESSAGE` environment variable.
3. Execute the command with `HAND_MESSAGE` set in the environment.
4. Print the message to stdout regardless.

Example `config/notify`:
```
curl -s -X POST "https://api.telegram.org/bot$TELEGRAM_TOKEN/sendMessage" -d "chat_id=$TELEGRAM_CHAT&text=$HAND_MESSAGE"
```

Or for macOS:
```
osascript -e "display notification \"$HAND_MESSAGE\" with title \"secondhand\""
```

Nothing calls `hand notify` yet: it is operator-invoked only, and no watcher code path reaches it.
Nothing writes `config/notify` either - `hand init --setup` covers `harness`, `model`, and `effort` - so an unconfigured
notify prints its message and exits 0 having sent nothing.
The channel is therefore implemented and dark, and it is the only path that would reach the user when no supervisory
session is awake, since `hand watch --until-event` delivers by exiting into a session that must be there to be woken.
Wiring it is atqamz/secondhand#80.

Output:
```
notified: fix-login PR is ready for review
```

Errors:
- Notify command failed (warn and continue - notification failure never blocks work).

---

### `hand search <query> [flags]`

Full-text search the prose corpus under `data/`.

```
hand search login auth decision
hand search --json "no-mistakes gate"
hand search --rebuild deploy failure
```

Flags:
- `--json`: output as JSON, one object per hit with `Path`, `Title` and `Snippet`. An empty result is `[]`, never `null`, so a caller can iterate without special-casing it.
- `--rebuild`: discard and re-derive the index before searching. The recovery for an index that is present but wrong; an index that is simply *missing* needs no flag.
- `--limit <n>`: maximum hits, default 20.

Behavior:
1. Scan `data/` for markdown files, comparing each against the index by mtime and size, and index what changed. Refreshing on every query rather than on a schedule keeps every other command free of the index entirely: the index is derived, so a stale answer is always settled by the corpus, and nothing else in `hand` has to know the index exists.
2. Match against the FTS5 index, ranked by bm25, and print `path`, `title` and a snippet per hit.
3. With no hits, print nothing to stdout and say `no matches for "<query>"` on stderr, so an empty result reads differently from a search that never ran while a pipeline still reads nothing.

Every whitespace-separated token in the query is quoted before it reaches FTS5, so a query a supervisor would actually type - `no-mistakes gate`, `atqamz/secondhand#53` - is matched as the literal text it looks like rather than parsed as query operators.

`data/dashboard.md` is deliberately excluded from the corpus: it is rendered from machine state, so indexing it would answer prose searches out of a cache of the database the search exists to complement.

The index lives in its own database at `state/index.db`, separate from machine state, and is safe to delete at any time (see "Machine state and the prose corpus"). Neither the search nor the rebuild reads `state/hand.db`.

Errors:
- Corpus unreadable (the rebuild names the file it could not read).
- Index unusable, past what a refresh repairs - the condition `--rebuild` exists for.

Both are general errors (`1`), never usage errors: the operator typed the command correctly and the fault is in the world.

---

### `hand doctor`

Report-only check of the resolved fleet home's `AGENTS.md` for perishable content and generated-block drift. Fixes nothing; a human or agent reads the findings and edits the file.

```
hand doctor
```

Behavior:
1. Resolve the fleet home (same resolution as every other command; a `hand doctor` outside one is the same precondition failure as elsewhere).
2. Scan `AGENTS.md` line by line, tracking fenced code blocks and the `hand:generated` span (see "AGENTS.md (target)"), and flag:
   - a date (`YYYY-MM-DD`) outside the generated span, since a date only stays true as long as the day it names,
   - self-expiring phrasing outside the generated span - `until #N lands`, `once #N lands`, `awaiting` - the same shape of problem as a bare date,
   - an em dash or emoji anywhere in the file, generated span included,
   - the generated span's content having drifted from `internal/agentsmd`'s `generatedBody`.
3. Print one line per hit to stdout, prefixed with the resolved fleet home's absolute path to `AGENTS.md` (the same absolute-path rule this PR adds to `generatedBody` - a bare `AGENTS.md:12:` is ambiguous once more than one fleet home is in scope) - `<path>:<line>: <finding>` for a line-anchored finding, `<path>: <finding>` for the whole-file drift check - and exit `1` if anything was found, `0` if the file is clean.

A date or self-expiring phrase inside inline code (`` `...` ``) or a URL is not flagged: a changelog entry or an example command legitimately names a date or says "awaiting" without going stale, since it is documenting a fixed past event or literal text rather than making a claim about the present.

A missing `AGENTS.md` is not an error: same as a directory that is not a fleet home at all, `hand doctor` finds nothing to flag and exits `0` silently, leaving `hand init` to be the one place that complains about an incomplete fleet home.

---

## Dashboard: `data/dashboard.md`

The dashboard is secondhand's answer to the session clobbering problem.
Instead of dumping fleet state into the agent's context on every session start, `hand` maintains a persistent markdown file that both the agent and the user can read.

Every `hand` command that changes what the dashboard shows rewrites `data/dashboard.md` (see "Update rules").
The agent reads it at session start.
The user can watch it in a side-by-side editor for real-time fleet visibility.

**The dashboard is a rendering, not a record.**
Every section that can be derived - Active Tasks, Pending Decisions, Projects - is rebuilt from the store and the report channel on every write, never patched into the previous rendering.
The two append-only logs, Recent Events and Recent Completions, are the only things read back out of the file, because nothing else holds them.
This is what closes atqamz/secondhand#53's four accuracy defects at the source rather than one write path at a time: a row cannot go stale, disagree with `hand status`, or outlive the state behind it, because it is not stored anywhere to go stale in.
It also means a hand-edited dashboard is overwritten on the next write, and that a caller which only reads may render it too - the result depends on the store, not on which command got there first.

### Format

```markdown
# Dashboard

Updated: 2026-07-24T12:30:00Z

## Active Tasks
| id | project | kind | state | age | pr |
|---|---|---|---|---|---|
| fix-login | nsr | ship | working | 2h | - |
| dark-mode | nsr | ship | blocked | 45m | #43 |

## Pending Decisions
- dark-mode: worker needs third-party API key (blocked 45m)

## Recent Events
- 12:30 done fix-login
- 12:15 pr-merged audit-deps: https://github.com/yes2games/nsr/pull/40
- 11:45 blocked dark-mode: needs API key for third-party service

## Recent Completions
- fix-typo: nsr | ship | merged | PR #40
- audit-deps: nsr | scout | done | report data/audit-deps/report.md

## Projects
- nsr: direct-pr | 3 active tasks
- yes2infra: no-mistakes | 0 active tasks
```

### Update rules

Only the append-only sections have per-command rules, because only they carry anything a command has to supply:

| Command | Appends |
|---|---|
| `hand teardown` | One Recent Completions entry (keep last 10) |
| `hand watch` | One Recent Events entry per actionable event (keep last 20) |
| every other command | Nothing to append; whether it writes at all is below |

A mutating command re-renders the whole file, and the derived sections follow from the store and the report channel wherever the write came from.
It re-renders unconditionally, never after testing whether its own effect reached a section: `hand promote` changes the `kind` the Active Tasks row derives, while `hand project sync` and `hand merge`'s PR path may leave every derived value identical, and a command that had to know which case it was in would be deciding from outside the render what the render already answers.
Five commands still write nothing at all: `hand send` and `hand notify` reach a worker without touching the store, `hand hold set` and `hand hold clear` mutate only the `hold` table, which no section of this file derives from (see "Holds" under "State management"), and `hand merge --local` writes only a merge flag no section reads and runs no project sync.

Recent Completions is a capped view, not the record of truth: every entry `hand teardown` moves here also lands, uncapped, in `state/completions.jsonl` first (see "Completion store" under `hand teardown`), so the 10-entry cap loses nothing that store still has.

### Derived sections

**Active Tasks** is one row per task in the store. The `state` column is the task's last classified report line - `working`, `paused`, `blocked`, `needs-decision`, `done`, `failed` - or `unreported` when the worker has written nothing that classified, or `unreadable` when its report file exists but can't be read.
`unreadable` is the same distinction `hand status` draws with ` (report unreadable)`, and for the same reason: an I/O fault is not evidence the worker never reported, and reads fail open, so one unreadable report costs its own row's state and not the whole render.
It speaks the report vocabulary and nothing else.
It is deliberately not a live herdr probe and not an event kind: a probe would give the column a second source that disagrees with `hand status`, and an event kind put watcher-internal spellings like `idle-unreported` and `pr-not-recorded` in a column that is meant to say what the *worker* said.
A pane observation that has no report behind it belongs in Recent Events, where it is a thing the fleet saw rather than a thing the worker claimed.

**Pending Decisions** holds the questions a worker is waiting on an answer for: a `blocked:` or `needs-decision:` line, in the worker's own words, one entry per task.
Nothing else may appear there.
Inferring an entry from an idle or blocked pane - the old `stopped, reason unknown` and `parked` entries - is what crowded the genuine questions out of the section in atqamz/secondhand#53, and those observations now surface only in Recent Events, on `hand watch`'s stdout and in `hand status`.
Which of `blocked` and `needs-decision` a worker reached for does not change who has to act, so both belong.

An entry is retired by replaying the append-log, not by remembering a flag: the section holds the question a worker is *still* waiting on, which is not the same as its last line.
`needs-decision:` followed by `paused: sleeping on it` leaves the question open, because a worker that parks on something has not resolved it.
`working:`, `done:` and `failed:` retire it - the worker resumed, finished, or gave up.
Replaying beats remembering here for the same reason the whole section is derived: a latched flag in machine state is how a cleared question used to survive the line that cleared it, and the log already holds the whole answer.

The section needs no cap: one entry per task, and a task's entry disappears with the task, so it is bounded by the live fleet.

**Projects** is one row per registered project with its mode and its live active-task count.

### Optional: qmd for semantic search

`data/` grows large over time (400+ files, 4MB+ in real usage - briefs, reports, decisions accumulate).
The dashboard solves "what's happening now".
For "what did we decide about X three months ago", searching hundreds of markdown files by hand is tedious.

`hand search` covers the keyword half of that in the binary, with no dependency and no setup (see its command section).

[qmd](https://github.com/tobi/qmd) is a local search engine for markdown that adds what `hand search` deliberately does not do: semantic and hybrid search over embeddings.
Secondhand recommends it but does not require it:

- `hand init --setup` does not require or configure qmd.
- If qmd is available, the agent can create a collection pointing at `data/` manually.
- The AGENTS.md sketch mentions `qmd search` as a way to find historical context, alongside `hand search`.
- All `hand` operations work without qmd. The agent can always fall back to `hand search`, or to reading files directly.

```sh
# user installs qmd separately
npm install -g @tobilu/qmd     # or: nix shell nixpkgs#qmd

# secondhand suggests indexing
qmd collection add data/ --name secondhand
qmd context add qmd://secondhand "Task briefs, scout reports, decisions, and backlog history"
qmd embed

# agent searches historical context
qmd search "login auth decision" --json
qmd vsearch "how did we handle the deploy failure" -c secondhand
```

qmd is not a dependency. It's a recommendation for users with growing data/ directories.
The agent should use it when available and read files directly when not.

## Harness launch templates

Each supported harness has a launch command template.
`hand spawn` constructs the command, `cd`s into the worktree, and sends it to the herdr pane.

Every template must launch its harness **interactively**, never headless/one-shot.
`hand send` writes into a running pane, `hand watch` polls pane state to classify a worker as
working/blocked/idle, and the `no-mistakes` delivery mode drives many turns as the worker
responds to review/test/document/lint gates.
A one-shot process (`claude --print`, `opencode run`) answers once and exits: there is nothing
left to send to, classify, or drive through a gate.
Any harness template added here must stay resident across the whole task, and must have its
autonomy/permission flag set so an unattended worker does not stall on a permission prompt.

### Claude Code

```sh
cd <worktree> && CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --dangerously-skip-permissions "Read the brief at <brief-path> and carry out the task it describes."
```

The brief path is included in the prompt because Claude Code takes prompt text, not a file path.
When configured, `--model <name>` and `--effort <level>` are inserted before the prompt.
Claude is the only harness with an effort flag: `opencode` takes `--model` but no effort, and
`codex`, `grok` and `pi` take neither (`harness.SupportsEffort`).
When the brief carries a `---` declaration, the prompt gains a sentence disclaiming it as dispatch
metadata (see "Brief format").
`--dangerously-skip-permissions` is required so the unattended worker does not stall on a
permission dialog.
`CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false` suppresses the dim predicted-next-prompt ghost text
Claude Code renders while idle; without it, a supervisor reading the pane can misread ghost text
as the worker having typed input.

Interactive launch has first-run dialogs that headless `--print` skipped:

- The workspace trust dialog. Claude Code only trusts a directory whose path or one of its
  ancestors has been accepted before, and every treehouse worktree is a fresh path under the
  pool root (`~/.treehouse/...`), so this dialog appears on every spawn, not just a fresh host.
- The bypass-permissions disclaimer, a one-time global accept that `--dangerously-skip-permissions`
  is gated on.
- The managed-settings security dialog ("Managed settings require approval"), shown when this
  host has managed settings configured by the organization's IT administration. It has nothing
  to do with the checked-out repository: accepting it grants arbitrary code execution and prompt
  interception for every run on the host, so it is recognized but deliberately not answered. The
  operator accepts it once on the host, then respawns.

Their signatures (`internal/harness`) must stay case-sensitive and keep their distinguishing anchors
- `Bypass\s+Permissions\s+mode`, not a bare `bypass permissions` - because Claude Code's status line
permanently contains the string `bypass permissions`, so a case-insensitive or unanchored loosening
would match a dialog in every pane forever and break every spawn.

`hand spawn` and `hand promote` clear the answerable ones automatically. After sending the launch
command, each polls the pane. Whether a worker is running is herdr's answer, not the screen's:
herdr reports an agent on a pane only while a harness process is in its foreground, so a harness
that painted a dialog and then exited leaves the text behind but no agent, and is never mistaken
for a started worker. That labeling is verified empirically for claude and opencode, each run in a
real pane and observed being labeled. For codex, pi and grok it rests on herdr's shipped agent
detection manifests, read but not exercised, because no binary for those is installed on this host.
Pane text is used only to spot dialogs (`internal/harness`'s `FirstRunPromptsFor`): a known one is
answered, and success needs the pane to hold a live agent and stay free of both known dialogs and
the harness's generic unrecognized-dialog fallback for the settle window. A harness's readiness
signature is a secondary shortcut - on a pane already holding a live agent, the harness's own paint
means there is nothing left to settle for.

That text is read from the pane's recent scrollback (`pane read --source recent`), not its visible
viewport, because a pane in an unattached herdr session is too short to show a whole dialog - 23
rows against 61 in an attached one - and what it clips is the lower half, where the option and
footer lines that identify a dialog live.

Reading scrollback rests on a measured premise: Claude Code erases an answered first-run dialog in
place rather than scrolling it away, so a recent-scrollback read does not carry answered dialogs
forward. Measured on 2026-07-26 against a real `hand spawn`ed worker pane on the Claude Code version
installed on this host, reading 200 lines of retained scrollback: no trust-dialog, bypass-disclaimer
or `Enter to confirm` text remained anywhere in it. A read that still matches a catalogued dialog is
therefore treated as that dialog still being up, and the launch runs out its poll window rather than
being confirmed. If that premise stops holding on a later Claude Code version, spawn fails on the
deadline instead of confirming a healthy worker. That direction is chosen, not an oversight: a wrong
deadline failure is loud and fixable, while confirming an unread dialog is issue #28 itself.
Independently of the read, each catalogued dialog is answered at most once per launch, so retained
text can cost a timeout but can never send a second round of keys into a live agent's composer.

Two outcomes are not success. A pane with no agent, or one still showing a dialog, when the poll
window elapses fails the spawn/promote with that pane content and what held it up; for a harness
whose agent detection has not been exercised, that failure names the unexercised detection first
and the possibility of a harness that exited on a dialog second, since an unrecognized process is
the likelier cause. A recognized-but-refused dialog fails immediately, naming what a human has to
accept. See `cmd/launch.go`'s `confirmLaunch` for the polling/timeout values and why they were
chosen.

A harness with no catalogued signatures at all is confirmed on agent presence alone, so an agent
parked on a dialog hand cannot recognize is reported as started. That is a known, accepted gap, and
the reason the catalogue matters for every harness added, not only claude.

### Codex

```sh
cd <worktree> && codex --file "<brief-path>"
```

Unverified: no `codex` binary was available to check `--help` against.
Confirm this launches interactively (not one-shot) before relying on it; the template above
predates that requirement and may need an autonomy flag and a different invocation shape.

### Grok

```sh
cd <worktree> && grok --trust --file "<brief-path>"
```

Unverified, same caveat as Codex above.

### Pi

```sh
cd <worktree> && pi "<brief-path>"
```

Unverified, same caveat as Codex above.

### OpenCode

```sh
cd <worktree> && OPENCODE_CONFIG_CONTENT='{"permission":{"*":"allow"}}' opencode --prompt "Read the brief at <brief-path> and carry out the task it describes."
```

The bare `opencode` command opens its interactive TUI, unlike `opencode run`, which is
explicitly headless and exits after one reply.
`OPENCODE_CONFIG_CONTENT` grants blanket tool permission so the unattended worker does not
stall on a permission prompt.
When configured, `--model <name>` is inserted; the bare command has no effort/variant flag, so
`--effort` has no effect on OpenCode workers.
The bare command also has no `--file` flag, so the brief path is embedded in the prompt text
instead of attached.

The Claude and OpenCode forms above were verified against the installed CLI versions.
Codex, Grok, and Pi retain unverified templates until those binaries are installable; whoever
verifies them must confirm interactive (not headless) launch, not just flag names.
The harness module is the single place that constructs these commands.

## Herdr integration detail

### Connection

`hand` connects to herdr via its CLI (`herdr`) and/or its HTTP API.
The herdr server must be running before any `hand` operation that touches tabs/panes.

### Workspace and tab model

- One workspace per project. Workspace label = project name.
- One tab per task within the project workspace. Tab label = task ID.
- The supervisory agent's own session is a separate herdr workspace (or the user's own terminal).

### Agent state

herdr tracks agent state per pane, with five real values: `working`, `idle`, `blocked`,
`done`, `unknown`. `working` and `blocked` mean what they say. `unknown` is herdr's
own degrade-gracefully value. `idle` and `done` are where the two-value mental model
("idle: waiting for input" / "done: work is finished") that shipped in an earlier
version of this doc turned out to be wrong, and produced the bug tracked as #30.

`done` does not mean the task is done. It is herdr's own notification bookkeeping:
when a pane goes from working (or blocked) to not-busy, herdr reports the transition
as `idle` only if a live, OS-focused herdr client currently has that pane's tab
active at the instant of the transition (its internal `seen` flag); otherwise it
reports `done`. `hand` polls the API and never focuses a client on a worker's pane,
so it observes `done`, essentially always, for this transition - never `idle`.

The corollary: for `hand`'s headless deployment, `done` versus `idle` carries no
task-outcome information at all, only whether a human happened to be looking. Which
means the report channel (see "Report channel") is the *only* source of task outcome,
not a supplement to herdr's status. `hand` treats `idle` and `done` identically -
both just mean "the pane stopped being busy" - and never infers completion from
either one alone.

`hand status` queries this directly.
`hand watch` subscribes to state changes.

### Operations

| hand command | herdr operation |
|---|---|
| `hand spawn` | create workspace (if needed, at the worktree's cwd, reusing its own root tab as the task tab) or create tab in an existing workspace + send launch command + poll pane state and read pane text until the worker is confirmed started, sending keys to answer first-run dialogs |
| `hand status` | get agent state for pane |
| `hand send` | check composer empty + send keys to pane |
| `hand teardown` | close tab (+ close workspace if empty) |
| `hand watch` | subscribe to agent_status_changed events, or poll pane states |

### Herdr CLI calls

```sh
# list workspaces
herdr workspace list

# create workspace without focusing it - herdr cannot create an empty workspace, so this always
# creates a root tab and pane too, at --cwd; hand points --cwd at the worktree (not the clone) and
# reuses that root tab as the first task's tab (see "herdr tab rename" below) rather than
# discarding it and creating a second tab, which would leave it behind as an orphan shell
herdr workspace create --no-focus --cwd <worktree> --label <project-name>

# create tab in an already-existing workspace
herdr tab create --workspace <ws-id> --no-focus --cwd <worktree> --label <task-id>

# rename a tab - used to turn workspace create's own root tab into the first task's tab
herdr tab rename <tab-id> <task-id>

# list tabs in workspace
herdr tab list --workspace <ws-id>

# get pane and agent state
herdr pane get <pane-id>
# returns agent: detected harness name, empty when no harness runs in the pane
# returns agent_status: "working" | "idle" | "blocked" | "done" | "unknown"

# read the pane's recent scrollback as plain text
herdr pane read <pane-id> --source recent --lines <n>

# run a command in pane
herdr pane run <pane-id> <command>

# send text and submit it
herdr pane send-text <pane-id> <text>
herdr pane send-keys <pane-id> Enter

# close tab
herdr tab close <tab-id>

# close workspace (if empty)
herdr workspace close <ws-id>
```

These calls and their responses were verified against the installed herdr version.
Responses come in two shapes, and the client validates each one differently:

- **Query commands** print a JSON envelope carrying a non-null `result` object; a missing or null
  result is an error.
  `tab close` and `workspace close` belong here too: they answer with a `{"type":"ok"}` result
  rather than staying silent.
- **Void commands** (`pane run`, `pane send-text`, `pane send-keys`) print nothing on success.
  A failure prints a JSON error envelope whose exit code cannot be trusted on its own, so any
  non-empty body is parsed for that envelope before the exit status is consulted.
- **`pane read`** is a third shape: plain pane text on success, and on failure a bare
  `{"code","message"}` object rather than the `{"error":{...}}` envelope the other two shapes use.

The herdr client abstracts these into Go function calls; `internal/herdr` keeps one entry point
per shape and is the source of truth for which command uses which.

## No-mistakes integration

No-mistakes is an external Go binary.
Secondhand does not wrap it.
The worker uses `no-mistakes` directly in the worktree:

- **Initialization:** `hand project add --mode no-mistakes` runs `no-mistakes init` in the clone. Treehouse worktrees inherit the gate.
- **Validation:** The worker runs `no-mistakes axi run` in its worktree. `axi` is no-mistakes' built-in agent interface (non-interactive, token-efficient output). It is not a wrapper tool.
- **Gates:** When `no-mistakes axi run` parks at a gate (review approval, fix review), it prints the gate state. The worker reads it and either resolves it (`no-mistakes axi respond`) or reports blocked to the supervisory agent via herdr state.
- **Status:** `no-mistakes axi status` shows the current run state.
- **Abort:** `no-mistakes axi abort` cancels an active run.

Secondhand's only no-mistakes awareness:
- `hand project add` initializes it when `--mode no-mistakes`.
- The ship brief template mentions the no-mistakes workflow when the project mode is `no-mistakes`.
- `hand spawn` and `hand promote` ask `no-mistakes status` before dispatching into a `no-mistakes`
  project, refusing if the gate is not initialized for that repo (see "Gate preflight" below). This
  is a read-only status check, not driving the pipeline: `hand` still never calls `axi run`,
  `axi respond`, or `axi abort`. The worker does.

### Gate preflight

`no-mistakes`'s own state is keyed on the absolute `working_path` of the repo it was initialized
against. Two real histories orphan that row without either `hand` or `no-mistakes` reporting it
anywhere: the fleet home gets renamed (moving every project's clone path at once), or a project is
registered with `--mode no-mistakes` but `no-mistakes init` is never run against it. Both leave a
`no-mistakes`-mode project silently ungated - a worker's `axi run` either fails deep into the task
or, worse, is never invoked at all - with nothing obliging anyone to notice.

`hand spawn` and `hand promote` each run a preflight check before dispatching into a `no-mistakes`
project: `no-mistakes status`, run inside the project's clone. `no-mistakes status` always exits 0,
whether or not the repo is initialized, and reports both of the histories above with the identical
text `repo not initialized (run 'no-mistakes init' first)` - there is no way to distinguish a
never-initialized repo from a stale renamed one from the outside, so the preflight does not try.
The outcome is read from that text, never from `~/.no-mistakes/state.sqlite` directly,
which is another tool's private schema.

Three outcomes:
- **Initialized:** proceed.
- **Not initialized** (either history): refuse with exit code 3, naming the exact remedy verbatim -
  `no-mistakes init` is idempotent and repairs a stale `working_path` in place, so the message reads
  `no-mistakes gate not initialized for project "<name>", run: cd <clone-path> && no-mistakes init`.
- **Binary missing or not runnable:** refuse with a distinctly different message (`no-mistakes
  binary not found or not runnable: <error>`), never collapsed into "not initialized" - the remedy
  for a missing binary is not `no-mistakes init`. This is a general error (exit code `1`), not a
  precondition: the world is not in a state the operator can fix by initializing anything.

Escape hatch: `--skip-gate-check` on both `hand spawn` and `hand promote` bypasses the preflight
and prints a warning to stderr naming the project, so bypassing it is visible in the transcript
rather than a silent env var.

`hand project list` runs the same check for every `no-mistakes`-mode project and appends `(gate:
not initialized)` or `(gate: unreachable)` to its output line (and `"gate_issue"` in `--json`
output) when the check doesn't come back clean, so a stale or never-initialized gate is visible
without waiting for a spawn or promote to refuse.

## Brief format

`data/<id>/brief.md` is written by the supervisory agent before `hand spawn`.
`hand spawn` validates it exists but does not generate or modify it.

The brief is freeform markdown. The agent writes whatever the worker needs.
Recommended structure:

```markdown
# Task: <short description>

## What to do
<clear description of the task>

## Acceptance criteria
<what "done" looks like>

## Constraints
<anything the worker should know: don't touch X, use library Y, etc.>

## Context
<relevant background, links, prior investigation findings>
```

There is no template engine, no placeholder substitution, no generated sections.
The supervisory agent is an LLM - it writes good briefs naturally.
Removing template machinery removes a maintenance burden and failure mode.

### Declared model and effort

A brief may open with a `---` fenced block declaring the tier the task should run at:

```markdown
---
model: claude-opus-5
effort: high
---

# Task: <short description>
```

Both keys are optional, and the block is only recognized when `---` is the brief's very first
line. `hand spawn` and `hand promote` read it (`internal/brief`) and resolve model and effort as
flag, then declaration, then config default, then unset. The brief is the durable statement of
scope, so a respawn or a promote picks the declaration up again instead of falling back to
`config/model`.

The parser is deliberately forgiving, unlike `data/projects.md`'s registry parser: unknown keys
inside the block are ignored, a value written as a YAML quoted scalar (`model: "claude-opus-5"`)
declares the same tier as the bare form, and a brief it cannot scan (an unterminated fence, an
enormous pasted line) is read as having no declaration rather than failing the spawn. A brief is
prose that happens to carry two optional settings, not a config file that happens to contain prose.
Model names are not validated against a list, which would rot the first time a model ships.

The declaration is dispatch metadata, not task content. The worker opens the brief itself, so the
launch prompt gains one sentence marking the block's `model` and `effort` keys as dispatch metadata
when a block is present; anything else the block carries is left to the worker to read, and the
brief on disk is never rewritten or stripped. Only the prompt-bearing harnesses carry that
sentence: `codex`, `grok` and `pi` are handed the brief as a file with no prompt at all, so a
declaring brief reaches them undisclaimed.

A declared effort under a harness that cannot apply one warns on stderr (see `hand spawn`).

## Backlog format

`data/backlog.md` is a plain markdown file edited directly by the supervisory agent.

```markdown
# Backlog

## Queue
- fix-login: nsr - fix flaky login test
- dark-mode: nsr - add dark mode toggle (depends: fix-login)
- api-cache: yes2infra - add API response caching (after: 2026-08-01)

## In Progress
- investigate-crash: nsr - investigate production crash reports | scout

## Done
- fix-typo: nsr - fix README typo | PR https://github.com/yes2games/nsr/pull/40
- audit-deps: nsr - audit outdated dependencies | report data/audit-deps/report.md
```

Conventions:
- Each item: `- <id>: <project> - <description> [| <artifact>]`
- Dependencies: `(depends: <other-id>)` in the description.
- Date gates: `(after: <date>)` in the description.
- Artifacts: `| PR <url>` or `| report <path>` or `| scout` suffix.
- The agent moves items between sections as work progresses.

`hand` reads this file for:
- `hand spawn` can optionally warn if the task ID isn't in the backlog (non-blocking).
- `hand teardown` can optionally update Done items (but the agent can do this itself).

The backlog is the agent's document. `hand` is a consumer, not an owner.

## Project registry format

The registry is machine state, so the store owns it and `data/projects.md` is its projection, rewritten by `hand project add/remove`.
The file is still what an operator reads, so the projection preserves the file: comments, prose and ordering survive a rewrite, a re-registered project keeps its place, and a new one goes at the end.
Hand-editing it is not the way to register a project - the next write rewrites the file from the store - and a pre-sqlite file is imported once (see "Migration").

```markdown
# Projects

- nsr: https://github.com/yes2games/nsr mode=direct-pr
- yes2infra: https://github.com/yes2games/yes2infra mode=no-mistakes
- secondhand: local mode=local-only
```

Fields:
- `<name>`: project identifier, used in all `hand` commands.
- URL or `local`: git remote URL or `local` for repos without a remote.
- `mode=<mode>`: delivery mode.

Delivery modes:
- `no-mistakes`: worker runs no-mistakes pipeline, ships via PR with validation evidence. `hand
  spawn` and `hand promote` refuse to dispatch into a `no-mistakes` project whose gate is not
  initialized (see "Gate preflight").
- `direct-pr`: worker pushes branch and opens PR directly.
- `local-only`: worker commits to a local branch. Merging into default branch via `hand merge --local`.

## State management

### Design decisions

- **sqlite, not one JSON file per task.** Machine state lives in `state/hand.db` (see "Machine state and the prose corpus"). One row per task and per project, queried rather than globbed, with the whole registry consistent at every read. Pre-sqlite `state/<id>.json` files are imported once and moved to `state/migrated/`; see "Migration" below.
- **Current state, not append-only logs.** One row per task, updated in place. History comes from the dashboard, events.log, and herdr event streams, not from accumulating status lines.
  `state/completions.jsonl` is a deliberate exception, not a contradiction: it does not track a task's current state, which the store already owns and which teardown deletes outright. It is durable history of a state that no longer exists, kept precisely because the dashboard's own history (Recent Completions) is capped and scheduled to disappear with `data/dashboard.md` itself (atqamz/secondhand#62) - the log this principle warns against is one substituting for state that should be current, not one recording state that is gone for good.
- **Nothing durable is derived from a rendering.** Every view - the dashboard's derived sections, `hand status`, the watcher's classification - is computed from the store and the report channel at the moment it is asked for. Reading a previous rendering back in as evidence is what produced atqamz/secondhand#53's accuracy defects, and no code path does it any more.
- **No separate status files for herdr-visible state.** The worker's herdr-visible state (working/idle/blocked/done/unknown) is queried from herdr in real-time, not persisted by `hand`. The store tracks static metadata (project, worktree, harness, PR URL), not dynamic agent state.
  The one exception is `state/<id>.status`, the worker-to-supervisor report channel (see "Report channel" below): herdr's agent state answers "is the pane busy," not "why did it stop" or "what actually happened," and that gap is exactly what caused done/blocked/needs-decision to go unreported in production. This file is not a second copy of herdr's state - it's a channel for information herdr has no way to carry, and it exists only because that specific gap caused a real incident, per principle 5 (no feature without friction).
  This is the strongest argument for the whole design: herdr's `idle`/`done` split (see "Agent state") carries no task-outcome information at all for `hand`'s headless deployment, only whether a human happened to be looking at the time. The report channel isn't a supplement to herdr's status for learning how a task ended - it's the only source of that information there is.
- **Event log for crash recovery.** `state/events.log` is a bounded rotating log (last 200 lines) of actionable watcher events. Not for real-time consumption - the watcher prints to stdout for that. The log exists so a restarted agent can read recent history that happened while its context was down.
- **Holds are their own table, not a task column.** See "Holds" below for the full reasoning: a task-scoped hold would be destroyed by `hand teardown` exactly when it matters most.

### Report channel

`state/<id>.status` is an append-only text file the worker writes and `hand` only ever reads.
The brief the supervisory agent writes for a worker must include this file's absolute path and the vocabulary below, so the worker knows to append to it.
It lives and dies with the task: `hand teardown` removes it alongside the task's row, so an ID respawned later starts with an empty channel rather than inheriting the previous run's log (see `hand teardown`).

Each line has the shape `<state>: <note>`, one state transition per line:

```
working: added the retry loop
needs-decision: two ways to fix the race, ask-user found both risky
done: PR https://github.com/org/repo/pull/42 checks green
```

Fixed vocabulary (anything else is malformed, and malformed lines are surfaced, never silently dropped):

- `working`: the worker is actively making progress. `<note>` is a short description of what it's doing.
- `paused`: the worker stopped without being blocked or done (e.g. waiting on something time-based).
- `blocked`: the worker is stuck and needs help. `<note>` is the reason.
- `needs-decision`: something requires supervisor or human judgment the worker isn't authorized to make alone (e.g. an ask-user finding from `no-mistakes`). `<note>` is the decision needed.
- `done`: the worker believes the task is complete. `<note>` should include the PR URL for ship tasks.
- `failed`: the worker gave up. `<note>` is why.

Only a `hand send` message carries an operator decision. A worker answering its own harness's question dialog is deciding for itself, not being told - it must never write that answer as if the operator said it. There is no separate vocabulary word for this: the worker records it as `working: deciding myself: <the call> because <reason>`, first person, and reserves `needs-decision:` for what it cannot take back itself (see atqamz/secondhand#87).

Read/classify semantics:

- `hand watch` tails the file once per task per poll tick from a byte offset persisted as `report_offset` on the task, classifying only whole, newline-terminated lines. A partial trailing line (a write still in flight) is left unconsumed until the next tick. Because the offset is durable, a restarted `hand watch` resumes exactly where it stopped: no already-surfaced line is replayed into stdout, `state/events.log`, or Pending Decisions, and no line written moments before the restart is dropped. The last state and note a line classified to are carried across a restart as `last_report_state` and `last_report_note` rather than re-read from the file, so a pane found not-busy after a restart isn't mistaken for an unexplained stop - see "What survives a `hand watch` restart".
- Blank and whitespace-only lines are skipped by every reader, so `hand status`'s history never shows an entry `hand watch` didn't surface and a stray trailing newline can't masquerade as a malformed terminal report.
- If the file shrinks below the last known offset (recreated, truncated), tailing restarts from the beginning rather than erroring.
- Each classified line becomes a `report-*` event (see `hand watch`) and updates the task's last-known report state, which `hand watch`'s idle classifier and `hand status`'s report suffix both consult. Both answer from the last line that *classified*, never simply the last line - `hand watch` by only advancing its carried state on one, `hand status` by skipping trailing malformed lines when it re-reads the file - so free text appended after a real report cannot erase it or make the two commands answer differently about the same quiet pane. The Active Tasks `state` column is this same last classified line, derived on every render rather than written by an event, so the row cannot disagree with `hand status` about it (see "Derived sections").
- **A `done` report is never trusted alone.** A worker's belief that it's finished is a claim, not a fact; it's cross-checked against completion evidence the worker didn't produce before it's allowed to change agent state or clear a pending decision, and until then it surfaces as "reported-done", not "done" (see `classifyReportDone` in `internal/watcher/events.go`). Each task kind has its own evidence: a ship task's merge (`merged` written by `hand merge`, whichever route it took - a PR merge or a `--local` fast-forward that leaves no PR at all - or a recorded PR the watcher's own `gh pr view` poll saw merged), and a scout task's `data/<id>/report.md` - the deliverable `hand promote` itself requires. The ship check never asks which mode the project uses. Evidence usually arrives *after* the `done` line is consumed, so the watcher re-checks every tick and fires the verified `done` event once, when the evidence lands (`ClassifyDeferredDone`) - including when it landed while the watcher was stopped, since the announcement is tracked by the durable `done_verified` marker rather than re-derived from whatever evidence is on disk at startup (see "What survives a `hand watch` restart").
- A line carrying exactly one PR URL auto-records it on a task that doesn't have one yet, exactly as if `hand pr` had been called - including `hand pr`'s full validation (repo-slug match against the project clone's origin remote, plus the `gh pr view` existence check), since a recorded PR is what `hand merge` later merges for real. Both paths call the one shared `project.ValidatePR`. Neither kind of miss aborts the watcher: an attempted recording that did not complete raises `pr-not-recorded` with the underlying error appended, flattened onto the event's single line, and its remedy is a human running `hand pr`, which reconciles whatever half is missing; one the task lock kept the watcher from even attempting raises `pr-record-unknown`, which claims nothing about the outcome and points at `hand status`. The report line is consumed either way, so both go to the event stream and `state/events.log` rather than only to stderr. The one exception is silent by design: losing the lock race to the `hand pr` recording that very URL is not a failure, so the watcher re-reads the task and says nothing when the URL is already on record. A line with more than one URL, or a task that already has a PR recorded, is left alone so `hand pr`'s own explicit-mismatch refusal stays the single path for correcting a wrong record.

### Holds

A hold (atqamz/secondhand#63) records that an id is waiting on something, so "what needs the operator" is derived from the store rather than authored by hand in `data/backlog.md` - that file stays out of scope for holds entirely; a design that finds itself parsing it has gone wrong.

**A hold is its own row, keyed by an arbitrary id, not a foreign key into the task table.** The alternative - a column or side table hanging off a task row - was rejected: it would fit the common case, but `hand teardown` deletes the task row, so a hold set on a task torn down while its question stayed open would vanish exactly when it matters most. A standalone row survives that teardown, which is the motivating case the issue names - work with no task row behind it, either never dispatched or torn down mid-question.

A second, independent reason favored the same answer at the time: before atqamz/secondhand#111, `Open` had no schema-version mechanism - it applied `schema` with `CREATE TABLE IF NOT EXISTS`, which is a correct create against a table that does not exist yet but a silent no-op against one that exists and is merely missing a new column, with no error and no column added. Adding a `blocked_on`-style column to the existing `task` table would have passed every test (which build fresh databases) and silently failed to apply to any already-migrated `state/hand.db` on disk. A brand-new `hold` table sidestepped the gap entirely: every existing database was missing the whole table, so `CREATE TABLE IF NOT EXISTS` took the create branch, not the no-op one, on both a fresh database and a migrated one. See "Schema versioning" below for the mechanism atqamz/secondhand#111 added; an ordinary column addition no longer needs a workaround like this one.

Two kinds, no others invented without a new issue:
- `operator`: waiting on a human. `reason` says what for.
- `blocked`: waiting on another id. `reason` says what for, `blocked_on` names the id.

Set with `hand hold set`, which upserts - a second call on the same id replaces its kind, reason, and blocked-on, so narrowing down a reason is a re-run, not a clear-then-set. Cleared with `hand hold clear`, which deletes the row outright: no residue survives a clear for `hand status` to find later.

**Surviving teardown makes id reuse a hazard, so `hand spawn` refuses a held id.** The same standalone row that keeps a torn-down task's question visible would otherwise reattach it to whatever new work claimed the id next, which is the replay hazard `Delete` already guards against for the report channel by removing `state/<id>.status` (see "Report channel"). The report channel is a volatile wake log and can simply be discarded; a hold is operator-authored, so `hand spawn` refuses with exit 3 and names `hand hold clear <id>` rather than clearing it silently - answering the question is an acknowledgement `hand` has no business making on the operator's behalf. Clearing the hold is therefore the explicit step that says the question is settled, and it is the only escape hatch, since a `--force`-style flag would be the silent clear wearing a different name.

**A hold that cannot be read must never read as nothing waiting.** `ListHolds`/`ReadHold` surface every row exactly as stored, inconsistent ones included - filtering here is what would let an external write's mistake silently disappear from "what is held" - and `hand status` flags an inconsistent row (an unrecognized `kind`, a `blocked` hold with no `blocked_on`, or an `operator` hold carrying one) rather than rendering it as if it were valid. A store-level failure to read holds at all - not a single bad row, the whole read - propagates as a hard error out of `hand status`, fleet or single-task, rather than degrading to an empty list: this is the one place in `hand status` that does not fail open on a read, because an empty `held:` block and "the store couldn't be read" look identical unless the second one is a fatal error instead.

### Concurrency

- Each task is one row. Writes go through sqlite, which serializes them; `hand`'s own named `flock`s (task, project, worktree, dashboard) sit above that and guard whole command sequences, which a per-statement database lock cannot. The project lock is what keeps the `data/projects.md` projection whole: rendering it is a read-modify-write over the file, so a second writer rendering from its own snapshot mid-write would drop a registered project from it.
- `hand watch` is the only long-running process; all other commands are short-lived.
- File locking: machine state is written through sqlite, which serializes writers itself.
- Multiple `hand` invocations against different tasks are safe in parallel.
- Multiple `hand` invocations against the same task should be avoided (agent discipline, not locking).
- **Concurrent tasks on same project:** allowed. Each gets its own treehouse worktree. The collision guard in `hand spawn` prevents worktree overlap. File-level conflicts are resolved at merge time (rebase or conflict resolution), not at spawn time. The agent should avoid spawning tasks that touch the same files when possible, but this is a judgment call, not an enforced constraint.
- **No session lock.** Multiple supervisory sessions can run `hand` commands. The agent is responsible for not conflicting with itself. sqlite's own locking and the dashboard's atomic rewrite prevent corruption; duplicate work is an agent-level problem, not a CLI-level problem.
- **No daemon and no connection pool.** Every command opens the database, does its work and closes it, on a single connection (see "Not Postgres, and no daemon").

### Recovery

On restart (new supervisory agent session):
1. Agent reads `data/dashboard.md` for fleet context.
2. Agent runs `hand status` to see active tasks with current herdr state.
3. Optionally reads `state/events.log` for events that happened during the gap.
4. For each task, herdr state shows whether the pane is busy (working/blocked), not-busy (idle/done - see "Agent state" for why these carry no task-outcome signal by themselves), unreachable (unknown), or dead.
5. Dead herdr pane = dead worker. Agent decides: respawn or teardown.
6. No special recovery logic in `hand`. The CLI shows state; the agent decides action.

When `hand` itself is the thing that is broken - a stale binary, a database that will not open - none of the above is available, and the recovery is `cat state/<id>.status`.
That is why the report channel is a plain append-only text file and why there is no `hand dump` (see "Which to believe when they disagree").
A corrupt `state/index.db` is not a recovery situation at all: delete it, and the next `hand search` rebuilds it from `data/`.

### Migration

An existing fleet home has live state on disk, and the import has to meet it without a working previous binary.

- On first open, `hand` imports every `state/<id>.json` it finds by reading the JSON directly - not by asking the old binary for anything - and moves each imported file into `state/migrated/`. The files are kept rather than deleted so an operator can still read what was imported, and moved rather than left in place so `state/` never holds a second file that looks authoritative.
- `data/projects.md` is imported the same way, once. Unlike the task files it survives the import as its own projection, so its absence cannot serve as the done marker; a `migrated:projects.md` row in the store's `meta` table serves instead.
- **The import is idempotent, because running it twice is what actually happens.** A second run finds no JSON left to import and a registry already marked imported, and changes nothing.
- **A row already in the database wins over a file.** A legacy file that reappears - restored from a backup, or copied back out of `state/migrated/` by an operator reading it - is a snapshot from before the import and must never overwrite what `hand` has recorded since.
- **The whole import runs under one named lock**, the same primitive that guards a command sequence elsewhere, because it spans files sqlite cannot see. Without it two `hand` processes opening a not-yet-migrated home interleave: one parses a file, the other imports it, archives it and then deletes the row under `hand teardown`, and the first lands its insert afterwards and resurrects a torn-down task. It is a lock of its own, not the project registry's, since `hand project add` and `remove` already hold that one when they trigger the registry import.
- **A legacy file that will not parse stops the import and names the file**, rather than importing the rest and leaving an operator to notice a task went missing. Moving the named file aside is the way forward.
- There is no reverse migration. The `state/migrated/` copies are what a rollback would read.
- **A new table needs no migration step of its own; a new column does, and now has one.** `Open` runs the whole `schema` string, `CREATE TABLE IF NOT EXISTS` and all, on every open - not only the first. That statement is correct for a table an existing database is missing outright, which is why adding the `hold` table was sufficient on its own for an existing `state/hand.db` to gain it on its next open. It was a silent no-op for a column an existing table was missing, since sqlite had already satisfied "if not exists" at the table level and never looked at the column list again; see "Schema versioning" below for the mechanism atqamz/secondhand#111 added to close that gap.

### Schema versioning

`Open` gates every other statement on `PRAGMA user_version`, sqlite's own built-in counter for exactly this: no extra table, free to read, and part of the database file itself rather than a `meta` row a stray write could get out of sync with the tables it describes.

- **Version 0 is the schema the `schema` constant in store.go builds** - the one every existing `state/hand.db` already carries, since sqlite defaults an unset `user_version` to 0 and the one real fleet home predates this mechanism entirely. 0 means "the baseline schema this commit ships", not "unknown, refuse to proceed"; the latter reading would stop the one home that exists from opening the moment this merged.
- `migrations` in schemaversion.go is an ordered list of SQL statements, one per schema change since that baseline, each moving `user_version` from its index to index+1. An ordinary column addition - atqamz/secondhand#48's `lease_id`, or a future `project` column for atqamz/secondhand#78 - is two edits that stay in step: the column goes into the `schema` constant, so every database created from then on is built with it, and the matching `ALTER TABLE` is appended to `migrations`, so every database that already exists gains it on its next open. Nothing else in the package needs hand-written detection logic for it.
- **A brand-new database never replays migrations.** `migrateSchema` checks for the `task` table before running `schema` - absent means the file has never had a schema at all - and on that path creates the tables and stamps `user_version` straight to `len(migrations)`, both in one transaction so a crash cannot leave a home carrying the migrated columns while still reading as version 0, which every later open would answer by replaying those migrations against columns that are already there. Without that check, keeping `schema` and `migrations` in step would break every fresh `hand init` with "duplicate column name" while the already-migrated homes kept working: the tests-pass, production-fails asymmetry inverted, which is the exact failure mode atqamz/secondhand#111 exists to remove. The alternative - freezing `schema` at the baseline forever and reading every column addition out of `migrations` - would leave the constant lying about the current layout, and nothing but prose to stop the next reader from adding a column to it.
- **A database newer than the binary is refused, not guessed at.** If `user_version` exceeds `len(migrations)`, `Open` fails wrapping `ErrSchemaNewer` before running a single statement against the tables - an old `hand` opening a new database and writing malformed rows into it would be worse than refusing to run.
- **Applying pending migrations takes a lock**, `SchemaLock` in lock.go, because sqlite's per-statement locking cannot make "add this column, then bump `user_version`" atomic across a whole `Open`. Two `hand` processes opening the same freshly-upgraded home both re-check the version after acquiring the lock, so whichever loses the race finds the version already caught up and applies nothing, rather than re-running `ALTER TABLE ADD COLUMN` against a column the winner already added.
- Each pending step on a database that already exists runs in its own transaction, after the baseline `schema` exec, one step at a time, so a migration that fails partway leaves `user_version` at the last step that fully committed rather than at a state nothing on disk matches.

## Error handling

### Philosophy

- **Fail closed on destructive operations.** `hand teardown` refuses unlanded work. `hand merge` refuses red CI.
- **Fail open on read operations.** `hand status` shows "unknown" if herdr is unreachable, doesn't error.
- **No retries.** If something fails, report the error. The agent decides what to do. Don't loop.
- **No fallback backends.** If herdr is down, say so. Don't silently fall back to tmux.
- **No hooks, no guards, no callbacks.** If the agent does something wrong, the CLI refuses with an error message. The agent reads the error and adjusts. No pretool hooks, no turn-end guards, no continuity checks.

### Exit codes

- `0`: success.
- `1`: general error.
- `2`: usage error: wrong argument count, unknown flag, unknown command or subcommand, a required flag left out (`hand hold set --reason`), mutually exclusive or mutually dependent flags (`hand watch --timeout` without `--until-event`, `hand hold set --blocked-on` on any kind but `blocked` and its absence on a `blocked` one), an invalid argument or flag value (malformed project URL, unknown project mode, harness or hold kind, unparsable `--poll` duration, a non-positive `--timeout`).
  A value the invocation did not supply is not a usage error: the same malformed value read from a `config/` default is a general error (code `1`).
- `3`: precondition failed, meaning the command refuses because the world is not in the state it requires: unlanded work, red CI, a missing or unmerged PR, a missing brief or report, a task, project or hold that does not exist, an id carrying an open hold (`hand spawn`), a task in the wrong kind or state (already merged, not a completed scout, already claimed by another command), a project name or worktree already taken, a project still referenced by active tasks, a PR that conflicts with one already recorded for a task or doesn't belong to the task's project's repo (`hand pr`), a PR that `gh pr view` can't confirm exists (`hand pr`), a task branch whose PRs do not resolve to a single usable winner (`hand teardown`), a `no-mistakes`-mode project whose gate is not initialized (`hand spawn`, `hand promote` - see "Gate preflight").
  Two more apply to every command, since each one resolves a fleet home before it does anything: the working directory has no fleet home at or above it and `HAND_HOME` is unset, or `HAND_HOME` is set to a directory that is not a fleet home. The second refuses rather than falling back to the walk up, because a silent fallback is how an operator dispatches into the wrong fleet.
- `4`: no event delivered, only from `hand watch --until-event`: its `--timeout` elapsed, or it was signaled, without a transition. This includes the timeout elapsing anywhere in arming, the herdr reachability probe as well as the per-task probe sweep - the window is over either way, and no one task is at fault. Distinct from `0` because there the exit *is* the event delivery, and from `1` because the watcher itself did not fail (see "Delivering an event to a supervisory agent").
- `5`: arm-time probe failure, only from `hand watch --until-event`: one named task's herdr pane answered its pre-wait probe with a failure, named on stderr. Distinct from `4` because a specific worker is at fault and can be acted on, and from `0` because nothing was delivered (see "Delivering an event to a supervisory agent").

### Error output

All errors go to stderr. Commands print structured output to stdout.
The agent can parse stdout reliably and read stderr for diagnostics.

## Testing strategy

Every faked `herdr`, `gh`, `treehouse` or harness invocation, in unit and end-to-end tests alike, carries a comment recording the fake-fidelity contract: what the real tool does on success and on failure - exit code, stream, response shape - and whether the fake mirrors that or deliberately diverges.

A fake of a *state-changing* command carries a second obligation, because the first one alone has let vacuous tests through repeatedly: **a fake that answers a state-changing command identically before and after that command cannot test anything about the state change.**
Such a fake must model the state its own commands leave behind, and its fidelity note must say what that state is - a closed herdr tab stops being listed; a returned treehouse worktree keeps its pool slot directory and returns again as a no-op success, while a path no pool manages exits 1.

### Unit tests

- Machine state reading/writing, and that every field survives a round trip.
- Schema versioning: an existing version-0 database opening as the baseline, a registered migration applying automatically and only once, a fresh database skipping a migration its `schema` already builds, and a refusal on a database newer than the binary.
- Legacy import: idempotence over repeated runs, a database row winning over a restored file, and a loud refusal on an unparseable one.
- Index rebuild: that deleting `state/index.db` costs neither machine state nor corpus, and that a corrupt index recovers.
- Project registry parsing.
- Harness launch command construction.
- Event classification logic.
- Brief validation.
- Dashboard rendering.
- Worktree collision detection.

### Integration tests

Implemented in `tests/e2e`, which drives the built binary against a scratch home.
herdr, treehouse, and gh are faked as shell scripts on `PATH` and remote clones are redirected to a local repo, so the suite never touches a real session provider or the network.
`TestMain` enforces that once for the whole suite: it replaces `PATH` with a hermetic one carrying only the real binaries the suite genuinely runs, then asserts that neither herdr, treehouse, gh nor the worker harness resolves, so a missing fake fails the run loudly instead of quietly answering from the developer's real tools.
CI therefore installs no real herdr or treehouse.

- Spawn/teardown cycle, including teardown's refusal on unlanded work.
- Watch event stream with simulated herdr state changes.
- Project add/remove/list/sync cycle.
- Merge with mock gh responses.
- Dashboard updates through full lifecycle.
- Migration of a pre-sqlite fleet home the suite builds by hand, run twice.
- Promote scout-to-ship cycle.
- Collision guard with concurrent tasks.
- Hold lifecycle: set, every `hand status` surface, surviving the teardown of the task it was set on, the spawn refusal on the reused id, and clear.

### No test categories from firstmate

- No "isolation proof" tests (34k lines of shell needing proof of isolation is the problem).
- No "portable shards" (89 scripts needing distributed test execution is the problem).
- No "fleet snapshot schema" tests (JSON schema is Go struct tags).

## What is NOT in scope

These are explicit non-goals. Each lists the firstmate feature it replaces and why it's cut.

| Cut feature | Firstmate equivalent | Why cut |
|---|---|---|
| Secondmates / federation | `fm-home-seed.sh`, `fm-pending-reply-lib.sh`, `fm-config-inherit-lib.sh`, `fm-backlog-handoff.sh` (3,500 lines) | Solves a scaling problem at 10+ projects. Start with one home. |
| X-mode / Twitter | `fm-x-*.sh`, `fmx-respond` skill (3,250 lines) | Separate product concern. Build as a separate tool if ever needed. |
| AFK daemon | `fm-supervise-daemon.sh`, `fm-afk-launch.sh` (2,150 lines) | `hand watch --until-event` as the agent's own background task covers the awake path; `hand notify` is meant to cover the AFK half but is not wired to anything yet (atqamz/secondhand#80). |
| Multiple backends | `backends/herdr.sh`, `backends/cmux.sh`, `backends/zellij.sh`, `backends/orca.sh`, `fm-backend.sh` (5,500 lines) | herdr only. Add tmux fallback later if herdr proves insufficient. |
| Dispatch profiles | `fm-dispatch-select.sh`, `config/crew-dispatch.json` (340 lines + skill) | Pass `--harness`/`--model`/`--effort` explicitly, declare `model`/`effort` in the brief, or set defaults in `config/`. |
| Decision holds | `fm-decision-hold.sh`, decision-hold-lifecycle skill (500 lines) | No longer cut: `hand hold set`/`hand hold clear` record a hold in the store and `hand status` renders it (see "Holds" under "State management"). Only the lifecycle skill wrapped around it stays out. |
| Hook-based guards | `fm-continuity-pretool-check.sh`, `fm-continuity-command-policy.mjs`, `fm-subagent-pretool-check.sh` (450 lines) | CLI refuses bad operations internally. No hooks. |
| PR-check migration | `fm-pr-check-migrate.sh` (1,148 lines) | No legacy to migrate. |
| Fleet snapshot / bearings | `fm-fleet-snapshot.sh`, `fm-bearings-snapshot.sh` (1,860 lines) | `hand status --json` and `data/dashboard.md` cover it. |
| *-axi wrappers | `gh-axi`, `tasks-axi`, `lavish-axi` | Agent uses `gh` directly. Backlog is a file. No wrappers. |
| Vocabulary translation | AGENTS.md section 9 (~40 lines of translation rules) | No internal jargon to translate if AGENTS.md is 25 lines. |
| Wake queues | `fm-wake-lib.sh`, `state/.wake-queue` (596 lines) | Watcher prints to stdout + `state/events.log`. No durable queue. |
| Turn-end guards | `fm-turnend-guard.sh`, `docs/turnend-guard.md` | The supervisory agent's harness handles its own session lifecycle. |
| Persona / role-play | "captain", "ahoy", nautical theming | Pure functionality. Users add personality if they want. |
| Session lock | `fm-lock.sh`, `fm-lock-lib.sh` | No session lock. Atomic file writes prevent corruption. Agent avoids duplicate work. |
| Bootstrap / session-start | `fm-session-start.sh`, `fm-bootstrap.sh` (56K lines combined) | Agent reads `data/dashboard.md` and runs `hand status`. No 187-line digest. |

## Distribution

### Install methods

**Pre-built binary (recommended):**
```sh
# installer script (detects OS/arch, downloads from GitHub Releases)
curl -fsSL https://secondhand.dev/install.sh | bash

# or via Go
go install github.com/atqamz/secondhand@latest

# or via Nix
nix profile install github:atqamz/secondhand
# or one-shot: nix shell github:atqamz/secondhand -c hand init --setup
```

**From source (contributors):**
```sh
git clone https://github.com/atqamz/secondhand
cd secondhand
go build -o hand .
# optionally: cp hand ~/.local/bin/
```

The source repo is a development repo. End users install the binary and create fleet homes anywhere.

### Fleet home creation

```sh
# create a fleet home anywhere
mkdir ~/fleet && cd ~/fleet
hand init --setup

# or in one shot
hand init ~/fleet --setup
```

`hand init` writes the runtime dirs (`data/`, `state/`, `config/`, `projects/`), creates missing `data/backlog.md`, `data/projects.md`, and `data/dashboard.md` skeletons, and creates `state/hand.db` if it is not already there.
It also writes the generated AGENTS.md template and its CLAUDE.md symlink (see "AGENTS.md (target)").
Other existing files are left unchanged, and an optional target path is accepted.

### Self-update: `hand update`

```
hand update
hand update --check
```

Flags:
- `--check`: print whether an update is available without installing it.

Behavior:
1. Query GitHub Releases API for the latest version tag.
2. Compare against the running binary's embedded version.
3. If newer: download the binary for the current OS/arch, verify checksum, replace the running binary in place.
4. After update, refresh the generated AGENTS.md template in the resolved fleet home to the latest version, preserving user edits (see "AGENTS.md (target)"). Outside any fleet home this is skipped silently; a `HAND_HOME` that names no fleet home is a warning, not a silent skip, since that is a misconfiguration rather than an absence. A refresh that fails is likewise a warning on stderr, not a failed update, since the binary is already replaced.
5. Print old version, new version, and what changed (from the installed release's notes). The AGENTS.md line appears only when the template actually changed, and the `changed:` block only when the release has notes.

```
hand update
current: v0.3.1
latest:  v0.4.0
updated hand to v0.4.0
updated AGENTS.md template
changed:
- fix: teardown no longer strands worktrees
```

Same pattern as `no-mistakes update`, `treehouse update`, and `herdr update`.

**Version check on startup:** `hand` prints a one-line notice to stderr when a newer version is available (checked at most once per day, cached in `state/.version-check`). Non-blocking, non-fatal.

```
A new version of hand is available: v0.3.1 -> v0.4.0
Run "hand update" to update
```

### Release pipeline

Automated via [release-please](https://github.com/googleapis/release-please) (same as no-mistakes and treehouse).

**How it works:**
1. Commits to `main` use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `chore:`, etc.).
2. release-please maintains a release PR that accumulates changes and bumps the version according to semver.
3. Merging the release PR creates a GitHub Release with the tag.
4. The release triggers the build workflow that compiles cross-platform binaries and uploads them as release assets.

**Build matrix:**
- linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Each binary checksummed (SHA256).
- Version embedded at build time via `-ldflags "-X main.version=v0.4.0"`.

**Release artifacts per tag:**
- `hand-linux-amd64.tar.gz`
- `hand-linux-arm64.tar.gz`
- `hand-darwin-amd64.tar.gz`
- `hand-darwin-arm64.tar.gz`
- `checksums.txt`
- Auto-generated changelog from conventional commits.

**Workflow files:**

**`.github/workflows/ci.yaml`:** the tracked file is authoritative - runs on every PR to main and on push to main: lint, test across the OS matrix, e2e gated on test passing, and nix-build, which builds the flake package (`nix build .#default`) on x86_64-linux and runs the built binary's `--version`.

**`.github/workflows/release.yaml`:** the tracked file is authoritative - runs on push to main; `workflow_dispatch` exists to re-run release-please after a conflicted release PR is rebased.

Same CI pattern as no-mistakes and treehouse: format, vet, lint, test across OS matrix, e2e against faked herdr and treehouse (no real ones installed, see "Integration tests"), plus a nix-build job guarding the flake package, then release-please for automated releases.

`.github/dependabot.yaml` - keep Go modules and GitHub Actions up to date:
```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
    commit-message:
      prefix: "chore(deps)"
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    commit-message:
      prefix: "chore(ci)"
```

### Repo scaffolding

Files tracked in the source repo (not generated by `hand init`):

**`release-please-config.json`:** the tracked file is authoritative - Go release type, 0.x versioning where a breaking change bumps minor not major, and `flake.nix` listed in `extra-files` so its `x-release-please-version`-marked version stays in sync with each release.

**`.release-please-manifest.json`:** the tracked file is authoritative - release-please owns this file and rewrites the version on every release, so its current value is expected to differ from any snapshot of it.

**`Makefile`:** the tracked file is authoritative - mirrors the CI workflow's format/vet/lint/test/e2e steps for local use before pushing.

**`.golangci.yaml`:** the tracked file is authoritative - it keeps golangci-lint's default linter set and only sets `run.build-tags: [e2e]`, without which the `//go:build e2e` package in `tests/e2e` is invisible to the linter.

**`.gitignore`:** the tracked file is authoritative - the built binary, the `hand init` runtime directories, Go and Nix build output, worktree tooling files, and editor/OS cruft.

**`flake.nix`:** the tracked file is authoritative - a `packages.default` derivation building the `hand` binary and a `devShells.default` carrying the Go toolchain.

**`CONTRIBUTING.md`:** the tracked file is authoritative.

**License:** MIT.

No CD beyond the release - `hand update` is the distribution channel, not a deploy pipeline.

## Implementation plan

### Phase 1: Core lifecycle

Get spawn-work-teardown working end-to-end.

1. `hand init` (with `--setup`) - create runtime directories, interactive harness discovery.
2. `hand project add` - clone and register.
3. `hand spawn` - treehouse worktree + collision guard + herdr tab + launch agent.
4. `hand status` - read state + herdr agent state.
5. `hand send` - send message to herdr pane.
6. `hand teardown` - verify landed, close tab, return worktree.
7. `data/dashboard.md` - auto-maintained by all mutating commands.

Deliverable: can spawn a worker, watch it work, and clean up after.

### Phase 2: Supervision and lifecycle

Make the watcher work so the supervisory agent doesn't have to poll manually.

8. `hand watch` - event loop with herdr push/poll + dashboard updates + event log.
9. `hand merge` - merge PR (with CI check) or local fast-forward.
10. `hand project sync` - fast-forward clones.
11. `hand promote` - scout to ship promotion.
12. `hand notify` - out-of-band notifications.

Deliverable: fully supervised fleet lifecycle.

### Phase 3: Polish

13. `hand status --json` - machine-readable fleet state.
14. Error messages and edge cases.
15. Integration tests.
16. README and AGENTS.md finalization.

Deliverable: ready for daily use.

## AGENTS.md (target)

**`internal/agentsmd/agentsmd.go`'s `generatedBody` constant** is authoritative - the template `hand init` writes into a new fleet home's `AGENTS.md` and `hand update` refreshes there, delimited by `hand:generated` markers so anything a user adds outside that span survives a refresh. `hand doctor` (see the CLI specification) reports perishable content and generated-block drift in a real fleet home's `AGENTS.md` without fixing either.

This repo's own `AGENTS.md` carries no `hand:generated` markers, so `agentsmd.Refresh` declines to touch it even if a maintainer runs `hand init` in the checkout and `state/hand.db` makes `internal/home.IsHome` report true for it. Rather than hand-keep a second copy of `generatedBody` in sync (the drift #44 was filed against), this file's own Rules section points at `generatedBody` and SPECS.md by name instead of restating it: one prose template, one place it can go stale.
