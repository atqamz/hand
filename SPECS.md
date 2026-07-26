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
6. **The repo is the working directory.** Clone secondhand, cd in, launch your agent. The repo contains tracked code; runtime state is gitignored.
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

The supervisory agent is any supported harness (claude, codex, pi, grok, opencode) launched inside the secondhand directory.
It reads AGENTS.md, understands the `hand` CLI, and manages the fleet.

Workers are autonomous agents launched by `hand spawn` into herdr tabs with treehouse worktrees.
They follow the brief, do the work, and report through herdr's agent state.

## Directory layout

```
secondhand/                 # repo root = working directory
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
    notify.go
  internal/
    herdr/                  # herdr client library
      client.go             # API calls: create tab, get state, send keys
      types.go              # herdr data types
    state/                  # task state management
      task.go               # read/write/list state/<id>.json
      types.go              # Task struct definition
      report.go             # read/classify state/<id>.status (see "Report channel")
      pr.go                 # PR URL validation and extraction
    worktree/               # treehouse integration
      worktree.go           # get, return, status, collision check
    brief/                  # brief template and generation
      brief.go              # scaffold a brief from a template
    watcher/                # fleet supervision
      watcher.go            # poll/push event loop
      events.go             # event classification
    project/                # project registry
      project.go            # add, list, remove, resolve
    harness/                # agent launch templates
      harness.go            # per-harness launch command construction
    dashboard/              # living dashboard maintenance
      dashboard.go          # read/write/update data/dashboard.md
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
  state/                    # volatile per-task metadata
    <id>.json               # current task state, one file per active task
    <id>.status             # worker-to-supervisor report channel, worker-written, hand-read-only
    events.log              # recent watcher events, bounded rotating log
  data/
    dashboard.md            # living fleet dashboard, auto-maintained by `hand`
    backlog.md              # plain markdown task queue, agent-edited
    projects.md             # thin project registry
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
```

## CLI specification

### `hand init [path] [flags]`

Initialize secondhand runtime directories in the current working directory.
Creates `state/`, `data/`, `projects/`, `config/` if they don't exist.
Creates `data/backlog.md`, `data/projects.md`, and `data/dashboard.md` with skeleton content.
Idempotent: safe to run multiple times.

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

Clone a git repository into `projects/` and register it in `data/projects.md`.

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
7. Append a line to `data/projects.md`: `- <name>: <url> mode=<mode>`.
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

List registered projects from `data/projects.md`.

```
hand project list
hand project list --json
```

Output (human):
```
nsr         https://github.com/yes2games/nsr          direct-pr
yes2infra   https://github.com/yes2games/yes2infra    no-mistakes
```

Output (JSON):
```json
[
  {"name": "nsr", "url": "https://github.com/yes2games/nsr", "mode": "direct-pr"},
  {"name": "yes2infra", "url": "https://github.com/yes2games/yes2infra", "mode": "no-mistakes"}
]
```

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
- `--model <name>`: model override for harnesses that support it. Default: value from `config/model`.
- `--effort <level>`: effort level for harnesses that support it. Default: value from `config/effort`.

Behavior:
1. Validate project exists in registry.
2. Validate no active task with this ID exists.
3. Validate `data/<id>/brief.md` exists (the agent must write it before spawning).
4. Acquire a treehouse worktree: `treehouse get --lease --json --lease-holder hand:<id>`, run inside the project clone (treehouse resolves the pool from cwd).
5. **Collision guard:** cross-check the acquired worktree path against all active tasks' recorded worktree paths in `state/*.json`. If the path matches another active task, return the worktree to treehouse and fail with an error naming the conflicting task. This prevents the stale-lease-after-crash bug (firstmate #947).
6. Create a herdr tab in the project's workspace.
   - Workspace naming: one workspace per project, named after the project.
   - Tab naming: task ID.
7. Construct the harness launch command from the template (see harness section).
8. Send the launch command to the herdr pane.
9. Confirm the worker actually started: poll the pane until herdr reports a live agent on it and
   no first-run dialog is left, answering any known dialog along the way, or the poll window
   elapses (see Harness launch templates).
10. Write `state/<id>.json` with all metadata.
11. Update `data/dashboard.md` with the new task.

