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
3. **herdr-native.** herdr provides semantic agent state (working/idle/done/blocked) and push events. Use them instead of regex-scraping terminal output.
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
4. Acquire a treehouse worktree: `treehouse get <project-clone-path>`.
5. **Collision guard:** cross-check the acquired worktree path against all active tasks' recorded worktree paths in `state/*.json`. If the path matches another active task, return the worktree to treehouse and fail with an error naming the conflicting task. This prevents the stale-lease-after-crash bug (firstmate #947).
6. Create a herdr tab in the project's workspace.
   - Workspace naming: one workspace per project, named after the project.
   - Tab naming: task ID.
7. Construct the harness launch command from the template (see harness section).
8. Send the launch command to the herdr pane.
9. Write `state/<id>.json` with all metadata.
10. Update `data/dashboard.md` with the new task.

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
3. Print one line per task.

Output (fleet overview):
```
fix-login       nsr     ship    working     2h ago
dark-mode       nsr     ship    blocked     45m ago
investigate     nsr     scout   done        10m ago
```

Behavior (single task):
1. Read `state/<id>.json`.
2. Query herdr for current agent state and recent output.
3. Print detailed view.

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
```

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
  "created_at": "2026-07-24T08:00:00Z"
}
```

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
   - `done <id>`: agent reached done/idle state after doing work.
   - `blocked <id>: <reason>`: agent reports blocked.
   - `failed <id>`: herdr pane died unexpectedly.
   - `stale <id>`: agent hasn't changed state for longer than the stale threshold (default 300s, configurable via `config/stale-threshold`).
   - `pr-merged <id>`: a recorded PR has been merged (checked periodically via `gh pr view`).
   - Benign events (working, routine transitions): absorbed silently.
5. Print one line to stdout per actionable event.
6. Append each actionable event to `state/events.log` (bounded: keep last 200 lines, rotate on overflow).
7. Update `data/dashboard.md` with state changes and events.
8. Re-scan `state/` periodically to pick up newly spawned or torn-down tasks.
9. Exit cleanly on SIGINT/SIGTERM.

Output (stream):
```
done fix-login
blocked dark-mode: needs API key for third-party service
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
2. Tear down the scout's herdr tab and worktree.
3. Create or update `data/<id>/brief.md` - the agent should update it with implementation instructions before calling promote, referencing the scout report.
4. Acquire a fresh treehouse worktree (with collision guard).
5. Create a new herdr tab.
6. Launch the worker.
7. Update `state/<id>.json`: kind changes from `scout` to `ship`, new worktree and herdr coordinates.
8. Update `data/dashboard.md`.

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
| `hand watch` | Update agent states, add actionable events to Recent Events (keep last 20), update Pending Decisions |
| `hand project add` | Add to Projects |
| `hand project remove` | Remove from Projects |
| `hand project sync` | Update project sync status |
| `hand promote` | Update task kind in Active Tasks |
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

### Claude Code

```sh
cd <worktree> && claude --print "Read the brief at <brief-path> and carry out the task it describes."
```

The brief path is included in the prompt because `--print` accepts prompt text, not a file path.
When configured, `--model <name>` and `--effort <level>` are inserted before the prompt.

### Codex

```sh
cd <worktree> && codex --file "<brief-path>"
```

### Grok

```sh
cd <worktree> && grok --trust --file "<brief-path>"
```

### Pi

```sh
cd <worktree> && pi "<brief-path>"
```

### OpenCode

```sh
cd <worktree> && opencode run --file "<brief-path>" "Follow the attached brief and complete the task."
```

OpenCode runs headlessly through `opencode run`; `--model <name>` and `--variant <effort>` are
inserted when configured.

The Claude and OpenCode forms above were verified against the installed CLI versions.
Codex, Grok, and Pi retain the literal templates above until those binaries are verified.
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

herdr tracks agent state per pane:
- `working`: agent is actively processing.
- `idle`: agent finished a turn, waiting for input.
- `done`: agent completed its work.
- `blocked`: agent is stuck and needs help.

`hand status` queries this directly.
`hand watch` subscribes to state changes.

### Operations

| hand command | herdr operation |
|---|---|
| `hand spawn` | create workspace (if needed) + create tab + send launch command |
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
# returns agent_status: "working" | "idle" | "done" | "blocked"

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

These calls and the JSON response envelope were verified against the installed herdr version.
The herdr client abstracts them into Go function calls and validates required response fields.

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
- **No separate status files.** The worker's state is queried from herdr in real-time. The JSON state file tracks static metadata (project, worktree, harness, PR URL), not dynamic state (working/idle/done).
- **Event log for crash recovery.** `state/events.log` is a bounded rotating log (last 200 lines) of actionable watcher events. Not for real-time consumption - the watcher prints to stdout for that. The log exists so a restarted agent can read recent history that happened while its context was down.

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
4. For each task, herdr state shows current reality (working/idle/done/blocked/dead).
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
- `3`: precondition failed, meaning the command refuses because the world is not in the state it requires: unlanded work, red CI, a missing or unmerged PR, a missing brief or report, a task or project that does not exist, a task in the wrong kind or state (already merged, not a completed scout, already claimed by another command), a project name or worktree already taken, a project still referenced by active tasks.

### Error output

All errors go to stderr. Commands print structured output to stdout.
The agent can parse stdout reliably and read stderr for diagnostics.

## Testing strategy

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
      - name: Install herdr
        run: |
          mkdir -p "$HOME/.local/bin"
          curl -fsSL https://github.com/ogulcancelik/herdr/releases/download/v0.7.5/herdr-linux-x86_64 -o "$HOME/.local/bin/herdr"
          chmod +x "$HOME/.local/bin/herdr"
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - name: Install treehouse
        run: go install github.com/kunchenguid/treehouse@v2.1.0
      - name: End-to-end
        run: go test -tags=e2e -timeout=10m ./tests/e2e/...
```

`.github/workflows/release.yaml` - runs on push to main:
```yaml
name: Release

on:
  push:
    branches: [main]

permissions:
  contents: write
  pull-requests: write

concurrency:
  group: release-${{ github.ref }}
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

Same CI pattern as no-mistakes and treehouse: format, vet, lint, test across OS matrix, e2e with real herdr and treehouse, then release-please for automated releases.

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
4. Write a brief at `data/<id>/brief.md`.
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
<!-- hand:generated:end -->
```

~22 lines of rules. The CLI's `--help` carries the rest.
CLAUDE.md is a symlink to AGENTS.md.

The `hand:generated` markers delimit the span `hand init` and `hand update` own.
A refresh replaces only that span, so anything a user adds outside it survives; a file with no markers at all is left untouched rather than clobbered.