Any failure before step 10 leaves nothing behind: the worktree lease returns to treehouse and the
herdr side is rolled back.
A workspace this command created is closed whole, because a fresh workspace already holds an
auto-created root tab and closing only the task's tab would leak it.
A workspace that already existed is shared with other tasks, so it keeps running and only loses
the tab this command added.
Once `state/<id>.json` is written the task owns its worktree and tab, so a later failure such as
the dashboard update never tears down a task that is already running.

Output:
```
spawned fix-login project=nsr kind=ship harness=claude worktree=/home/user/.treehouse/nsr-abc/1/nsr
```

Errors:
- Project not registered.
- Task ID already active.
- Brief not found at `data/<id>/brief.md`.
- Treehouse worktree acquisition failed (pool exhausted, git error).
- Worktree collision with another active task (names the conflicting task).
- Herdr tab creation failed (herdr not running, session error).
- Harness not recognized.
- Worker never confirmed started within the poll window, or is waiting on a first-run prompt hand
  refuses to answer (pane content and the blocking prompt included in the error).

State file written (`state/fix-login.json`):
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
  "created_at": "2026-07-24T10:00:00Z"
}
```

---

### `hand status [id] [flags]`

Show fleet overview or single-task detail.

```
hand status
hand status fix-login
hand status --json
```

Flags:
- `--json`: output as JSON.

Behavior (fleet overview):
1. List all `state/*.json` files.
2. For each, query herdr for current agent state.
3. If agent state is `idle` or `done` (herdr's two spellings of "pane stopped being busy" - see "Agent state" below), consult the task's last report line (see "Report channel"): no report, or the last line was still `working`, appends ` (unreported)`; any other terminal report appends ` (reported: <state>)`; a report file that exists but can't be read appends ` (report unreadable)`, never ` (unreported)` - an I/O fault is not evidence the worker never reported. Any other agent state is printed unadorned.
4. Print one line per task.

Output (fleet overview):
```
fix-login       nsr     ship    working     2h ago
dark-mode       nsr     ship    blocked     45m ago
stuck-task      nsr     ship    idle (unreported)      1h ago
paused-task     nsr     ship    idle (reported: needs-decision)   30m ago
investigate     nsr     scout   done (reported: done)      10m ago
```

Behavior (single task):
1. Read `state/<id>.json`.
2. Query herdr for current agent state and recent output.
3. Read the last 5 lines of the task's report channel (see "Report channel").
4. Print detailed view, including the most recent reported line and a labeled history block.

Output (single task):
```
Task:       fix-login
Project:    nsr
Kind:       ship
Harness:    claude
Model:      sonnet
State:      working
Worktree:   /home/user/.treehouse/nsr-abc/1/nsr
Herdr:      default / wA:tB
Created:    2h ago
PR:         (none)
Reported:   needs-decision: two ways to fix the race, ask-user found both risky

Report history (reported by worker, not verified current truth):
  working: added the retry loop
  needs-decision: two ways to fix the race, ask-user found both risky
```

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
  "created_at": "2026-07-24T08:00:00Z",
  "reported": {"state": "needs-decision", "note": "two ways to fix the race, ask-user found both risky"},
  "report_history": ["working: added the retry loop", "needs-decision: two ways to fix the race, ask-user found both risky"]
}
```

`reported` and `report_history` are omitted when the task has no report file yet. In fleet-overview JSON, only `reported` is included (no history) per row.

Errors:
- Task ID not found.
- Herdr unreachable (graceful degradation: show state as "unknown").

---

### `hand send <id> <message>`

Send a text message to a running worker's herdr pane.

```
hand send fix-login "focus on the auth middleware, not the test framework"
```

Behavior:
1. Read `state/<id>.json` for herdr pane coordinates.
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

### `hand teardown <id> [flags]`

Clean up a completed task. Fail-closed: refuses if work isn't properly landed.

```
hand teardown fix-login
hand teardown investigate --force
```

Flags:
- `--force`: skip landed-work checks (requires explicit authorization).

Behavior (ship task):
1. Check worktree has no uncommitted changes.
2. Check work is landed:
   - If `pr` is set in state: verify the PR is merged via `gh pr view`.
   - If mode is `local-only`: verify the branch is merged into the default branch.
3. Close the herdr tab.
4. Return the worktree to treehouse: `treehouse return <path>`.
5. Remove `state/<id>.json`.
6. Update `data/dashboard.md`: move task to Recent Completions (keep last 10).
7. Keep `data/<id>/brief.md` for history (the agent can prune old briefs).

Behavior (scout task):
1. Check `data/<id>/report.md` exists (the report is the deliverable).
2. Close the herdr tab.
3. Return the worktree to treehouse.
4. Remove `state/<id>.json`.
5. Update `data/dashboard.md`.

Behavior with `--force`:
- Skip steps 1-2 for ship tasks, skip step 1 for scout tasks.
- Still closes herdr tab and returns worktree.

Output:
```
teardown fix-login complete
```

Errors:
- Task not found.
- Uncommitted changes in worktree (without `--force`).
- PR not merged (without `--force`).
- Report not found for scout task (without `--force`).
- Treehouse return failed (worktree locked, already returned).
- Herdr tab close failed (graceful: warn and continue).

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
1. Read `state/<id>.json` for PR URL.
2. Refuse if no PR is recorded.
3. Check PR CI status via `gh pr checks`.
4. Refuse if checks are not green.
5. Run `gh pr merge <number> --repo <owner/repo> --squash` (or specified method).
6. Update `state/<id>.json` with merge status.
7. Update `data/dashboard.md`.
8. Run `hand project sync <project>` to fast-forward the project clone.

Behavior (local merge, `--local`):
1. Read `state/<id>.json` for worktree and project.
2. Refuse if worktree has uncommitted changes.
3. Determine the task branch from the worktree.
4. In the project clone: `git merge --ff-only <task-branch>`.
5. Refuse if fast-forward is not possible (diverged branches).
6. Update `state/<id>.json` with merge status.
7. Update `data/dashboard.md`.

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
2. Read `state/<id>.json`.
3. If the task already has this exact PR recorded, no-op and report it (idempotent).
4. If the task already has a *different* PR recorded, refuse - one task, one PR; correcting a wrong record is a deliberate `hand teardown`/`hand spawn` decision, not something `hand pr` overwrites silently.
5. Resolve the task's project and derive `owner/repo` from the project clone's own `origin` remote (`git config --get remote.origin.url`, not `git remote get-url`, so a local `url.<base>.insteadOf` rewrite never turns a genuine mismatch into a false match).
6. Refuse if the URL's `owner/repo` doesn't match the derived repo slug.
7. Confirm the PR exists via `gh pr view` (network check, 30s timeout) - shape validation in step 1 only proves the URL looks right, not that the PR is real.
8. Write `pr` into `state/<id>.json` and update `data/dashboard.md`.

Steps 5-7 live in `project.ValidatePR` and are the *only* validation path: `hand watch`'s auto-record calls the same function, so a worker-supplied URL can never reach task state on weaker terms than an explicit `hand pr`.

Output:
```
recorded PR for fix-login: https://github.com/org/repo/pull/42
```

Output (idempotent repeat):
```
pr already recorded for fix-login: https://github.com/org/repo/pull/42
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
```

Flags:
- `--poll <duration>`: poll interval when push events aren't available. Default: value from `config/watch-interval`, or `5s`.

Behavior:
1. List all active tasks from `state/*.json`.
2. Subscribe to herdr's `agent_status_changed` push events if available.
3. Fall back to polling herdr agent states at `--poll` interval.
4. Classify each state change:
   - `idle-unreported <id>`: agent stopped being busy after working/blocked - herdr reports this as `idle` or `done` interchangeably (see "Agent state" below; hand's polling model observes `done`, essentially always, never `idle`) - but its report channel (see "Report channel") doesn't explain the stop: no report at all, or the last line was still `working`. Any other terminal report (`paused`, `blocked`, `needs-decision`, `done`, `failed`) already explains the stop, so that transition is absorbed silently instead.
   - `blocked <id>: <reason>`: agent reports blocked (herdr-level; herdr gives no free-text reason, so `<reason>` is a fixed string).
   - `failed <id>`: herdr pane died unexpectedly.
   - `stale <id>`: agent hasn't changed state for longer than the stale threshold (default 300s, configurable via `config/stale-threshold`).
   - `pr-merged <id>`: a recorded PR has been merged (checked periodically via `gh pr view`). Announced once ever: the observation is recorded as `pr_merged_observed` in `state/<id>.json` after the line is printed, so a restart neither repeats it nor loses it to a crash between the two.
   - `pr-not-recorded <id>: <url> (<reason>)`: a PR URL a worker embedded in a report line failed the validation `hand pr` enforces, so it was not recorded. Also raised as a Pending Decision naming the URL, since the report line is consumed either way and the fix is a human running `hand pr <id> <url>`.
   - `working <id>: <note>` / `paused <id>: <note>` / `report-blocked <id>: <note>` / `needs-decision <id>: <note>` / `report-failed <id>: <note>`: a new line landed on the task's report channel, classified per "Report channel" above.
   - `reported-done <id>: <note>` / `done <id>: <note>`: a `done` report line landed; printed as `reported-done` until cross-checked against the task kind's completion evidence (a merged PR for ship, `data/<id>/report.md` for scout), then once as `done` when that evidence lands - which is usually a later tick (see "Report channel").
   - `malformed report <id>: <line>`: a report line didn't match the fixed vocabulary. Surfaced, never dropped, so a typo in the worker's report doesn't silently vanish.
   - Benign events (working herdr transitions, routine transitions): absorbed silently.
5. Print one line to stdout per actionable event.
6. Append each actionable event to `state/events.log` (bounded: keep last 200 lines, rotate on overflow).
7. Update `data/dashboard.md` with state changes and events.
8. Re-scan `state/` periodically to pick up newly spawned or torn-down tasks.
9. Exit cleanly on SIGINT/SIGTERM.
10. While tailing a task's report channel, a line carrying exactly one PR URL auto-records it if the task doesn't already have one, subject to the same validation `hand pr` enforces; a URL that fails validation is skipped and surfaced as `pr-not-recorded` plus a Pending Decision (see "Report channel").
11. Per-task bookkeeping the watcher owns (`report_offset`, `pr_merged_observed`) is written back after the tick's events are announced, under a non-blocking lock. A task locked by another command (`hand merge` holds it across `gh` round-trips) is skipped and retried next tick rather than stalling the poll loop; everything written there is re-derivable.

Output (stream):
```
idle-unreported dark-mode
blocked dark-mode: needs API key for third-party service
needs-decision fix-login: two ways to fix the race, ask-user found both risky
reported-done fix-login: PR https://github.com/org/repo/pull/42 checks green
stale investigate-crash
failed api-refactor
pr-merged fix-login
```

The supervisory agent runs `hand watch` as a background task (via its harness's background-task mechanism) and acts on each printed line.

Event durability: if the supervisory agent's context compacts or the session restarts, events since the last read are in `state/events.log`. The agent can `hand status` to recover current truth and read `state/events.log` for recent history.

Errors:
- Herdr not running (fatal: exit with error).
- Individual task probe failure (graceful: report as "unknown" state).

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
3. Update `data/dashboard.md` if any project advanced.

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
- `--model <name>`: model override. Default: value from `config/model`.
- `--effort <level>`: effort override. Default: value from `config/effort`.

Behavior:
1. Validate the task exists and is a completed scout (has `data/<id>/report.md`, herdr pane is done or dead).
2. Create or update `data/<id>/brief.md` - the agent should update it with implementation instructions before calling promote, referencing the scout report.
3. Acquire a fresh treehouse worktree (with collision guard).
4. Create a new herdr tab.
5. Launch the worker and confirm it started (same as `hand spawn`).
6. Update `state/<id>.json`: kind changes from `scout` to `ship`, new worktree and herdr coordinates.
7. Only now tear down the scout's herdr tab and return its worktree; a failure here is a warning, not an error.

The scout side is torn down last on purpose: the same rollback contract as `hand spawn` applies up
to step 6, so a promotion that fails partway still leaves the scout's pane and worktree intact
instead of stranding the task with nothing to look at.
`data/dashboard.md` is deliberately left untouched, so the task's row keeps reading `scout` until
something else rewrites it.

Output:
```
promoted investigate-crash: scout -> ship project=nsr harness=claude
```

Errors:
- Task not found.
- Task is not a completed scout.
- Scout report not found.
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

The watcher calls `hand notify` for captain-relevant events (done, blocked, failed) so the user gets notified even when away from the terminal.

Output:
```
notified: fix-login PR is ready for review
```

Errors:
- Notify command failed (warn and continue - notification failure never blocks work).

---

## Dashboard: `data/dashboard.md`

The dashboard is secondhand's answer to the session clobbering problem.
Instead of dumping fleet state into the agent's context on every session start, `hand` maintains a persistent markdown file that both the agent and the user can read.

Every `hand` command that mutates state also updates the relevant section of `data/dashboard.md`.
The agent reads it at session start.
The user can watch it in a side-by-side editor for real-time fleet visibility.

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

| Command | Dashboard update |
|---|---|
| `hand spawn` | Add row to Active Tasks |
| `hand status` | Refresh agent states in Active Tasks (only when dashboard is stale) |
| `hand send` | No update |
| `hand teardown` | Move from Active Tasks to Recent Completions (keep last 10) |
| `hand merge` | Update PR status, add to Recent Events |
| `hand pr` | Set PR on the task's row |
| `hand watch` | Update agent states, add actionable events to Recent Events (keep last 20), update Pending Decisions, auto-record a PR seen on the report channel |
| `hand project add` | Add to Projects |
| `hand project remove` | Remove from Projects |
| `hand project sync` | Update project sync status |
| `hand promote` | No update (the row keeps its scout kind) |
| `hand notify` | No update (notification is a side channel) |

### Optional: qmd for historical search

`data/` grows large over time (400+ files, 4MB+ in real usage - briefs, reports, decisions accumulate).
The dashboard solves "what's happening now".
For "what did we decide about X three months ago", searching hundreds of markdown files by hand is tedious.

[qmd](https://github.com/tobi/qmd) is a local search engine for markdown that provides keyword, semantic, and hybrid search.
Secondhand recommends it but does not require it:

- `hand init --setup` does not require or configure qmd.
- If qmd is available, the agent can create a collection pointing at `data/` manually.
- The AGENTS.md sketch mentions `qmd search` as the way to find historical context.
- All `hand` operations work without qmd. The agent can always fall back to reading files directly.

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
| `hand spawn` | create workspace (if needed) + create tab + send launch command + poll pane state and read pane text until the worker is confirmed started, sending keys to answer first-run dialogs |
| `hand status` | get agent state for pane |
| `hand send` | check composer empty + send keys to pane |
| `hand teardown` | close tab (+ close workspace if empty) |
| `hand watch` | subscribe to agent_status_changed events, or poll pane states |

### Herdr CLI calls

```sh
# list workspaces
herdr workspace list

# create workspace without focusing it
herdr workspace create --no-focus --cwd <project-clone-path> --label <project-name>

# create tab in workspace
herdr tab create --workspace <ws-id> --no-focus --cwd <worktree> --label <task-id>

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
- `hand` itself never calls `no-mistakes`. The worker does.

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

`data/projects.md` is a simple list managed by `hand project add/remove`.

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
- `no-mistakes`: worker runs no-mistakes pipeline, ships via PR with validation evidence.
- `direct-pr`: worker pushes branch and opens PR directly.
- `local-only`: worker commits to a local branch. Merging into default branch via `hand merge --local`.

## State management

### Design decisions

- **JSON, not .meta key=value files.** Structured, typed, parseable without sed/grep/awk.
- **Current state, not append-only logs.** One file per task, updated in place. History comes from the dashboard, events.log, and herdr event streams, not from accumulating status lines.
- **No separate status files for herdr-visible state.** The worker's herdr-visible state (working/idle/blocked/done/unknown) is queried from herdr in real-time, not persisted by `hand`. The JSON state file tracks static metadata (project, worktree, harness, PR URL), not dynamic agent state.
  The one exception is `state/<id>.status`, the worker-to-supervisor report channel (see "Report channel" below): herdr's agent state answers "is the pane busy," not "why did it stop" or "what actually happened," and that gap is exactly what caused done/blocked/needs-decision to go unreported in production. This file is not a second copy of herdr's state - it's a channel for information herdr has no way to carry, and it exists only because that specific gap caused a real incident, per principle 5 (no feature without friction).
  This is the strongest argument for the whole design: herdr's `idle`/`done` split (see "Agent state") carries no task-outcome information at all for `hand`'s headless deployment, only whether a human happened to be looking at the time. The report channel isn't a supplement to herdr's status for learning how a task ended - it's the only source of that information there is.
- **Event log for crash recovery.** `state/events.log` is a bounded rotating log (last 200 lines) of actionable watcher events. Not for real-time consumption - the watcher prints to stdout for that. The log exists so a restarted agent can read recent history that happened while its context was down.

### Report channel

`state/<id>.status` is an append-only text file the worker writes and `hand` only ever reads.
The brief the supervisory agent writes for a worker must include this file's absolute path and the vocabulary below, so the worker knows to append to it.

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

Read/classify semantics:

- `hand watch` tails the file once per task per poll tick from a byte offset persisted as `report_offset` in `state/<id>.json`, classifying only whole, newline-terminated lines. A partial trailing line (a write still in flight) is left unconsumed until the next tick. Because the offset is durable, a restarted `hand watch` resumes exactly where it stopped: no already-surfaced line is replayed into stdout, `state/events.log`, or Pending Decisions, and no line written moments before the restart is dropped. On first tracking a task, the watcher also re-reads the last report line so a pane found not-busy after a restart isn't mistaken for an unexplained stop; a report file that exists but can't be read is diagnosed on stderr, never treated as "this worker never reported".
- Blank and whitespace-only lines are skipped by every reader, so `hand status`'s history never shows an entry `hand watch` didn't surface and a stray trailing newline can't masquerade as a malformed terminal report.
- If the file shrinks below the last known offset (recreated, truncated), tailing restarts from the beginning rather than erroring.
- Each classified line becomes a `report-*` event (see `hand watch`) and updates the task's last-known report state, which `hand watch`'s idle classifier and `hand status`'s report suffix both consult.
- **A `done` report is never trusted alone.** A worker's belief that it's finished is a claim, not a fact; it's cross-checked against completion evidence the worker didn't produce before it's allowed to change agent state or clear a pending decision, and until then it surfaces as "reported-done", not "done" (see `classifyReportDone` in `internal/watcher/events.go`). Each task kind has its own evidence: a ship task's merge (`merged` written by `hand merge`, whichever route it took - a PR merge or a `--local` fast-forward that leaves no PR at all - or a recorded PR the watcher's own `gh pr view` poll saw merged), and a scout task's `data/<id>/report.md` - the deliverable `hand promote` itself requires. The ship check never asks which mode the project uses. Evidence usually arrives *after* the `done` line is consumed, so the watcher re-checks every tick and fires the verified `done` event once, when the evidence lands (`ClassifyDeferredDone`).
- A line carrying exactly one PR URL auto-records it on a task that doesn't have one yet, exactly as if `hand pr` had been called - including `hand pr`'s full validation (repo-slug match against the project clone's origin remote, plus the `gh pr view` existence check), since a recorded PR is what `hand merge` later merges for real. Both paths call the one shared `project.ValidatePR`. Validation failure on the auto-record path skips recording and raises `pr-not-recorded` plus a Pending Decision rather than aborting the watcher; the report line is consumed either way, so the URL has to reach a human who can run `hand pr` instead of only a stderr diagnostic. A line with more than one URL, or a task that already has a PR recorded, is left alone so `hand pr`'s own explicit-mismatch refusal stays the single path for correcting a wrong record.

### Concurrency

- Each task has its own state file. No shared mutable state between tasks.
- `hand watch` is the only long-running process; all other commands are short-lived.
- File locking: `state/<id>.json` is written atomically (write to temp + rename). No lock files.
- Multiple `hand` invocations against different tasks are safe in parallel.
- Multiple `hand` invocations against the same task should be avoided (agent discipline, not locking).
- **Concurrent tasks on same project:** allowed. Each gets its own treehouse worktree. The collision guard in `hand spawn` prevents worktree overlap. File-level conflicts are resolved at merge time (rebase or conflict resolution), not at spawn time. The agent should avoid spawning tasks that touch the same files when possible, but this is a judgment call, not an enforced constraint.
- **No session lock.** Multiple supervisory sessions can run `hand` commands. The agent is responsible for not conflicting with itself. Atomic file writes prevent corruption; duplicate work is an agent-level problem, not a CLI-level problem.

### Recovery

On restart (new supervisory agent session):
1. Agent reads `data/dashboard.md` for fleet context.
2. Agent runs `hand status` to see active tasks with current herdr state.
3. Optionally reads `state/events.log` for events that happened during the gap.
4. For each task, herdr state shows whether the pane is busy (working/blocked), not-busy (idle/done - see "Agent state" for why these carry no task-outcome signal by themselves), unreachable (unknown), or dead.
5. Dead herdr pane = dead worker. Agent decides: respawn or teardown.
6. No special recovery logic in `hand`. The CLI shows state; the agent decides action.

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
- `2`: usage error: wrong argument count, unknown flag, unknown command or subcommand, mutually exclusive flags, an invalid argument or flag value (malformed project URL, unknown project mode or harness, unparsable `--poll` duration).
  A value the invocation did not supply is not a usage error: the same malformed value read from a `config/` default is a general error (code `1`).
- `3`: precondition failed, meaning the command refuses because the world is not in the state it requires: unlanded work, red CI, a missing or unmerged PR, a missing brief or report, a task or project that does not exist, a task in the wrong kind or state (already merged, not a completed scout, already claimed by another command), a project name or worktree already taken, a project still referenced by active tasks, a PR that conflicts with one already recorded for a task or doesn't belong to the task's project's repo (`hand pr`), a PR that `gh pr view` can't confirm exists (`hand pr`).

### Error output

All errors go to stderr. Commands print structured output to stdout.
The agent can parse stdout reliably and read stderr for diagnostics.

## Testing strategy

Every faked `herdr`, `gh`, `treehouse` or harness invocation, in unit and end-to-end tests alike, carries a comment recording the fake-fidelity contract: what the real tool does on success and on failure - exit code, stream, response shape - and whether the fake mirrors that or deliberately diverges.

### Unit tests

- State file reading/writing.
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
- Promote scout-to-ship cycle.
- Collision guard with concurrent tasks.

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
| AFK daemon | `fm-supervise-daemon.sh`, `fm-afk-launch.sh` (2,150 lines) | `hand watch` + `hand notify` + the agent's own background task is sufficient. |
| Multiple backends | `backends/herdr.sh`, `backends/cmux.sh`, `backends/zellij.sh`, `backends/orca.sh`, `fm-backend.sh` (5,500 lines) | herdr only. Add tmux fallback later if herdr proves insufficient. |
| Dispatch profiles | `fm-dispatch-select.sh`, `config/crew-dispatch.json` (340 lines + skill) | Pass `--harness`/`--model`/`--effort` explicitly, or set defaults in `config/`. |
| Decision holds | `fm-decision-hold.sh`, decision-hold-lifecycle skill (500 lines) | Agent tracks decisions in backlog and dashboard. |
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

The source repo is a development repo. End users install the binary and create workspaces anywhere.

### Workspace creation

```sh
# create a workspace anywhere
mkdir ~/fleet && cd ~/fleet
hand init --setup

# or in one shot
hand init ~/fleet --setup
```

`hand init` writes the runtime dirs (`data/`, `state/`, `config/`, `projects/`) and creates missing `data/backlog.md`, `data/projects.md`, and `data/dashboard.md` skeletons.
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
4. After update, refresh the generated AGENTS.md template in the current workspace to the latest version, preserving user edits (see "AGENTS.md (target)"). Outside a workspace this is skipped silently; a refresh that fails is a warning on stderr, not a failed update, since the binary is already replaced.
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

`.github/workflows/ci.yaml` - runs on every PR to main:
```yaml
name: CI

on:
  pull_request:
    branches: [main]

jobs:
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Format
        run: |
          output=$(gofmt -l .)
          if [ -n "$output" ]; then
            echo "Files not formatted:"
            echo "$output"
            exit 1
          fi
      - name: Vet
        run: go vet ./...
      - name: Lint
        uses: golangci/golangci-lint-action@v9
        with:
          version: latest

  test:
    name: Test (${{ matrix.os }})
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Test
        run: go test -race ./...
      - name: Build
        run: go build -o hand .

  e2e:
    name: E2E
    runs-on: ubuntu-latest
    needs: test
    steps:
      - uses: actions/checkout@v7
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: End-to-end
        run: go test -tags=e2e -timeout=10m ./tests/e2e/...
```

`.github/workflows/release.yaml` - runs on push to main, and manually via `workflow_dispatch` (used to re-run release-please after a conflicted release PR is rebased):
```yaml
name: Release

on:
  push:
    branches: [main]
  workflow_dispatch:

permissions:
  contents: write
  pull-requests: write

concurrency:
  group: release
  cancel-in-progress: false

jobs:
  release-please:
    name: Release Please
    runs-on: ubuntu-latest
    outputs:
      release_created: ${{ steps.release.outputs.release_created }}
      tag_name: ${{ steps.release.outputs.tag_name }}
      version: ${{ steps.release.outputs.version }}
    steps:
      - uses: googleapis/release-please-action@v5
        id: release
        with:
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json

  build:
    name: Build (${{ matrix.goos }}/${{ matrix.goarch }})
    needs: release-please
    if: needs.release-please.outputs.release_created == 'true'
    strategy:
      fail-fast: false
      matrix:
        include:
          - goos: linux
            goarch: amd64
            runner: ubuntu-latest
          - goos: linux
            goarch: arm64
            runner: ubuntu-latest
          - goos: darwin
            goarch: amd64
            runner: macos-latest
          - goos: darwin
            goarch: arm64
            runner: macos-latest
    runs-on: ${{ matrix.runner }}
    steps:
      - uses: actions/checkout@v7
        with:
          ref: ${{ needs.release-please.outputs.tag_name }}
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - name: Build
        env:
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
          CGO_ENABLED: "0"
        run: |
          go build -ldflags "-s -w -X main.version=${{ needs.release-please.outputs.version }}" -o hand .
          tar czf hand-${{ matrix.goos }}-${{ matrix.goarch }}.tar.gz hand
      - name: Upload artifact
        uses: actions/upload-artifact@v7
        with:
          name: hand-${{ matrix.goos }}-${{ matrix.goarch }}
          path: hand-${{ matrix.goos }}-${{ matrix.goarch }}.tar.gz

  publish:
    name: Publish release assets
    needs: [release-please, build]
    runs-on: ubuntu-latest
    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v8
        with:
          merge-multiple: true
      - name: Generate checksums
        run: sha256sum hand-*.tar.gz > checksums.txt
      - name: Upload to release
        uses: softprops/action-gh-release@v3
        with:
          tag_name: ${{ needs.release-please.outputs.tag_name }}
          files: |
            hand-*.tar.gz
            checksums.txt
```

Same CI pattern as no-mistakes and treehouse: format, vet, lint, test across OS matrix, e2e against faked herdr and treehouse (no real ones installed, see "Integration tests"), then release-please for automated releases.

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

**`release-please-config.json`:**
```json
{
  "packages": {
    ".": {
      "release-type": "go",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "bump-patch-for-minor-pre-major": true,
      "extra-files": ["flake.nix"]
    }
  },
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json"
}
```

**`.release-please-manifest.json`:**
```json
{
  ".": "0.0.0"
}
```

**`Makefile`:**
```makefile
.PHONY: build test fmt lint e2e install clean

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o hand .

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	@output=$$(gofmt -l .); if [ -n "$$output" ]; then echo "Files not formatted:"; echo "$$output"; exit 1; fi
	go vet ./...
	golangci-lint run

e2e:
	go test -tags=e2e -timeout=10m ./tests/e2e/...

install: build
	cp hand $(GOPATH)/bin/ 2>/dev/null || cp hand ~/.local/bin/

clean:
	rm -f hand
```

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

```markdown
<!-- hand:generated:start -->
# Secondhand

You manage a fleet of coding agents using the `hand` CLI.
Run `hand --help` for the full command reference.

## Workflow

1. Read `data/dashboard.md` for current fleet state.
2. Match the request to a project in `data/projects.md`.
3. Edit `data/backlog.md` to record the task with a unique ID.
4. Write a brief at `data/<id>/brief.md`, including the absolute path to `state/<id>.status` and the report vocabulary the worker should append to it.
5. `hand spawn <id> <project>` to start a worker.
6. `hand watch` as a background task to monitor the fleet.
7. Act on watch output: steer blocked workers with `hand send`, relay results.
8. When told to merge: `hand merge <id>`.
9. `hand teardown <id>` after work is landed.

## Rules

- Never edit files under `projects/`. Workers do that in worktrees.
- Never merge without explicit authorization.
- Never force-teardown without explicit authorization.
- Report outcomes plainly. If work failed, say so with evidence.
- Ship tasks produce PRs or local branches. Scout tasks produce `data/<id>/report.md`.
- `data/backlog.md` is your task queue. Edit it directly.
- For no-mistakes projects, workers use `no-mistakes axi` directly in the worktree.
- Use `qmd search` to find historical context in data/ when available. Fall back to reading files directly.
- `hand status <id>` shows a worker's reported state; see SPECS.md's state management section for the report vocabulary (working/paused/blocked/needs-decision/done/failed).
<!-- hand:generated:end -->
```

~23 lines of rules. The CLI's `--help` carries the rest.
CLAUDE.md is a symlink to AGENTS.md.

The `hand:generated` markers delimit the span `hand init` and `hand update` own.
A refresh replaces only that span, so anything a user adds outside it survives; a file with no markers at all is left untouched rather than clobbered.
