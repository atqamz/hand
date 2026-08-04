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
6. **A fleet home is any directory `state/hand.db` marks as one.** `hand init` creates the file up front; put `hand` on PATH and launch the agent there. Only `hand` ever writes it, so a project clone under `projects/` carrying its own generic top-level `data/` and `state/` cannot capture the walk up. A home initialized before `state/hand.db` existed falls back to the marker it was initialized with, `data/projects.md` plus `state/`, so an operator upgrading in place never has to re-run anything by hand and the legacy `state/<id>.json` import still finds a home to run against (see "Migration"). Maintainers dogfood a fleet home inside the secondhand repo checkout itself, with runtime state gitignored alongside the tracked code, but the CLI has no opinion about the two: `HAND_HOME`, or an ancestor of the working directory, is all it looks for.
7. **`hand status` is the memory.** It is computed from the store and the report channel at the moment it is asked for, never a file the agent or the user reads out of band. No session digests, no bootstrap scripts, no 187-line status dumps, and no rendering that can disagree with the state behind it (atqamz/secondhand#62).
8. **No hooks, no guards, no callbacks.** The CLI fails closed on bad operations. Errors are CLI output, not injected hook messages. The agent reads errors and decides. No magic. The one hook `hand` installs is the opposite of a guard: a `SessionStart` entry that runs the bare command so a session opens with the fleet in context, policing nothing and refusing nothing (see "Ambient context").

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
`hand watch` is still the only long-running process, and it holds no database lock between ticks - only the `flock` that makes it the fleet home's single watcher (see "One watcher per fleet home").
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
      worktree.go           # get, return, collision check
    brief/                  # brief parsing
      brief.go              # read the brief's declared model/effort (see "Brief format")
    watcher/                # fleet supervision
      watcher.go            # poll/push event loop
      events.go             # event classification
      usagelimit.go         # detect, resume and release a quota-limited worker (see "Resuming a usage-limited worker")
      ownership.go          # flock on state/watch.pid (see "One watcher per fleet home")
    notify/                 # out-of-band delivery
      notify.go             # config/notify template execution, shared by hand notify and the watcher's in-process hook
    project/                # project registry
      project.go            # add, list, remove, resolve
      pr.go                 # shared PR validation: repo-slug match, gh existence check
    harness/                # agent launch templates
      harness.go            # per-harness launch command construction
      usagelimit.go         # per-harness usage-limit signatures and reset parsing (claude only)
    completion/             # durable teardown completion record
      completion.go         # append/list state/completions.jsonl
    agentsmd/               # generated AGENTS.md template
      agentsmd.go           # write and refresh the generated span, check a home's file (see "`hand doctor`")
    age/                    # elapsed-time strings shared by hand status and the watcher
      age.go                # FormatAge, FormatDuration
    atomicfile/             # shared write-to-temp-then-rename helper
      atomicfile.go         # atomic file replacement
    axi/                    # the one TOON renderer every command emits through (see "Output shape")
      axi.go                # fields, row blocks, --fields selection, truncation hints, help[] lines
    sessionhook/            # ambient context for a supervising session (see "Ambient context")
      sessionhook.go        # install, repoint and report the SessionStart hook entry
  tools/
    commentlint/            # the comment check `make lint` and CI run (see "Repo scaffolding")
      main.go               # walk the tree, report one file:line:column per violation
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
    watch.pid               # the flock the fleet home's single watcher holds (see "One watcher per fleet home")
    completions.jsonl       # durable teardown completion records, one JSON object per line, uncapped
  data/
    operator.md             # standing operator constraints and preferences, read at session start
    backlog.md              # plain markdown task queue, agent-edited
    projects.md             # project registry projection (see "Project registry format")
    learnings.md            # dated, evidence-backed operational facts, curated not appended
    done-archive.md         # finished entries rolled off backlog.md (see "Backlog format")
    note-archive.md         # dropped or superseded backlog entries, kept with the reason
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
    send-wait               # how long `hand send` waits for a busy composer (default: 2m)
    parked-paused-bound     # seconds a paused-and-silent task may sit before it's parked (default: 3600)
    parked-done-bound       # seconds a done-or-failed-and-silent task may sit before it's parked (default: 5400)
    parked-other-bound      # seconds a silent task in any other state may sit before it's parked (default: 1200)
  .claude/
    settings.json           # merged, never overwritten: hand owns one SessionStart entry in it
```

Who writes a file for whom is what decides whether it belongs under `data/` at all.
`operator.md` is the operator writing for the agent - identity, authority, and hard constraints that outrank the agent's judgment.
`backlog.md`, `learnings.md` and the two archives are the agent writing for itself, and `projects.md` is the one file here that is a projection of machine state rather than prose either of them authored (see "Project registry format").
The direction `data/` does not carry is a file maintained by hand for the operator to read: `hand status` and the issue tracker already answer what the fleet is doing and what is waiting on a human, which is why `data/inbox.md` was proposed as that channel and cut (atqamz/secondhand#64), and why a decision taken under a standing autonomy grant goes on the PR or issue it concerns rather than into a fleet file.
`hand init` seeds `operator.md`, `learnings.md`, `done-archive.md` and `note-archive.md` and no command ever overwrites one that exists (atqamz/secondhand#47); `hand update` seeds whichever of them the home is missing, so a home refreshed to a template that names them is never pointed at absent files (see "Self-update: `hand update`").
`learnings.md`, `done-archive.md` and `note-archive.md` are plain agent-edited markdown with no schema, no subcommand and no validation, the same treatment `backlog.md` already gets.
`operator.md` gets that same treatment minus the editing: it is the operator's file, which the agent reads at session start and never rewrites, and that one-way ownership is what lets its constraints outrank the agent's judgment at all.

## Ambient context

A supervising agent that has to ask for the fleet before it can reason about it spends a turn on
what the session could have opened with. `hand init` and `hand update` install `hand` as a Claude
Code `SessionStart` hook in the home's `.claude/settings.json`, so every conversation starts with
the bare command's overview - identity, home, counts and the task table - already in context.
`data/dashboard.md` was the file-based answer to the same need and was removed with atqamz/secondhand#62;
the hook replaces it with output generated at the moment it is read, which no file can be.

The file is merged, never overwritten. An operator's permissions, other events and other
`SessionStart` entries are carried through untouched, and a `settings.json` hand cannot parse is an
error rather than a clobber. Hand owns at most one entry: the first whose command runs this binary
or any binary named `hand`. Refreshing repoints that entry's path, which is what an install that
moved needs, and leaves any arguments the operator added to it alone.

Installing is confined to a fleet home. A directory with no `state/hand.db` gets no `.claude/`
directory at all, because nothing there runs a supervising session.

## Output shape

`hand`'s only consumer is an LLM agent, so its default output is TOON (https://axi.md) rather than a table aligned for a human terminal (atqamz/secondhand#45).
`internal/axi` is the single renderer every command emits through, so the shape below is a property of that package and not a convention each command re-implements.

Three block kinds, rendered in the order the command adds them:
- A scalar field, `key: value`.
- A row block, `name[N]{f1,f2,f3}:` followed by one two-space-indented comma-joined row per item.
- A list block, `name[N]:` followed by one `  - item` line per item.

The rules that hold across every command:
- A row block prints its `name[N]{...}:` header even when `N` is zero, so an empty result is a positive statement carrying a count rather than the silence a broken command also produces (atqamz/secondhand#100).
- A value is quoted, in Go string-literal syntax, only when it would otherwise be ambiguous: it carries a `,`, a `:` or a `"`, it has leading or trailing whitespace, or it is empty. The empty string renders as `""`, since unquoted it is indistinguishable from a field nobody emitted.
- A field with nothing behind it renders as `none`, so "no PR recorded" never reads as a PR whose URL happens to be blank.
- A truncated field carries its own recovery: `<cut text>... (truncated, <N> chars total - use <command> to see complete text)`, naming the command that returns the whole field. The budget is counted in runes, never bytes, so a multi-byte character is never cut in half.
- A list item runs to end of line, so an embedded newline is collapsed to a space rather than silently becoming a second item.
- A `help[N]:` block, when present, is last, and names what to run next. It is omitted entirely when a command has nothing to suggest - an empty `help[0]` block costs context and says nothing.
- Counts and other aggregates are pre-computed and emitted as scalar fields above the rows they summarize, so a caller that only needs the number never has to count rows to get it.
- A command that changes something confirms it with a `result:` field naming what happened, alongside the fields it changed. One field carries the outcome, so `recorded` and `already-recorded`, or `merged` and its method, are told apart by reading a value rather than matching a sentence.
- `--fields <a,b,c>` narrows a command's row block to the named columns, in the order named, and the schema header narrows with the rows: a header promising columns the rows do not carry is worse than no header at all. An unknown name is a usage error (exit 2) naming the whole vocabulary, never a silently narrower result. `<command> --help` lists every field name.
- `--json` is retained everywhere it existed, byte for byte unchanged. TOON is the default because the consumer is an agent; JSON stays because a caller that wants a parser-backed object should not have to parse TOON to build one. `--fields` narrows the TOON schema only, so combining it with `--json` is a usage error rather than a silently ignored request.

`hand watch` is the one command outside this contract: its stdout is a per-line event stream a supervisory agent tails as it arrives, so it stays a line protocol (see "`hand watch`").

## CLI specification

### `hand`

The bare command introduces the binary and reports the fleet in one document, rather than printing the help screen cobra defaults to.
What a caller with nothing to go on needs first is the state; `hand --help` is one word away for the command reference.

```
tool: hand
purpose: manages a fleet of coding agents - one worker per task in its own worktree and herdr pane
version: 0.1.4
exec: ~/.local/bin/hand
home: ~/secondhand
count: 2
attention: 1
held: 0
tasks[2]{id,state,reported,age,flags}:
  fix-login,working,working,2h ago,none
  audit-deps,done,done,20m ago,unacknowledged
holds[0]{id,kind,detail,age}:
help[3]:
  - Run `hand status <id>` for one task's detail and report history
  - A flagged row is waiting on you: `hand send <id> <message>` to steer it, `hand hold set <id> --kind operator --reason <text>` to park it
  - Run `hand status --fields <a,b>` to pick columns, `hand status --help` for every field name
```

`exec` names the executable that answered, with the user's home abbreviated to `~`, so a caller who has more than one `hand` on `PATH` can tell which one it reached.
Everything from `count` down is `hand status`'s fleet overview with its default fields, built by the same code, so the two can never disagree.

Outside a fleet home the identity fields still print, `home` is `none`, and the help block names the way in:

```
tool: hand
purpose: manages a fleet of coding agents - one worker per task in its own worktree and herdr pane
version: 0.1.4
exec: ~/.local/bin/hand
home: none
help[2]:
  - Run `hand init` in the directory that should become the fleet home, or point HAND_HOME at one that already exists
  - Run `hand --help` for the command reference
```

Exit `0`, unlike every other command's `3` for an unresolvable home: this is the one surface whose job is to introduce the tool, and a caller who has not set a home up yet is exactly who runs it.

### `hand init [path] [flags]`

Initialize secondhand runtime directories in the current working directory.
Creates `state/`, `data/`, `projects/`, `config/` if they don't exist.
Creates `data/backlog.md`, `data/projects.md`, `data/operator.md`, `data/learnings.md`, `data/done-archive.md` and `data/note-archive.md` with skeleton content, and creates `state/hand.db` if it does not already exist - the fleet-home marker `IsHome` checks for (see "Core principles").
A skeleton is written only when the file is absent, so re-running `hand init` in an existing home is how it picks up a file the layout gained since it was initialized, and never how it loses what is in one.
`hand update` seeds the same skeletons the same way, so an existing home picks a new layout file up whether it is re-initialized or updated.
Also installs the ambient-context session hook described below.
Idempotent: safe to run multiple times.
This is the one command that does not resolve its home: it creates the one its argument or the working directory names.
When `HAND_HOME` is set and names some other directory it still initializes the requested target, and warns on stderr that every other command will use `HAND_HOME` instead, naming it as the absolute path those commands resolve it to so a relative `HAND_HOME` is not mistaken for a second home.

```
hand init
hand init --setup
```

Flags:
- `--setup`: run interactive first-time setup. Discovers available harnesses on PATH (claude, codex, pi, grok, opencode) and available tools (treehouse, herdr, no-mistakes, gh), then asks the user for the default worker harness, model, and effort and writes `config/harness`, `config/model`, and `config/effort`.

Output:
```
result: initialized
home: /path/to/secondhand
agents_md: written
session_hook: written
harness: none
model: none
effort: none
help[3]:
  - Run `hand project add <repo-url>` to register the first project
  - Read AGENTS.md in this home for how a supervising agent is meant to drive it
  - A Claude Code session started in this home now opens with the fleet already in context
```

`agents_md` and `session_hook` are each `written` or `unchanged`, so a re-run says which of the two
it had to touch rather than going silent about both.

`--setup` is a dialog with whoever is at the terminal, so its discovery lines and prompts stay plain
lines (`found harnesses: ...`, `found tools: ...`, a numbered harness menu, then a prompt per value).
Only the answers reach the document, which follows the dialog with `harness: claude`, `model: sonnet`
and `effort: low` in place of the three `none`s above. Without `--setup` nothing was chosen, so all
three read `none` rather than being dropped: the schema is the same either way.

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
   Then list `treehouse.toml` in the clone's `info/exclude`, since treehouse leaves it untracked and
   an untracked pool config makes the clone read dirty to every later `hand project sync`.
7. Add the project to the store and rewrite the `data/projects.md` projection.
8. If clone or init fails, clean up partial state (remove the clone dir, don't append to registry).

Output:
```
name: nsr
result: added
mode: direct-pr
url: "https://github.com/yes2games/nsr"
clone: /home/user/fleet/projects/nsr
help[1]:
  - Run `hand spawn <id> nsr` to dispatch a worker into it
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
hand project list --fields name,mode
hand project list --json
```

Flags:
- `--fields <a,b,c>`: which columns the `projects` block emits, in the order given. Any of `name`, `mode`, `url`, `upstream`, `gate`; all five by default. An unknown name is a usage error (`2`) naming the known ones.
- `--json`: the raw registry objects, unchanged from earlier versions. Rejects `--fields` as a usage error (`2`) rather than silently handing back the full object the caller asked to narrow.

Output:
```
count: 3
gate_issues: 1
projects[3]{name,mode,url,upstream,gate}:
  nsr,direct-pr,"https://github.com/yes2games/nsr",none,none
  yes2infra,no-mistakes,"https://github.com/yes2games/yes2infra",none,not initialized
  no-mistakes,direct-pr,"https://github.com/atqamz/no-mistakes",kunchenguid/no-mistakes,none
help[2]:
  - Run `hand spawn <id> <project>` to dispatch a worker into one of these
  - A project with a gate value cannot honour its no-mistakes mode until that is fixed; `hand doctor` and the project's own clone are where to look
```

Output (JSON):
```json
[
  {"name": "nsr", "url": "https://github.com/yes2games/nsr", "mode": "direct-pr"},
  {"name": "yes2infra", "url": "https://github.com/yes2games/yes2infra", "mode": "no-mistakes", "gate_issue": "not initialized"},
  {"name": "no-mistakes", "url": "https://github.com/atqamz/no-mistakes", "mode": "direct-pr", "upstream": "kunchenguid/no-mistakes"}
]
```

Every column carries a value on every row: a project with no declared upstream reads `none` rather
than dropping the cell, so the row stays aligned with the schema header. JSON keeps its
omit-when-absent shape (`upstream`, `gate_issue`).

A `no-mistakes`-mode project whose gate cannot currently be honoured (not initialized, or
`unreachable` - the binary itself missing, the clone path missing on disk, or the clone path
existing but not a git repository; see "Gate preflight") carries that text in its `gate` column and
its `gate_issue` field in JSON. `gate_issues` counts them, so a caller learns whether any project is
unspawnable without reading every row; a nonzero count adds the `help[]` line above saying where to
look. Every `no-mistakes`-mode project pays one `no-mistakes status` call per
`hand project list` invocation; other modes pay nothing.

An empty registry is `count: 0` with the schema header and a `help[]` line naming
`hand project add`, never silence.

---

### `hand project upstream <name> <repo>`

Declare which repo a fork project opens its PRs against, so `hand pr` accepts a PR living there
instead of on the fork hand pushes to, and gate-opened-PR detection looks for one there as well as on
the project's own repo (see "Project registry format", and `hand status` for the detection rule). Pass
an empty `<repo>` to clear the declaration.

```
hand project upstream no-mistakes kunchenguid/no-mistakes
hand project upstream no-mistakes https://github.com/kunchenguid/no-mistakes
hand project upstream no-mistakes ""
```

Behavior:
1. Normalize `<repo>` to an `owner/repo` slug, accepting a bare slug or any remote URL form. Refuse
   anything that cannot be resolved to one: an unresolvable upstream would widen the PR guard to
   whatever the comparison happened to fall through to. A slug containing whitespace is refused for a
   second reason: the `data/projects.md` projection separates fields by whitespace, so a stored one
   reads back truncated and rejects the whole registry line for every later project command.
2. Write it onto the project's row and rewrite the `data/projects.md` projection, under the project
   lock.

This is a separate command rather than a `hand project add --upstream` flag because a fork project is
usually already registered by the time the first upstream contribution comes up, and `hand project
add` cannot be re-run against an existing clone.

Output:
```
name: no-mistakes
result: upstream-set
upstream: kunchenguid/no-mistakes
```

Output (cleared):
```
name: no-mistakes
result: upstream-cleared
upstream: none
```

Errors:
- `<repo>` cannot be resolved to `owner/repo` (usage error, code `2`).
- Project not found in registry.

---

### `hand project remove <name>`

Unregister a project. Does NOT delete the clone under `projects/`.
Refuses if any active task references this project.

```
hand project remove nsr
```

Output:
```
name: nsr
result: removed
clone: /home/user/fleet/projects/nsr
help[1]:
  - The clone is retained; delete it by hand if the registration was the only thing holding it
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
- `--skip-gate-check`: dispatch into a `no-mistakes` project even if its gate is not initialized, its
  clone path is missing from disk, or that path is not a git repository (see "Gate preflight").

Model and effort resolve most-specific-first: the flag, then the brief's `---` declaration (see
"Brief format"), then the config default, then unset.

Anything the chosen harness cannot carry is a warning on stderr, not a failure: the spawn proceeds,
with a resolved model or effort recorded in state and ignored by the launch command. Everything a
launch drops is named on one line rather than one line each, since consecutive warnings all naming
the same harness read as separate problems. What can be dropped:

- a resolved effort under anything but claude (`harness.SupportsEffort`)
- a resolved model under `codex`, `grok` or `pi` (`harness.SupportsModel`)
- the operator-decision rule, and the front-matter disclaimer when the brief has front matter,
  under `codex`, `grok` or `pi` (`harness.CarriesPrompt`, see "Harness launch templates")

The line reads `warning: harness "codex" cannot carry model "opus", effort "high", the
operator-decision rule, the front-matter disclaimer; launching anyway`, listing only what that
launch actually drops.

The warning belongs to a launch that is about to happen, so the tier resolves only after the
project, gate preflight and brief checks have passed. A run that refuses names its refusal and
nothing about a launch it never performs; `hand promote` resolves the tier in that same position
(atqamz/secondhand#156).

Behavior:
1. Validate project exists in registry.
2. If the project's mode is `no-mistakes`, run the gate preflight check (see "Gate preflight");
   refuse before touching any task or worktree state if it comes back not initialized or
   unreachable, unless `--skip-gate-check` is set.
3. Validate no active task with this ID exists.
4. Validate no hold is set on this ID (see "Holds" under "State management"), whatever its kind: an `operator` or `blocked` hold outlives the teardown of the task it was set on, so reusing the id for new work would reattach the previous incarnation's open question to an unrelated task, and a `limit` hold means a worker is still on this id, merely out of quota. The error names `hand hold clear <id>` as the remedy.
5. Validate `data/<id>/brief.md` exists (the agent must write it before spawning).
6. Acquire a treehouse worktree: `treehouse get --lease --json --lease-holder hand:<id>`, run inside the project clone (treehouse resolves the pool from cwd). Both the slot path and the lease identity treehouse returns are kept, and both are recorded on the task row.
7. **Collision guard:** cross-check the acquired lease against every other task row in the store. If one matches, return the worktree to treehouse and fail with an error naming the conflicting task. See "Collision guard" under "State management" for what counts as a match, and why it is the lease identity rather than the worktree path.
8. Acquire the task's herdr tab in the project's workspace.
   - Workspace and tab labels: one workspace per project, one tab per task - see "Workspace and
     tab model" under "Herdr integration detail" for the labels themselves.
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

Any failure before step 12 leaves nothing behind: the worktree lease returns to treehouse and the
herdr side is rolled back.
A workspace this command created is closed whole: the task's tab is that workspace's own
auto-created root tab, so there is nothing else in it to preserve.
A workspace that already existed is shared with other tasks, so it keeps running and only loses
the tab this command added.
Once the task's row is written the task owns its worktree and tab: nothing after that step can
tear down a task that is already running.

Output:
```
id: fix-login
result: spawned
project: nsr
kind: ship
harness: claude
worktree: /home/user/.treehouse/nsr-abc/1/nsr
help[2]:
  - Run `hand status fix-login` to read what this worker reports
  - Run `hand send fix-login <message>` to steer it
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
- Worktree collision with another task row (names the conflicting task; see "Collision guard").
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
  "report_digest": "",
  "pr_merged_observed": false,
  "done_verified": false,
  "created_at": "2026-07-24T10:00:00Z",
  "pane_started_at": "2026-07-24T10:00:00Z",
  "status_changed_at": "",
  "status_changed_for": "",
  "parked_fired_for": "",
  "usage_limit_retry_at": "",
  "usage_limit_attempts": 0,
  "last_report_state": "",
  "last_report_note": "",
  "send_undelivered_message": "",
  "send_undelivered_at": "",
  "lease_id": "5fe5412a4aabdeb85a148d6d73eb42d8",
  "delivered_at": "",
  "delivered_reason": ""
}
```

---

### `hand status [id] [flags]`

Show fleet overview or single-task detail.

```
hand status
hand status fix-login
hand status --fields id,state,pr
hand status --json
hand status fix-login --full
```

Flags:
- `--fields <a,b,c>`: emit these columns instead of the view's defaults, in the order named. One vocabulary serves both views, so a field means the same thing wherever it is asked for: `id`, `project`, `kind`, `harness`, `model`, `effort`, `state`, `reported`, `report`, `age`, `created`, `last_report`, `pr`, `worktree`, `herdr`, `brief`, `delivered`, `held`, `gate`, `report_file`, `flags`. See "Output shape" for the rules this flag follows everywhere.
- `--json`: output as JSON instead of TOON. Always carries the reported line and history untruncated - a machine consumer wants the whole field, and silently truncating a JSON field is a data-loss bug, not a rendering choice.
- `--full`: in the single-task view, show the reported line and history untruncated and skip the history dedup below, reproducing the pre-atqamz/secondhand#65 content exactly.

Behavior (fleet overview):
1. List every task in the store.
2. For each, query herdr for current agent state.
3. Carry the worker's own last classified report in the `reported` column, whatever the pane is doing, and `none` when it has never reported. A pane state and a report answer different questions, so both are emitted as their own column: a worker that appends `paused:` while its harness keeps running used to render as a bare `working`, showing the pane and hiding the only party that had said why. A report file that exists but can't be read reads `unreadable` there, which is why that column is never the place the `unreported` flag lands - an I/O fault is not evidence the worker never reported.
4. Derive the `flags` column, one space-separated token per marker so a caller can test for one without parsing prose, and `none` when a task carries no marker at all. `unreported` requires both a not-busy pane (herdr's `idle` or `done` - see "Agent state" below) and a last word of `working` or nothing, since a busy pane that has not reported yet is not a stop anyone has to explain. `report-unreadable` is the I/O fault above. `unacknowledged` is a terminal report (`done`, `failed`) no `hand watch` has consumed; see "Unacknowledged terminal reports" below. `delivered` is a task closed out by `hand deliver`. `merged` and `merged-external` are the merge state of a recorded PR - `merged` when `hand` performed the merge, `merged-external` when `hand` only observed it, whether `hand watch`'s own `gh` poll saw it merged or gate-opened-PR detection recorded a PR that was already merged - carried whatever the agent state is, since a merged PR is a fact about the PR rather than about the pane. `gate-no-run-found` and `gate-unreachable` come from step 6.
5. Emit `count`, `attention` and `held` as scalar fields above the rows. `count` is the number of tasks, `attention` the number of them a supervisor has to look at, and `held` the number of open holds. `attention` counts a task whose report is `paused`, `blocked`, `needs-decision` or `failed`, or that carries `unreported`, `unacknowledged`, `report-unreadable` or a gate flag: the point is that a reader who only takes the aggregate learns whether anything wants them without reading a single row.
6. For a `ship` task reported `done` with a recorded PR, on a registered `no-mistakes` project: check whether that PR ever went through a gate run (see "Gate-run visibility" below), and set the `gate-<issue>` flag when it did not. The project registry read this step needs is best-effort - a registry fault leaves the check silent for every task rather than failing the whole overview over it, but prints a one-line `warning:` to stderr naming the read failure, so dropping every gate flag fleet-wide is never silent.
7. Emit the `tasks[N]{...}` block, then the `holds[N]{...}` block, then `help[N]`. Both row blocks print their header at `N` of zero, so an empty fleet is a positive statement rather than the same bare output a broken command could also produce, and a torn-down task's still-open hold is never hidden behind an absent task table. A hold names any id, not only a live task's, so it keeps appearing here after its task row is gone. A failure to read the holds fails the whole command rather than degrading to an empty list - reading no holds back must never be mistaken for nothing being held.

The `last_report` column is the mtime of `state/<id>.status`, and `none` when the worker has never written one.
It is deliberately not the task's age: the two used to be conflated, so a task spawned hours ago read as hours stale next to a status file its worker had touched minutes earlier - a reporting worker that looked abandoned.
`age` measures the task, `last_report` measures the channel.

Output (fleet overview):
```
count: 9
attention: 5
held: 2
tasks[9]{id,state,reported,age,flags}:
  fix-login,working,working,2h ago,none
  dark-mode,blocked,none,45m ago,none
  build-wait,working,paused,20m ago,none
  stuck-task,idle,none,1h ago,unreported
  paused-task,idle,needs-decision,30m ago,none
  quiet-done,idle,done,15m ago,unacknowledged
  investigate,done,done,10m ago,none
  shipped-fix,done,done,5m ago,merged-external
  no-gate-fix,done,done,3m ago,gate-no-run-found
holds[2]{id,kind,detail,age}:
  fix-login,operator,"two ways to fix this, needs a call",2h ago
  torn-down-task,operator,question never answered,1d ago
help[3]:
  - Run `hand status <id>` for one task's detail and report history
  - A flagged row is waiting on you: `hand send <id> <message>` to steer it, `hand hold set <id> --kind operator --reason <text>` to park it
  - Run `hand status --fields <a,b>` to pick columns, `hand status --help` for every field name
```

The default columns are the five that answer "what is running and what wants me"; the rest of the vocabulary is one `--fields` away, and narrowing it narrows the schema header with it:
```
$ hand status --fields id,pr,gate
count: 9
attention: 5
held: 2
tasks[9]{id,pr,gate}:
  fix-login,none,none
  ...
```

A hold row that can't be trusted at face value - an unrecognized kind, a `blocked` hold with no `blocked_on`, or an `operator` or `limit` hold carrying one - is still emitted, never dropped, with `inconsistent: <why>` in place of its `detail`: an external write to `state/hand.db` is the only way such a row exists, since `hand hold set` validates before writing, and filtering it out here would silently drop the row most worth seeing.

Output (fleet overview, empty):
```
count: 0
attention: 0
held: 0
tasks[0]{id,state,reported,age,flags}:
holds[0]{id,kind,detail,age}:
help[2]:
  - Run `hand project list` to see which projects are registered
  - Run `hand spawn <id> <project>` to start a worker
```
The `holds[N]` block still carries any open hold when there are no tasks at all (step 7 above).

Behavior (single task):
1. Read the task from the store.
2. If the task is a `ship` task with no PR recorded and its project is registered and not `local-only`: look for a PR on the project's repo whose head ref is the task's current branch (never matched on title, issue number, or task id), and record it under the task if found - a no-mistakes gate's own `pr` step opens a PR directly, bypassing `hand pr`, so `pr` can go unrecorded for genuinely landed work. A project with a declared `upstream` searches that repo too, since a fork contribution's PR is opened on the upstream while the branch is pushed to the fork (atqamz/secondhand#134). Only PRs whose head branch lives in the project's own repo count there: an upstream carries head refs from every contributor's fork, and a head ref is matched on the branch name alone, so a stranger's same-named branch would otherwise be recorded as this task's PR. Every repo-slug comparison in this lookup folds case, since a GitHub slug is unique only up to casing: the head-repo filter matches what `gh` reports in GitHub's canonical casing against a slug derived from whatever casing the clone's `origin` remote carries, and an `upstream` naming the project's own repo in another casing is that same repo, so it is not searched a second time. Compared exactly, the first drops a landed PR and the second returns it twice, making it its own same-tier duplicate (atqamz/secondhand#146). A branch carrying several PRs resolves by preference tier - merged, then open, then closed-unmerged - and only when the winning tier holds exactly one PR: a tier with more than one match is ambiguous, and so is a merged PR coexisting with an open one on the same head ref - an open PR is live evidence the branch may carry unlanded work, so that mix refuses rather than resolving to the merged PR. Matches from both searched repos resolve through that one tier pass, so a fork whose upstream also carries a PR on that branch name is ambiguous exactly like two PRs in one repo, and each candidate is named with its repo (`owner/repo#N`) rather than a bare number an operator would have to hunt for. A `scout` task is skipped: its deliverable is `data/<id>/report.md`, never a PR. This is a best-effort, non-blocking lookup (a held task lock, an unreachable `gh`, an ambiguous branch, or a task with no branch all just leave the command reporting what it read) so a fleet-wide `hand status` never pays this cost.
3. Query herdr for current agent state and recent output.
4. Read the task's report channel (see "Report channel") and show its last 5 lines. The read covers the whole file even though only 5 lines are shown, since the unacknowledged check below is answered from all of it, exactly as the fleet overview answers it - a window would let trailing free text hide from one view a completion the other flags. A report file that exists but can't be read degrades exactly as it does in the fleet overview: the `report` field reads `report unreadable: <error>` and the rest of the detail view still prints, rather than the command failing and showing nothing. A terminal report no watcher has consumed appends ` (unacknowledged)` to that field; see "Unacknowledged terminal reports" under "Report channel".
5. Read the hold on this id, if any (see "Holds" under "State management"). Unlike the report channel, a failure to read it fails the command - the same reasoning as the fleet overview's `holds[N]` block.
6. If the task is a `ship` task reported `done` with a recorded PR: look up its project (unlike the fleet overview's best-effort registry read, a failure here fails the command - this id's own project is the one fact the check is about) and, if it is a registered `no-mistakes` project, check whether that PR ever went through a gate run (see "Gate-run visibility" below).
7. Emit the selected fields, then `report_history[N]`, then `help[N]`.

Output (single task):
```
id: fix-login
project: nsr
kind: ship
harness: claude
model: sonnet
state: working
worktree: /home/user/.treehouse/nsr-abc/1/nsr
herdr: "default/wA:tB"
age: 2h ago
last_report: 3m ago
pr: none
reported: needs-decision
report: "needs-decision: two ways to fix the race, ask-user found both risky"
delivered: none
held: "waiting on migrate-schema: needs the new column before this can proceed"
gate: none
flags: none
report_file: /home/user/secondhand/state/fix-login.status
report_history[1]:
  - working: added the retry loop
help[1]:
  - Run `hand send fix-login <message>` to answer this worker
```

One item is not a list, so this view defaults to every field the fleet overview leaves out rather than to five of them; `--fields` narrows it the same way, drawing from the same vocabulary.
Every default field is emitted every time, `none` when it has nothing behind it, so the shape a caller parses never changes with the content: `pr` reads `none` with no PR recorded, `held` only carries a detail when this id has a hold, and `gate` only when the gate-run check came back `no run found` or `unreachable`.
That is also why the merge state, the gate issue and the unacknowledged marker live in `flags` here exactly as they do in the fleet overview, rather than as suffixes on the fields they qualify.

`held` reads the reason alone for an `operator` or `limit` hold, `waiting on <blocked_on>: <reason>` for a `blocked` one, and `inconsistent: <why>` for a row that can't be trusted at face value (see the fleet overview above). A `limit` hold's reason is written by `hand watch` and says which resume attempt it is on and when it next tries (see "Resuming a usage-limited worker").
`report` is the worker's own last claim about itself, not something `hand` has verified - the same caution as the `done`-vs-`reported-done` distinction in `hand watch` - and so is every entry in `report_history`.
The field names carry that framing, which is why no invocation repeats it in prose.

atqamz/secondhand#65: a worker's report prose has run several KB for a single task, and rendering it in full doubled the cost by repeating the latest entry - once as `report`, again as the last entry of `report_history`. Without `--full`:
- The `report` field and every history entry are capped to 200 runes (a character budget, not a word or line count, since the point is bounding rendered size). The cut lands after the state-vocabulary prefix (`working:`, `paused:`, `blocked:`, `needs-decision:`, `done:`, `failed:`) - the prefix is never part of what's cut - and a cut entry carries the recovery hint every truncated field carries (see "Output shape"), naming `hand status <id> --full` as what returns the whole text. `done: <PR url>` stays intact under this budget in the common case, since the worker convention puts the URL immediately after the prefix and 200 runes covers it comfortably; a URL buried after long prose is the same brief-authoring problem the write side already owns (see "Report channel").
- `report_history` drops the entry the `report` field already carries, so the same report is never emitted twice in one invocation. That is whichever entry the field actually rendered, not simply the last one: an unacknowledged terminal report followed by free text renders the terminal line, so the free text is what stays in the history block, and a terminal report the free text pushed out of the 5-line window drops nothing.
- The `report_file` field names the absolute path to `state/<id>.status`, so nothing is lost: the full text stays on disk and the path to it is one field away. It is emitted whether or not anything was cut, since a field that appears only when the content is long makes the schema a function of the content.
- The truncation adds a `help[]` line naming `hand status <id> --full`, so the recovery is stated once more where a caller looks for what to run next.

`--full` restores the pre-#65 content: `report` and `report_history` untruncated, and the latest entry repeated in history.

`--json` is never truncated or deduped, `--full` or not - see the JSON section below.

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

`reported` and `report_history` are omitted when the task has no report file yet, and so is `last_report_at`. `unacknowledged` is omitted unless it is true (see "Unacknowledged terminal reports" under "Report channel"). `held` is omitted when this id has no hold; an inconsistent hold (see the fleet overview above) adds an `inconsistent` field naming why instead of being omitted. `gate_run_issue` carries the same `no run found` / `unreachable` text as the `gate` field above and is omitted whenever that field would read `none` - not a `done` `ship` task with a recorded PR on a registered `no-mistakes` project, or the check came back clean - which is why the example above, with no PR recorded, does not carry it.

Fleet-overview JSON wraps the per-task rows rather than returning a bare array, so holds - which can outlive the task that had them - have somewhere to sit alongside it, and `task_count` alongside that, always present (never omitted, zero included) so an empty fleet is a positive statement rather than the same absence of output a broken command could also produce:

```json
{
  "task_count": 1,
  "tasks": [{"id": "fix-login", "...": "one row per task, reported only, no history"}],
  "holds": [{"id": "fix-login", "kind": "blocked", "reason": "needs the new column before this can proceed", "blocked_on": "migrate-schema", "set_at": "2026-07-24T09:00:00Z"}]
}
```

An empty fleet returns `{"task_count": 0, "tasks": [], "holds": [...]}` - `holds` still carries any
open hold, never suppressed by there being no tasks.

Each row in `tasks` carries `gate_run_issue` under the same omission rule as the single-task JSON above.

Errors:
- Task ID not found.
- Herdr unreachable (graceful degradation: show state as "unknown").
- The hold store can't be read (fails the command; never degrades to an empty `holds[N]` block - see "Holds" under "State management").
- The single-task view's own project can't be read while checking gate-run visibility (fails the command; the fleet overview's equivalent lookup is best-effort instead - see behavior step 6 above).
- `--fields` names a field no view carries, or is combined with `--json` (usage error, exit 2 - see "Output shape").

---

### `hand send <id> [message]`

Send a text message to a running worker's herdr pane.

```
hand send fix-login "focus on the auth middleware, not the test framework"
hand send fix-login --file data/fix-login/steer.md
```

A busy composer (agent mid-response) is the normal state a steer arrives into, not an error
condition (atqamz/secondhand#102): `hand send` waits for it to free rather than failing
immediately, so a caller does not have to reimplement that retry in shell. The wait is bounded,
not indefinite - a composer that stays busy for the whole bound means the message never reached
the pane, and that has to surface as a distinct, terminal-for-this-invocation outcome rather than
a hang.

Flags:
- `--file <path>`: read the message from this file instead of the positional argument, trailing
  newlines trimmed. A multi-paragraph steer going through the shell as a positional argument has
  to survive quoting, embedded newlines, and backticks intact; `--file` is the way to send one
  without gambling on that. Mutually exclusive with the positional `message` - exactly one of the
  two is required.
- `--wait <duration>`: how long to wait for a busy composer to free before giving up. Default
  `config/send-wait`, or `2m` if that is unset too. Long enough that an ordinary agent turn does
  not need the caller to retry at all, short enough that an invocation still returns in bounded
  time; a steer into an unusually long-running turn is a `--wait` argument, not a bigger polling
  loop.

Behavior:
1. Read the task's row for herdr pane coordinates.
2. Check herdr pane exists and agent is present.
3. Acquire a per-task send lock (`send:<id>`), held for the rest of this list. A second `hand send`
   against the same task waits behind the first rather than polling the same pane at the same
   time - two unsynchronized retry loops racing the same busy composer is the exact hazard
   atqamz/secondhand#102 traced a lost steer to. `hand watch`'s usage-limit resume attempt is the
   only other holder of this lock, and it takes it without waiting (see "Resuming a usage-limited
   worker" under `hand watch`).
4. If the composer is busy, poll until it frees or `--wait` elapses.
5. Whenever the message does not demonstrably land in the pane - `--wait` elapses first, the text
   fails to send, or the submit keystroke fails after the text went in - durably record the message
   and a timestamp on the task row (`send_undelivered_message`, `send_undelivered_at`) under a
   separate, short-lived task-row lock, so a steer that never arrives leaves a trace instead of
   vanishing with the process that attempted it. Only the elapsed-`--wait` case exits distinctly
   (see "Exit codes"); the two delivery failures are ordinary exit-1 errors. This lock is not held
   for the wait itself: a `hand send` waiting out its bound must not stall `hand watch`'s polling
   or a `hand status` read on the same task.
6. Otherwise, submit the message text and clear any previously recorded undelivered-send trace on
   the task row (whatever message that trace carries) - a send that reaches the pane moots it,
   whether or not it is a retry of the exact message that was previously abandoned. A failure to
   clear the trace warns on stderr and still succeeds: the message is already in the pane, so
   failing here would invite a retry that double-sends the steer.

Output:
```
id: fix-login
result: sent
chars: 42
help[1]:
  - The pane has the message; run `hand status fix-login` to read what it does with it
```

`chars` counts runes, not bytes, and is the one thing the caller cannot see for itself: what reached
the pane is the message as `hand` read it, `--file` or argument.

Errors:
- Task not found (exit 3).
- Neither or both of the positional `message` and `--file` given (exit 2).
- Invalid `--wait` value (exit 2 from the flag, exit 1 from a bad `config/send-wait`).
- Herdr pane doesn't exist (agent died, tab closed) - exit 1, distinct from a busy composer because
  this send can never succeed, no matter how long it waits. This covers a pane that stops answering
  partway through the wait too, not only one already gone when the wait starts.
- Sending or submitting the message text failed - exit 1. The message is recorded as undelivered
  (step 5): text that never left, and text left unsubmitted in the composer, are both a steer with
  no evidence it landed.
- Composer still busy after `--wait` elapses - exit 6, distinct from the pane-not-found case above:
  this is a transient state a caller can retry (e.g. with a longer `--wait`), not a terminal
  failure. The message is durably recorded as undelivered rather than lost; see step 5 above.

---

### `hand hold set <id> --kind <kind> --reason <reason> [--blocked-on <id>]`

Record that an id is waiting on something (atqamz/secondhand#63). See "Holds" under "State management" for the design this command exposes.

```
hand hold set fix-login --kind operator --reason "two ways to fix this, needs a call"
hand hold set fix-login --kind blocked --reason "waiting on the migration task" --blocked-on migrate-schema
```

`<id>` is any id, not only a live task's - see "Holds" for why. It is validated with the same charset as a task id (`state.ValidateID`), never read back against the task table.

Flags:
- `--kind`: `operator` (waiting on a human) or `blocked` (waiting on another id). Required. `limit` is a valid hold kind but not a valid value here - it is `hand watch`'s (see "Holds").
- `--reason`: why the id is waiting. Required.
- `--blocked-on`: the id being waited on. Required for `--kind blocked`, refused for `--kind operator`.

Behavior:
1. Validate `--kind`, `--reason`, and the `--blocked-on` pairing.
2. Upsert the hold row - a second `hand hold set` on the same id replaces the previous kind, reason, and blocked-on rather than requiring a clear first, and refreshes `set_at` to when it was last set.

Output:
```
id: fix-login
result: held
kind: operator
reason: two ways to do this, needs a call
blocked_on: none
help[1]:
  - `hand status` carries this in its holds block until `hand hold clear fix-login`
```

Errors:
- Invalid `--kind` (exit 2).
- `--kind limit` (exit 2). Refused with its own message rather than the generic one: the kind exists and an operator may well have seen one in `hand status`, so the error says who sets it and when it clears instead of claiming it is not a kind at all.
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

Every kind is clearable, `limit` included: it is the operator's way out of a hold `hand watch` set on their behalf (see "Holds"). Clearing one does not cancel the resume schedule behind it, which lives on the task row - the next `hand watch` tick that attempts or ends the limit writes the hold back or leaves it gone accordingly.

Output:
```
id: fix-login
result: released
help[1]:
  - Run `hand status fix-login` for where that task stands now that nothing holds it
```

Errors:
- No hold set on `<id>` (exit 3).

---

### `hand deliver <id> --reason <text>`

Record that a task's work is handed off and the decision to land it belongs to someone outside the fleet.

```
hand deliver no-mistakes-flake --reason "PR https://github.com/kunchenguid/no-mistakes/pull/597 offered upstream, maintainer decides"
hand deliver event-delivery-scout --reason "report at /home/atqa/secondhand/data/event-delivery-scout/report.md, no code to land"
```

`hand teardown`'s landed-work guard asks "is this landed", and for a contribution offered to a repo the fleet does not control - a fork PR on an upstream, a deliverable that is a report rather than a commit - landing is someone else's decision, possibly never taken. That is a real terminal state the guard could not express, and `--force` is the wrong way to express it: it records the task as `torn-down`, indistinguishable from work abandoned unlanded. This command is that state, on the same pattern as the gate-opened-PR case (atqamz/secondhand#69): name the state the guard was missing rather than widen the guard.

Behavior:
1. Refuse without `--reason`: the record has to say what was delivered and who decides whether it lands, not merely that something was.
2. Write `delivered_at` (now, UTC) and `delivered_reason` onto the task's row, under the task lock.

Re-running with a new reason is a correction rather than a conflict, unlike `hand pr`'s one-task-one-PR rule: nothing consumes the mark until teardown reads it, so the last word on what was delivered is the one worth keeping.

The state is keyed off the recorded delivery, never off `kind`, so a task filed as a ship whose deliverable turned out to be a report tears down cleanly without anyone correcting the kind first (atqamz/secondhand#129).

`hand status` shows it: a `delivered` token in the fleet view's `flags` column, a `delivered` field carrying the reason in the single-task view, and `delivered_at`/`delivered_reason` in `--json`. It never sets `merged` or `pr_merged_observed`, which both assert the work landed.

Output:
```
id: no-mistakes-flake
result: delivered
reason: "PR 597 offered upstream, maintainer decides"
delivered: "2026-07-29T11:04:00Z"
help[1]:
  - Run `hand teardown no-mistakes-flake` once the work is landed to release the worktree and pane
```

Errors:
- `--reason` missing (usage error, code `2`).
- Task not found.

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
2. If the task is recorded as delivered (`hand deliver`), stop here and treat the work as terminal: every remaining check below asks "did this land", which for a contribution offered to a repo the fleet does not control is not the fleet's question to answer. Deliberately after step 1 and after the scout task's own report check, so `--force` keeps its one meaning of discarding work nobody delivered: uncommitted changes still refuse, and a scout row claiming delivery with no report on disk has delivered nothing.
3. Check work is landed:
   - If mode is `local-only`: verify the branch is merged into the default branch.
   - Otherwise, if `pr` is not yet set in state and the project is registered: look for a PR on the project's repo, and on its declared `upstream` when it has one, whose head ref is the task's current branch, and record it under the task if found (same gate-opened-PR detection `hand status` performs, including the preference-tier rule for a branch with several PRs; see that command's spec). Detection failing because there is nothing to find (no clone on disk, `gh` unreachable) is not itself an error - it falls through to the same refusal below as if no PR existed. An ambiguous branch is different: teardown refuses outright rather than falling through to "no PR recorded" - that message means unlanded, and guessing which of an ambiguous set to trust is the failure this rule exists to remove. The refusal names every PR on the head ref with its repo and state, including one in a losing tier that did not itself trigger the refusal, since the operator has to resolve the whole branch, not just the pair that tripped the rule. A branch carrying both a merged PR and an open one is one such ambiguous case: the open PR is live evidence of unlanded work, so it is never silently resolved to the merged PR.
   - If `pr` is set in state (recorded by `hand pr`, or just detected above): verify the PR is merged via `gh pr view`. A detected PR that is closed without merging is refused exactly like one `hand pr` recorded.
   - If no PR is found and the row carries no merge evidence (`merged`, `pr_merged_observed`), but `data/<id>/report.md` exists and the worktree's branch adds no commit to the local default branch, treat the task as a completed scout: stop here and record the completion as `kind: scout`, outcome `done`. Otherwise refuse: work may not be landed.
     This is atqamz/secondhand#129. `kind` is the one field `hand spawn` records that nothing can correct afterwards - `hand promote` only goes scout to ship - so a scout spawned without `--scout` arrives at teardown as a ship row whose shape (a report, no PR) is exactly what the landed-work guard refuses, and `--force` plus a respawn was the only way out: forcing past a work-may-not-be-landed guard to fix a metadata typo. The fix makes the guard right rather than escapable by reading the work instead of the record.
     Both halves of the condition are required, and the branch check is the load-bearing one: a ship task whose PR was never opened still carries its commits, so it still refuses. "No PR and some file exists" would accept it and discard them. The branch comparison is local-only and fails closed exactly like step 1's, so an unresolvable base ref refuses rather than accepts.
     It is decided here, last, so it can only answer the case nothing else claims: a delivery, a local-only merge, and a gate-opened PR all stop above it, so reaching it means no PR exists for it to shadow. Merge evidence excludes it outright for the same reason - `hand promote` keeps the report on disk while turning the row into a ship, so a promoted scout that then merged locally has every shape this case reads, and it landed as a merge. Recording it as a report would claim a merge never happened, the inverse of the atqamz/secondhand#78 accuracy rule.
4. Close the herdr tab.
5. Return the worktree to treehouse: `treehouse return <path>`. `--force` is added whenever step 1 proceeded past dirt it judged safe, as well as under the command's own `--force`: treehouse refuses to clean a dirty worktree without it and there is nothing here to answer its prompt, so an unforced return would either abort after the tab is already closed or hand the pool a slot that is still dirty.
   That abort is the one treehouse failure its exit status does not report - it prints the abort and still exits 0 with the slot leased - so the return is judged on the output and refused, never taken for a returned worktree.
   A scout is the path that reaches it: its checks read the report on disk and never the worktree, so dirt is still in place when the return runs.
6. Append a completion record to `state/completions.jsonl` (see "Completion store" below).
7. Remove the task's row and the task's report channel `state/<id>.status`.
8. Keep `data/<id>/brief.md` for history (the agent can prune old briefs).

The report channel goes because it is the volatile wake log, not a deliverable: a task respawned under a used ID starts at `report_offset` 0, so a surviving log would be replayed as this run's - re-raising decisions already resolved, absorbing a genuine unexplained stop as one already seen, and auto-recording a PR URL out of the previous run's `done` line onto a task nobody recorded it for. The durable deliverables under `data/<id>/` survive teardown, as before; keeping a torn-down task's wake history would be its own feature with its own reason, not a side effect of cleanup.
A hold on the id is not removed either, deliberately - it is not task-scoped, so it outlives the row and keeps an unanswered question visible, which is also why `hand spawn` then refuses the id until `hand hold clear` (see "Holds" under "State management"). One kind is the exception: a `limit` hold is released, since nothing is left to resume and no watcher will ever clear it, so left behind it would refuse `hand spawn` on that id forever. Only that kind, and a failure to release it is a warning rather than an error - the delete has already happened and re-running teardown cannot undo it.

Behavior (scout task):
1. Check `data/<id>/report.md` exists (the report is the deliverable).
2. Close the herdr tab.
3. Return the worktree to treehouse.
4. Append a completion record to `state/completions.jsonl`.
5. Remove the task's row and `state/<id>.status`.

Behavior with `--force`:
- Skip steps 1-3 for ship tasks, skip step 1 for scout tasks.
- Still closes herdr tab and returns worktree.
- Stays the escape hatch for genuinely unlanded work only. Work that is delivered and not landed has its own state (`hand deliver`), so reaching for `--force` on it - and recording it as `torn-down`, indistinguishable from an abandoned task - is never the answer.

Teardown removes several resources in sequence, and any step can fault, so the command has to be runnable a second time: a resource already released is that step's goal already reached, not an error, and never something `--force` should be needed for.
A tab herdr no longer lists counts as closed.
A worktree already back in its pool counts as returned - that is treehouse's own answer, since `treehouse return` on an already-returned path is a no-op success, and it is not inferred from the path being gone: a returned worktree keeps its pool slot directory, so nothing can tell it from a leased one by looking.
The removals are ordered the same way - the report channel goes before the task's row, since the report removal is the one that can fail on a permissions or I/O fault and doing it first leaves the row, and with it the retry, intact.

#### Completion store

The completion record is appended before the task's row is removed, not after, because the record is derived from the task state that removal would take out from under it. That ordering has to hold under a fault on either side of it, and the two sides fail in different, deliberate directions:

- If the append itself fails, the command returns before the task's row is touched. Nothing was recorded, but the task is untouched too, so the whole command is simply retryable.
- If the state removal that follows it fails, the record already written is not thereby wrong, and it is already durable. Everything it claims (work landed, worktree returned) was independently true earlier in the same run regardless of what a later bookkeeping step does. The task's row is left in place, so the retry replays the whole command and appends a second, functionally duplicate record - a harmless duplicate traded for never silently losing a completion, on purpose.

`state/completions.jsonl` exists as a sibling to `state/events.log` rather than a share of it: `events.log`'s writer reads the whole file, appends, and rewrites it via a temp-file rename, which is fine for its single long-lived writer (`hand watch`) but loses a line outright if a second process's read-modify-write race lands its rename over the first's. Teardown is a short-lived process that can genuinely overlap a running `hand watch`, so the completion store instead takes a dedicated lock and performs one `O_APPEND` write per record - no read, no rename, nothing for a second writer to clobber.

The store is deliberately uncapped: it is the only durable record of a task's completion once `hand teardown` removes the row, and `hand status` only ever shows the live fleet, never history - so keeping just the last N entries would throw away the answer to "what happened to a task that's gone" with nothing left to reconstruct it from. Each line is a complete JSON object (`id`, `project`, `kind`, `outcome`, `detail`, `torndown_at`), readable without parsing markdown.

`outcome` is one of:
- `merged`: the work landed. `detail` names the PR, or `branch merged` for a local-only branch.
- `done`: a scout task's report is the deliverable. `detail` names the report path.
- `delivered`: the work is handed off and its landing was never the fleet's to decide. `detail` is the reason `hand deliver` recorded, prefixed with the PR when one is on the task. Ranked ahead of every outcome above, all of which assert the work landed - but only while the task's row carries no merge: a delivered task has to stay distinguishable from a merged one in the permanent record, or the fleet's history claims upstream merges that never happened (atqamz/secondhand#78). A delivery the upstream maintainer then actually merged (`merged` or `pr_merged_observed` on the row) records `merged` instead, because that is the stronger of the two facts and the requirement is only that the record never claim a merge that did not happen.
- `torn-down`: `--force` skipped the checks, so nothing about landing is claimed at all.

Output:
```
id: fix-login
result: torn-down
project: nsr
kind: ship
outcome: merged
detail: "https://github.com/org/repo/pull/42"
worktree: returned
help[1]:
  - This id is gone from `hand status`; its completion is the last word on it
```

`outcome` and `detail` are the completion record's own fields, so what teardown says and what the
permanent record holds cannot drift.

Errors:
- Task not found.
- No PR recorded and the project is not local-only (without `--force`, and unless the task is recorded as delivered, or its work is a completed scout deliverable - `data/<id>/report.md` on disk, a branch adding no commit to the local default branch, and no merge on the row).
- Uncommitted changes in worktree, unless every change is content-identical to the local default branch's tip in both the index and the working tree (without `--force`); the error carries a capped `git status --porcelain` of the worktree.
- PR not merged (without `--force`).
- Ambiguous PR head ref: the task's branch carries several PRs that do not resolve to a single usable winner - no preference tier holds exactly one match, or a merged PR coexists with an open one (without `--force`).
- Report not found for scout task (without `--force`).
- Treehouse return failed (worktree locked, path no pool manages), or an unforced return aborted on a dirty worktree and left the slot leased. The task's row is kept either way, since it is the only record of the leased slot.
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

Behavior (local merge, `--local`):
1. Read the task's row for worktree and project.
2. Refuse if worktree has uncommitted changes.
3. Determine the task branch from the worktree.
4. In the project clone: `git merge --ff-only <task-branch>`.
5. Refuse if fast-forward is not possible (diverged branches).
6. Update the task's row with merge status.

Output:
```
id: fix-login
result: merged
method: squash
pr: "https://github.com/org/repo/pull/42"
merged: "2026-07-29T11:04:00Z"
help[1]:
  - Run `hand teardown fix-login` to release this task's worktree and pane
```

Output (local):
```
id: fix-login
result: merged
method: local-fast-forward
branch: 42-fix-login
into: main
merged: "2026-07-29T11:04:00Z"
help[1]:
  - Run `hand teardown fix-login` to release this task's worktree and pane
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
3. If the task already has this exact PR recorded, skip steps 5-7 and report success without writing anything again (the URL is already on record, so there is nothing left to validate or write). This reconciling repeat is why `hand pr <id> <url>` is a sound remedy for a `pr-record-unknown` event: the lock holder that event leaves unnamed may have been recording this very URL, and a plain confirmation here lets the operator resolve the ambiguity instead of erroring on a URL that was already fine.
4. If the task already has a *different* PR recorded, refuse - one task, one PR; correcting a wrong record is a deliberate `hand teardown`/`hand spawn` decision, not something `hand pr` overwrites silently.
5. Resolve the task's project and derive `owner/repo` from the project clone's own `origin` remote (`git config --get remote.origin.url`, not `git remote get-url`, so a local `url.<base>.insteadOf` rewrite never turns a genuine mismatch into a false match).
6. Refuse if the URL's `owner/repo` matches neither the derived repo slug nor the project's declared `upstream` (see "Project registry format"). A fork contribution's PR lives on the upstream, not on the fork hand pushes to, so the upstream passes - but only because an operator declared it with `hand project upstream`, never because the URL's repo looks related to the project's own. The refusal names the declared upstream, or says none is declared, so an operator can tell "wrong upstream" from "no upstream declared". Both comparisons fold case: a GitHub slug is unique only up to casing, so folding admits no foreign repo, while comparing exactly refuses a genuine PR whose URL carries GitHub's canonical casing when the clone's `origin` remote or the declared upstream was written in another (atqamz/secondhand#146).
7. Confirm the PR exists via `gh pr view` (network check, 30s timeout) - shape validation in step 1 only proves the URL looks right, not that the PR is real. The refusal names the repo the *URL* belongs to, not the project's own: step 6 accepts a PR on the declared upstream too, so naming the project repo here would send an operator whose upstream PR number is wrong to check a repository the URL was never on.
8. Write `pr` into the task's row.

Steps 5-7 live in `project.ValidatePR` and are the *only* validation path: `hand watch`'s auto-record calls the same function, so a worker-supplied URL can never reach task state on weaker terms than an explicit `hand pr`.

Output:
```
id: fix-login
result: recorded
pr: "https://github.com/org/repo/pull/42"
help[1]:
  - Run `hand merge fix-login` once this PR's checks are green
```

Output (reconciling repeat) - same document, `result: already-recorded`, so a caller reads one field
to tell the two apart rather than two sentences.

Errors:
- Malformed PR URL (usage error, code `2`).
- Task not found.
- Task already has a different PR recorded.
- Project not registered.
- Cannot derive `owner/repo` from the project clone's origin remote.
- URL's repo matches neither the project's repo nor its declared upstream.
- PR not found via `gh pr view` (network error or nonexistent PR).

---

### `hand watch [flags]`

Blocking watcher. Polls herdr agent states and prints actionable events to stdout.
Also logs events to `state/events.log` for crash recovery.
One fleet home has at most one watcher at a time (see "One watcher per fleet home").

```
hand watch
hand watch --poll 10s
hand watch --until-event --timeout 30m
hand watch --until-event --event parked,failed
hand watch --takeover
```

Flags:
- `--poll <duration>`: poll interval when push events aren't available. Default: value from `config/watch-interval`, or `5s`.
- `--until-event`: block until the first events, print them, exit `0`. See "Delivering an event to a supervisory agent" below.
- `--timeout <duration>`: with `--until-event`, give up after this long and exit `4`. Default: no timeout. Without `--until-event` it is a usage error (exit `2`), since a streaming watcher has no completion to bound.
- `--event <kind>`: with `--until-event`, wake only on the given event kinds; repeatable or comma-separated. Default: any. Without `--until-event` it is a usage error (exit `2`), since the streaming path has no wake to filter - it prints every actionable event regardless. An unrecognized kind is also a usage error, naming the full known set. This is a stdout-only filter: `state/events.log` still receives every actionable event, filtered or not, since it is the fleet's durable record and a caller narrowing its own wake has no bearing on what happened. Kinds are internal identifiers, not the printed line's leading word - most agree (`stale`, `parked`, `blocked`, `failed`), but the report-derived ones don't: a `working <id>: <note>` line filters on `report-working`, a `reported-done`/`done <id>: <note>` line on `report-done`, and so on for every entry in "Report channel" below. A caller filtering on the classification a line came from, not its printed spelling, is why the filter is caller-expressible at all rather than hardcoded to one split: `working` is exactly what distinguishes a wedged spawn from a slow one, so no fixed actionable/progress grouping could serve every caller.
- `--takeover`: replace the watcher already attached to this fleet home instead of refusing, signaling it to stop first. A no-op when nothing is attached, so it is safe to pass unconditionally. See "One watcher per fleet home".

Behavior:
1. List all active tasks from the store.
2. Subscribe to herdr's `agent_status_changed` push events if available.
3. Fall back to polling herdr agent states at `--poll` interval.
4. Classify each state change:
   - `idle-unreported <id>`: agent stopped being busy after working/blocked - herdr reports this as `idle` or `done` interchangeably (see "Agent state" below; hand's polling model observes `done`, essentially always, never `idle`) - but its report channel (see "Report channel") doesn't explain the stop: no report at all, or the last line was still `working`. Any other terminal report (`paused`, `blocked`, `needs-decision`, `done`, `failed`) already explains the stop, so that transition is absorbed silently instead.
   - `blocked <id>: <reason>`: agent reports blocked (herdr-level; herdr gives no free-text reason, so `<reason>` is a fixed string).
   - `failed <id>`: herdr pane died unexpectedly. For a task already tracked, this fires the moment a probe that used to succeed starts failing - no dwell, since a pane that was just fine is itself the evidence. A task first sighted with its very first probe already failing (a re-scan picking up a fresh spawn between arm and its first successful probe) gets a dwell instead, measured against `config/stale-threshold`, before it fires: a pane can blink through a probe or two while herdr catches up to a just-spawned worker, and a blink must produce no event, only a pane that stays dark past the dwell earns one. Either path fires once per outage and stays quiet until the pane is next seen healthy.
   - `stale <id>`: agent hasn't changed state for longer than the stale threshold (default 300s, configurable via `config/stale-threshold`).
   - `parked <id>: <last-report-line> (silent <age>)`: the report channel itself has stopped growing for longer than its bound - independent of `stale`, which only ever watches herdr transitions and has nothing to fire on when herdr registers no transition at all (a pane can sit healthy and quiet forever without one). The bound is chosen by the last classified report line, and `done`/`failed` are bounded rather than exempt: the status file being torn down is what actually severs a task from steering, not the worker's own last word on the matter, so a done/failed worker still attached to a pane is silence like any other. Three tiers, three keys, rather than one tier reused for two of them: `paused` gets the long bound (`config/parked-paused-bound`, default 3600s) since naming what it's waiting on already explains the quiet; `done`/`failed` get their own bound (`config/parked-done-bound`, default 5400s), longer still, since a finished worker's pane lingering is expected and unhurried; everything else - `working`, `blocked`, `needs-decision`, or no report at all - gets the short one (`config/parked-other-bound`, default 1200s) since that silence is unexplained. Edge-triggered like every other trigger: fires once per silence episode and only refires once the report file actually grows past the mtime it fired for - a bound that holds across `hand watch` restarts, since the instant fired for is persisted as `parked_fired_for` rather than re-derived. A parked worker and a crashed one are indistinguishable from the status file alone - both are a report line that stopped moving - so the event carries only the last line and its age and leaves the process check itself to the caller (`hand status <id>`, or the session directly).
   - `usage-limit <id>: harness stopped on a usage limit; <attempts> made, next try <instant>`: the worker's harness stopped because its account ran out of quota, and a resume is now scheduled (see "Resuming a usage-limited worker" below). `<attempts>` counts the resume attempts spent on this limit so far, reading `no attempts` on the first line, then `1 attempt`, then `<n> attempts`. Also recorded as a hold of kind `limit` carrying that same text as its reason, so the wait is visible in `hand status` and `hand spawn` will not hand the id to new work while a worker still sits on it.
   - `usage-limit-resumed <id>: running again after <attempts>`: a task that was waiting on quota is running again, however that happened - a resume attempt that landed, an operator `hand send`, a human typing in the pane. The `limit` hold is released with it.
   - `usage-limit-stuck <id>: <the same reason text as usage-limit>`: the resume mechanism has spent six attempts on one limit and is out of its own answers. Fired once per limit, and attempts continue afterwards - a week-long limit is real and does eventually lift - but no longer quietly. This is the only one of the three that notifies (see "Notifying a supervisory agent with no session watching").
   - `pr-merged <id>`: a recorded PR has been merged (checked periodically via `gh pr view`). Announced once ever: the observation is recorded as `pr_merged_observed` on the task after the line is printed, so a restart neither repeats it nor loses it to a crash between the two. The same marker is set outside the watcher when gate-opened-PR detection records a PR that is already merged (see `hand status`), so a watcher that first sees the task after that stays quiet about a merge it never observed.
   - `pr-not-recorded <id>: <url> (<reason>)`: a PR URL a worker embedded in a report line was attempted and the recording did not complete. The token says only that much, for any cause - refused validation, an unregistered or unresolvable project, an unreadable task record, a failed state write - and `<reason>` is the underlying error, which is what says which. The whole cause is kept; only its line breaks are not, since `gh`'s multi-line stderr reaches these errors verbatim and an event is one line on stdout and one entry in `state/events.log` (continuation lines would parse back as separate events in both). The fix in every case is a human running `hand pr <id> <url>`: it either records the URL or fails with the real underlying reason. The kind is deliberately not split by cause, since an enumeration of causes is forgotten the next time a new one appears.
   - `pr-record-unknown <id>: <url> (<reason>)`: the same URL was never attempted, because another command held the task lock at that moment. Whether it ended up recorded is genuinely unknown - the holder may be the `hand pr` recording that very URL - so this event asserts nothing about the outcome and points at `hand status <id>` to confirm, except when the task's own state can't be read, where it names that read failure instead of a remedy that would hit it too. Nothing is announced at all when the lock holder is found to have already recorded that same URL.
   - Both auto-record events are durable on stdout and in `state/events.log` (plus a stderr diagnostic) rather than only a transient stderr line, since the report line is consumed either way.
   - `working <id>: <note>` / `paused <id>: <note>` / `report-blocked <id>: <note>` / `needs-decision <id>: <note>` / `report-failed <id>: <note>`: a new line landed on the task's report channel, classified per "Report channel" above.
   - `reported-done <id>: <note>` / `done <id>: <note>`: a `done` report line landed; printed as `reported-done` until cross-checked against the task kind's completion evidence (a merged PR for ship, `data/<id>/report.md` for scout), then once as `done` when that evidence lands - which is usually a later tick (see "Report channel").
   - `malformed report <id>: <line>`: a report line didn't match the fixed vocabulary. Surfaced, never dropped, so a typo in the worker's report doesn't silently vanish.
   - Benign events (working herdr transitions, routine transitions): absorbed silently.
5. Print one line to stdout per actionable event.
6. Append each actionable event to `state/events.log` (bounded: keep last 200 lines, rotate on overflow).
7. Re-scan `state/` periodically to pick up newly spawned or torn-down tasks.
8. Exit cleanly on SIGINT/SIGTERM.
9. While tailing a task's report channel, a line carrying exactly one PR URL auto-records it if the task doesn't already have one, subject to the same validation `hand pr` enforces; a URL whose recording was attempted and did not complete is surfaced as `pr-not-recorded`, and one the watcher never got to attempt as `pr-record-unknown` (see "Report channel").
10. Every task-state write the poll loop makes - the bookkeeping it owns (`report_offset`, `pr_merged_observed`) and an auto-recorded PR - takes the task lock non-blocking, and is skipped when another command holds it. The poll loop never waits on the **task** lock, because that lock is held across unbounded network and git work (`hand merge` across `gh pr checks`/`gh pr merge`, `hand promote` across a `git push`): waiting on it can stall every other task indefinitely, and `flock` cannot honor a SIGINT/SIGTERM in the meantime. Bookkeeping is re-derivable and simply retries next tick. A skipped auto-record is announced as `pr-record-unknown` - never as `pr-not-recorded`, which covers every attempt that was made and did not complete - except when the lock holder turns out to have recorded that same URL, which is silent.
11. Per-task bookkeeping is written back only after the tick's events are announced, never before. A marker persisted ahead of its line would, if the process died in between, suppress an announcement nothing can re-derive; a duplicate line is the cheaper failure.

#### One watcher per fleet home

`hand watch` acquires ownership of the fleet home before it polls anything, and refuses with exit `3` when another watcher already holds it, naming the incumbent's pid and `--takeover` as the remedy.
Two watchers on one home are not a redundant pair: each polls herdr independently, each classifies the same transition, and each fires `hand notify`, so the fleet's news arrives twice and the notify hook - whose whole purpose is to reach an operator with no session watching (see "Notifying a supervisory agent with no session watching") - becomes the loudest duplicate of all.
Ownership is validated at the point of acquisition, in the tool, because the way a second watcher actually gets started is a session that lost the memory of having started the first one; a convention written down anywhere is a convention that compaction can drop.

**Ownership is an `flock` on `state/watch.pid`, never the pid the file contains.**
The kernel releases an `flock` when its holder dies, however it died, so a crashed watcher leaves nothing stale behind to clear and no liveness heuristic can decide wrongly.
This is the whole reason the mechanism is safe to have at all: a lock that a crash can leave held would lock a fleet home out of watching itself, which is worse than having no lock.
The pid recorded inside the lock is advisory only - it lets a refusal name the incumbent and lets `--takeover` signal it - and a read that races the incumbent's own write degrades to `unknown` rather than to some other process's pid, since the value is only trusted when it arrives newline-terminated.

`--takeover` sends the incumbent SIGTERM, which `hand watch` already handles as a clean shutdown, and then waits up to 5s for the lock to come free.
If it does not, the takeover fails rather than proceeding: two watchers is the condition being prevented, so a takeover that cannot confirm the incumbent is gone must not become one.
Ownership is per fleet home and shared by both modes, so a streaming `hand watch` also blocks a `hand watch --until-event` arming against the same home - correctly, since the arming watcher would consume report lines out from under the streaming one - and `--takeover` is how a caller that wants the window says so.

#### Delivering an event to a supervisory agent

Detecting an event and delivering it are separate problems, and the streaming mode solves only the first.
`hand watch` never exits, and a supervisory agent's background-task runner re-invokes the agent when a process *exits*, so a streaming watcher's stdout is a file that is read only if the agent independently decides to look.
"Remember to check the watcher" is not a mechanism, and it is what failed on 2026-07-28.

`--until-event` makes the process exit the delivery:

1. Arm: connect to herdr and probe every active task's pane once, before anything else - both bounded by `--timeout` so a wedged herdr daemon cannot strand the wait past it before the poll loop even starts. Any task that *fails* its probe names it on stderr and exits `5` - never enters the baseline half-armed, since a task invisible to the very first probe has no herdr transition to ever fire on. Losing the race against `--timeout` instead is exit `4`, not `5`: the wait window is simply over, exactly as it is for a timeout in the poll loop, and no single task can be named as the cause the way `5` promises. A task first seen only later, after arming - a re-scan picking up a fresh spawn - has no such gate and is handled by the live poll's own first-sighting tracking; see the `failed` bullet above and "What survives a `hand watch` restart" below.
2. Take the baseline silently: two ticks with stdout discarded. The first seeds tracking for every task, the second consumes whatever a previous watcher left unconsumed on the report channels, since `report_offset` survives a restart on purpose and those lines are new to the poll loop. This second tick is also the first to classify against durable evidence, so a task already `stale` or already `parked` before this process ever started is absorbed here exactly as an already-`done` one is (see "The startup state is never an event" below).
3. Poll. On the first tick that produces any event, write that tick's events to stdout and exit `0`.
4. On `--timeout`, whether it elapses during arming or during the poll, write nothing to stdout, name the elapsed timeout on stderr, and exit `4`.
5. On SIGINT/SIGTERM, the same as a timeout: nothing was delivered, so exit `4`, never `0`.

Each rule below closes one way the `tee` + `grep -m1` wrapper this replaces failed:

- **The startup state is never an event.** Only a change from the baseline exits. A worker that was already `done` when the watcher armed produces nothing on stdout, where the wrapper's `grep` matched it, exited, and left the pipeline half-alive with nobody reading the two real events that followed.
- **Every wake trigger is edge-triggered, `idle-unreported`, `stale`, and `parked` included.** A worker fires one on entering the condition and does not fire again until it leaves and re-enters, so no signal has to be excluded from the trigger to avoid a wake storm - which is how the wrapper came to exclude the exact signal it was built for.
- **Arming itself can fail loudly, distinct from every other exit.** A worker whose pane answers with a failure at arm time is not a quiet fleet (`4`) and not a delivered event (`0`) - it is `5`, naming the worker, because the caller would otherwise wait out the full `--timeout` for a cause it can never see on stdout. An arm probe that instead runs past `--timeout` names no worker and is `4`.
- **The exit code says which happened**: `0` an event was delivered, `4` no event (timeout or signal, wherever the timeout lands), `5` a named task's pane failed its arm-time probe, `3` another watcher already owns this fleet home so the arming never happened (see "One watcher per fleet home"), `1` the watcher itself failed, `2` a usage error. A caller can never read a crash or a quiet window as fleet news.
- **No pipeline for the caller to get wrong.** The exit is the whole mechanism.

Worst-case delay from a real transition to the exit that delivers it is one `--poll` interval (`config/watch-interval`, default 5s) plus that tick's own bounded work - the only unbounded-looking piece, a `gh pr view` check for each task with a recorded, not-yet-confirmed-merged PR, is itself capped at 30s per task and run one task at a time, so a fleet with several such tasks can push a single tick past the poll interval alone, but never past that per-task cap times the count. Once the event is written to stdout, the process exits immediately; nothing after that adds delay.

Baseline events are withheld from stdout only.
They still reach `state/events.log`, because the report lines behind them are consumed either way and silently dropping a state change is worse than repeating one.
So the agent's loop is: arm the watcher, read `hand status` and `state/events.log` for current truth, then treat the next exit as the answer to "what changed since I armed".
Anything that lands between one exit and the next arming is in those same two places.

One invocation delivers one wake, and re-arming is the caller's own next step after acting on the exit, which it takes anyway with no human in it.

This covers the awake path only: an exit reaches a session that exists and re-arms, and has no reach when no session
is running. `hand notify` (see its own section) is the channel that reaches an unattended fleet, and every `hand
watch` invocation - `Run` and `RunUntilEvent` alike - now calls it in-process for every event that matches
`NotifyFilter` (see below), whether or not that event happened to also be printed to stdout, so a transition
discovered on a restart's own baseline tick still reaches the operator the same way a live one would.

#### Notifying a supervisory agent with no session watching

The notify hook is its own filtered consumer of the same classified event stream `--event` already filters for
stdout, not a severity test hardcoded into `handleEvent`: `internal/watcher.NotifyFilter` builds an `EventFilter` -
the identical `EventFilter`/`Matches` mechanism `--event` uses - with its own fixed membership, and `handleEvent`
checks it the same way it checks `cfg.EventFilter`. Its membership names the event kinds worth waking someone for
with no session watching: `blocked`, `report-blocked`, `failed`, `report-failed`, `report-needs-decision`,
`report-done`, and `usage-limit-stuck`. `report-blocked` - the worker's own `blocked: <reason>` report line, per "Report channel" - is in the
set alongside the herdr-transition `blocked` because the two are independent signals, and a worker that reports
blocked and then goes idle fires no other notifiable kind: `ClassifyStatus` suppresses `idle-unreported` precisely
because `LastReportState` is set.
`idle-unreported`, `stale`, `parked`, `pr-merged` and the `pr-record-*` kinds are left out - each describes a
transition the poll loop is already tracking toward one of the six above, or one that resolves on its own without a
human, so notifying on it too would either double up the same fact or wake someone for nothing actionable.
`usage-limit` and `usage-limit-resumed` are left out for that second reason exactly: a limit that the mechanism is
already scheduled to resume from, and a worker that is running again, are both bookkeeping a human has no part in.
`usage-limit-stuck` is in the set because it is the one of the three that says the mechanism has run out of its own
answers (see "Resuming a usage-limited worker").

`handleEvent` calls `internal/notify.Send` directly for every event `NotifyFilter` matches, never by shelling out to
the `hand notify` subcommand, so wiring reaches every caller of `hand watch` with no shell wrapper required. An unconfigured
`config/notify` is expected on most fleets - the same silent fallback every other `config/` default gets - so it
produces no diagnostic; a *configured* template that fails is written to the watcher's own stderr, the same channel
every other watcher-internal failure (a stuck lock, an unreadable report) already uses, per "Error output".
That rule is the whole consequence of a failed send: a diagnostic is written and the poll loop carries on, so the send
never ends the run nor suppresses the event's own stdout line.
A template that *hangs* is bounded the same way, by a timeout inside `internal/notify.Send` that surfaces as one more
such diagnostic - the send runs inline in the poll loop, so an unbounded one would wedge polling, `--until-event`'s
`--timeout` and shutdown alike.

One notifiable kind, `failed` fired from an already-unreachable pane (`ClassifyUnreachable`), can re-fire once per
`hand watch` restart for a condition that was already true before the restart, because its firing latch
(`UnreachableFired`) is deliberately non-persisted (see "What survives a `hand watch` restart" below). This is the
same "duplicate over silence" tradeoff the poll loop already accepts for that latch - a caller re-arming
`--until-event` gets a repeat notification for a condition that has not changed, rather than one that silently stops
notifying because the process restarted - so no additional rate limiting is added on top of it. `parked` does not
make the same tradeoff: its latch is persisted as `parked_fired_for`, because a done or failed task's report file
never grows again and the duplicates evicted real history from the capped `state/events.log`
(atqamz/secondhand#127). It is not a notifiable kind either way.

#### Resuming a usage-limited worker

A worker whose harness runs out of quota stops mid-task with the reason on screen and nothing else (atqamz/secondhand#136).
The stop looks like any other stop to the rest of the poll loop, so without this the task sits dead until a human notices.
Detection, a recorded state, and a resume trigger are one mechanism; any one of them alone leaves the task exactly as dead with a better label on it.

**Detection is a harness capability, not another condition on the poll loop.** `internal/harness` owns a catalogue of usage-limit signatures - which wordings mean "out of quota", and how to read a reset instant out of them - and exposes `SupportsUsageLimit` and `DetectUsageLimit`. Only `claude` is in it. Every other harness declines: one map lookup, no pane read, no steer, and no way for a bare shell pane to be typed into. Teaching `hand` about a second harness is an entry in that catalogue, not a branch in the watcher. This is the shape atqamz/secondhand#81, #84 and #85 got wrong by growing the poll loop one conditional at a time, and #128 was the bill for it.

**The state is a durable pair of task columns, with the `limit` hold as its operator-visible projection.** `usage_limit_retry_at` and `usage_limit_attempts` sit alongside `report_offset` and `parked_fired_for` as watcher bookkeeping: a non-empty retry stamp *is* what makes a task limited. The hold (kind `limit`, see "Holds" under "State management") is what makes the wait visible in `hand status` and keeps `hand spawn` from handing the id to new work while a worker still sits on it. Neither substitutes for the other - a hold carries no schedule, and a column is not rendered anywhere an operator looks - and the schedule is the authority: the retry path reads the columns, never the hold.

**"The limit lifted" is an observation, never a deadline that elapsed.** No attempt waits out a timestamp and then declares the quota back. An attempt is the same two-call steer `hand send` performs - a plain instruction into the composer, then Enter - and what it produces is either a pane that starts working (the next tick's clear check sees that and fires `usage-limit-resumed`) or a fresh refusal on screen, which is the observation the *next* attempt is scheduled from. A reset instant parsed out of the refusal only ever decides when to start trying, and it is read from the freshest refusal on screen: an older one still in scrollback names a reset that has already come and gone.

Being the same steer, an attempt holds the same `send:<id>` lock `hand send` holds, so the two never write one composer at once (see `hand send`).
It takes that lock without waiting, since a poll tick cannot block behind an operator's whole `--wait`; a busy lock spends no attempt, leaving the schedule due for the next tick.
An operator send landing in that window is in any case the likelier thing to end the limit.

**The failure mode designed against is a retry storm against an account that is still limited**, and five things bound it:

- The first attempt waits until the instant the harness itself named, plus a minute of skew, or ten minutes when it named none. Ten minutes is also the floor: no attempt is ever sooner.
- Later attempts back off by doubling, capped at hourly.
- Every wait is capped at 24 hours, so a misparsed or absurd prediction cannot strand the worker.
- Exactly one attempt per due window, and the schedule is durable, so a watcher restart cannot reset the clock into an immediate retry - which is the one thing that would turn a restart loop into a storm. A steer that fails keeps its schedule too: rolling the stamp back would leave the task due on every tick.
- A genuinely long limit is therefore not probed hourly for a week: each attempt re-reads the harness's own fresh refusal and reschedules from the reset it names, so the pattern settles at roughly one attempt a day. After six attempts on one limit, `usage-limit-stuck` says so once on the notify channel; attempts continue, but no longer quietly.

**A pane is read only on an edge, never per tick.** The edges are the transition into a not-busy status, and the first sighting of an already-stopped worker - which is the case no transition can cover, since the stop that stranded the worker happened before this watcher existed. A worker whose report channel already says `done` or `failed` is left alone entirely: it is not waiting on quota, and steering it would restart work that is over. Recognition matches on the quota being *reached*, so the harness's own "approaching your limit" warning is not a stop.

The limit is released the moment the pane is observed working or blocked again, whatever caused it - a landed resume attempt, an operator `hand send`, a human typing in the pane - and `hand hold clear` stays available as the operator's way out of a hold `hand` set on their behalf.

#### What survives a `hand watch` restart

**Anything the watcher announces is persisted at the moment it announces it, never re-derived on restart.** Re-deriving is how an announcement gets silently skipped: evidence that lands while the watcher is down makes the restarted process conclude the line already went out. Every fact the poll loop carries across a restart, and which side of that rule it is on:

| Fact | Treatment |
|---|---|
| How far the report file is consumed | Persisted as `report_offset` and `report_digest` together, after the tick's events are announced. The digest fingerprints the bytes the offset covers, and the pair is what the reader trusts or discards as one value - an offset alone cannot say whether it still describes the file it was taken from (see "Report channel"). |
| A merge this watcher's own `gh` poll saw | Persisted as `pr_merged_observed`, after `pr-merged <id>` is printed. |
| The verified `done` announcement | Persisted as `done_verified`, after `done <id>` is printed. `hand merge`'s evidence can land while the watcher is down. |
| An auto-recorded PR URL | Persisted as `pr` on the task, and every outcome that isn't a silently self-resolving race is announced (`pr-not-recorded` / `pr-record-unknown`) and logged. |
| Last reported state and note | Persisted as `last_report_state` and `last_report_note`, written alongside `report_offset` on the same tick that consumed the line. Re-reading them from `state/<id>.status` on resume instead - the previous behavior - meant re-reading history the durable offset says has already been consumed, and re-derived them from a file `hand promote` deliberately leaves alone, so a promoted ship inherited the scout's last report as if it were its own. They explain a quiet pane (`parked`'s bound is selected by the state, and `hand status` renders the note) and gate the scout's deferred-`done` bookkeeping. |
| The identity of the task being tracked | Re-read as `created_at` and compared every tick: an ID torn down and respawned is a different task, so it is re-seeded from its own state rather than inheriting the previous run's. Inheriting it would suppress the new task's verified `done` forever, since the bookkeeping write-back stamps that inherited `done_verified` onto the fresh row, and would absorb its first unexplained stop. Same hazard as a surviving report channel, one layer in (see `hand teardown`). |
| The same bookkeeping across `hand promote` | Not covered by the identity check above, and the sharper problem: promote rewrites the task row in place, keeps `created_at`, and gives the task a *new herdr pane*. Every cached fact anchored to the old pane is therefore not evidence about the ship at all, however plausible it looks. See "Pane-anchored facts across `hand promote`" below for the field-by-field classification and for how the watcher forgets its cached copies. |
| Current herdr agent status, and the blocked flag derived from it | Re-derived, safely: a live pane property with no durable answer, seeded on first sight without emitting (transitions, not states, are events). A transition that happened while the watcher was down is not announced, but is not lost either: `hand status` shows a quiet pane through its `reported` and `flags` columns, read off the same report channel, and the stale timer below re-flags the task within one window. |
| Whether the last probe of the pane succeeded | Re-derived, safely: seeded from the probe that does the sighting, so it is true only once a probe has actually succeeded, and false whenever none has yet - a fresh spawn, a live `hand promote` (see below), or a resume whose own first probe fails. It gates the once-only `failed` latch and `stale`'s detection. False is the honest seed for both, because there is no true-to-false edge to announce when nothing was ever reached: a pane still unreachable at the sighting is announced by the dwell in the row below rather than on sight, so a blink at exactly the wrong moment produces no event at all. |
| How long the current herdr status has been dwelt in | Persisted as `status_changed_at`, updated whenever a herdr status transition is actually observed - any transition, not only ones that raise an event - and re-seeded from `created_at` until the first one. This is what `stale`'s dwell is measured against. Seeding it from the resume time instead - the previous behavior - erased a real dwell on every restart, and since `--until-event` restarts on every delivered event by design, a fleet busy enough to re-arm faster than the threshold elapses could erase that dwell before it ever completed once, silencing `stale` for exactly the fleet it exists to watch (issue #75's Ruling 1). |
| Which status that dwell clock describes | Persisted alongside it as `status_changed_for`, and the timestamp is trusted only while the two agree. A timestamp on its own cannot prove the dwell it describes is still running: a status observed in a different pane is a new dwell even when it spells the same word, so a mismatch means the transition into the observed status happened at an unknown point since and the dwell can only honestly start now. Without this a restart after a `hand promote` read the restamped `status_changed_at` as the ship's own dwell in whatever status the ship happened to be in. |
| The stale timer's fired latch, as opposed to its dwell above | Re-derived, safely: cleared on every observed transition and reset to unfired on resume, so the worst a restart causes is one duplicate `stale <id>` after a further full threshold past a dwell that already fired once - it never suppresses a genuine re-announcement. The dwell it is measured against is not re-derived; see the row above. A restart is the safe direction, but a `hand promote` in a *live* watcher is not, and the latch is cleared there explicitly; see below. |
| Which silence episode `parked` already fired for | Persisted as `parked_fired_for`, written after the `parked` line is announced. It is the one fired latch that is *not* re-derived, because what makes re-deriving the `stale` and outage latches safe does not hold here: those dwell clocks keep moving, so a restart costs one duplicate and then the condition has to genuinely re-mature. A done or failed task's report file never grows again, so its silence instant is frozen and every restart re-fires against that same instant - and `state/events.log` is capped at 200 lines, so the duplicates evict real history rather than merely repeating themselves (atqamz/secondhand#127). Stored to nanosecond precision, because the value is a report file's mtime compared for exact equality and whole seconds would round it into an instant no later mtime ever matches. An unparseable stamp resumes unfired: one duplicate is the failure direction this whole table prefers. What the bound is measured against - the report file's own mtime, floored at `pane_started_at` - is untouched by a restart because neither one is process state to begin with; see "Delivering an event to a supervisory agent". |
| That a task is waiting on quota, and when it may next be poked | Persisted as `usage_limit_retry_at` and `usage_limit_attempts`, written after `usage-limit <id>` is announced (see "Resuming a usage-limited worker"). Not re-derived, and for a sharper reason than `parked_fired_for`: a re-derived schedule would let every watcher restart attempt a resume immediately, against an account the last attempt just found still limited, which is the retry storm the bounds exist to prevent. The attempt count is durable with it, since it is what the backoff and the stuck bound are both measured in. An unparseable stamp resumes *unlimited* - the one place this table prefers silence to a duplicate, because the duplicate here is a steer sent into a live pane rather than a line on stdout. |
| That this watcher has already looked for a limit on this task | Re-derived, deliberately: `LimitProbed` starts false on resume, which is what makes a watcher coming up against an already-stranded worker read its pane once and find the limit. There is no transition left to detect on in that case, so the first sighting has to do it. It costs one pane read per task per watcher lifetime for tasks that are stopped and unexplained. |
| The first-sighting outage's fired latch, as opposed to its dwell | Re-derived, safely, the same way the stale latch above is: `UnreachableFired` is not persisted and starts unfired on resume. The dwell it guards is not re-derived from scratch, though - a task first sighted unreachable is seeded through the same `status_changed_at`/`status_changed_for` path as any other status, with `herdr.StatusUnknown` standing in for "no real status yet", so a restart mid-outage resumes the outage's true start rather than a fresh clock, and the worst a restart causes is one duplicate `failed <id>` past a dwell that already fired once. |

Anything added to `TaskState` belongs in this table before it ships.

#### Pane-anchored facts across `hand promote`

`hand promote` keeps the task's `id` and `created_at` but hands it a **new herdr pane**.
Every cached fact anchored to the old pane is invalidated at that moment, so the governing question for each one is not "is it durable" but "was it anchored to the pane".
Both halves have to be dealt with, because neither is sufficient alone: promote clears the durable fields itself rather than leaving it to `hand watch`, since a watcher may not be running at all; and a watcher that *is* running holds an in-memory `TaskState` that passes the `created_at` identity check untouched and would write its cached copy straight back onto the freshly-rewritten row on the very next tick.
`forgetPaneScopedCache` is the single place that drops those cached copies.

Pane-anchored, and reset:

| Fact | Why it is not evidence about the ship |
|---|---|
| `done_verified` | The marker belongs to the scout's own verified `done`. The ship has not earned one, and carrying it would leave the ship run unable to ever announce its own, since the write-back only ORs the marker to true. |
| `status_changed_at` / `status_changed_for` | The scout's last observed transition happened in a pane the task no longer has. Carrying it would hand the ship a dwell already grown past `stale`'s threshold before its worker had run for a second. Promote restamps the timestamp to the promotion time and clears the status it was stamped for, which is what makes the ship's first observed status a fresh dwell rather than a resumed one. |
| `pane_started_at` | The whole point of the field: the ship's pane started at the promotion, not when the scout's did. Promote restamps it to the same instant it restamps `status_changed_at`, but it is a separate fact - nothing that merely observes the pane ever writes it, which is what keeps `parked`'s silence floor free of the outage restamps `status_changed_at` carries. |
| `last_report_state` / `last_report_note` | The scout's last report describes work in that pane. It selects `parked`'s bound and feeds the scout's deferred-`done` bookkeeping, so an inherited one both mis-bounds the ship's silence and can hand it a `done` it never reported. |
| `delivered_at` / `delivered_reason` | The delivery described the scout's deliverable, a report, and not a line of the ship's code. Carried, it would let `hand teardown` accept the ship task as terminal with no PR recorded and no merge check run at all - the landed-work guard bypassed without `--force` for work nobody delivered, which is the one meaning `--force` keeps. |
| The `stale` and `blocked` fired latches | Each is what makes its announcement fire only once. A latch surviving the promote silences that announcement for the ship's own pane - the `stale` one until the ship transitions at least once, which a genuinely stuck ship never does. |
| Whether the last probe of the pane succeeded | It gates the once-only `failed` latch and gates `stale` detection off entirely until some probe succeeds, and either value inherited from the scout's pane is a claim about a pane the task no longer has. It is reset to false, matching the seed a fresh spawn gets before its own first probe has succeeded: the ship's first probe of its new pane is a first sighting, so an unreachable one is announced through that sighting's dwell rather than firing `failed` on sight. Resetting it to true instead - the previous behavior - fired a no-dwell `failed` off a single blink, on the strength of a probe that only ever described the scout's pane. |
| The first-sighting outage's fired latch | A latch claiming the scout's outage has nothing to say about the ship's new pane. Left true it would sit inert until the next probe failure reset it anyway, but a fresh pane deserves a fresh episode on purpose, not by accident of that ordering, so it is reset to unfired alongside the probe-succeeded flag above. |
| The usage-limit schedule (`usage_limit_retry_at`, `usage_limit_attempts`) and the `limit` hold | A usage limit belongs to the harness process that hit it, and the ship's pane runs a new one against whatever quota exists now. Carried, the schedule would steer the fresh pane on a clock the scout's refusal set, and the stale hold would keep saying the ship is out of quota. Promote clears both columns with the other pane-scoped fields and releases a `limit` hold on the id - only that kind, so an operator's own hold on the same id is left standing (see "Holds"). |
| The cached herdr status the next probe is diffed against | The status a transition is measured *from*, so an inherited one invents or erases transitions in both directions: a scout cached as `working` turns the ship's first not-busy probe into `idle-unreported` for a pane never observed working, and a scout cached as `blocked` makes the ship's own `blocked` probe compare equal and never fire at all. It is reset to herdr's `unknown`, which matches neither branch, so the ship's first probe is the baseline a first sighting always is. This is not self-correcting in the same-status case, as was once assumed: equality is exactly what suppresses the announcement. |

Genuinely pane-independent, and carried:

| Fact | Why it survives |
|---|---|
| `report_offset` and `report_digest`, and the report channel they index | Promote never touches `state/<id>.status`: the report stream is continuous across the promotion and the offset already points exactly where the ship's first line lands, with the digest still describing the bytes behind it. Resetting them would replay the scout's consumed lines, the hazard the durable offset exists to prevent. |
| `pr`, `merged`, `pr_merged_observed` | Facts about the branch and its PR, not about any pane. |
| `created_at` | The task's identity, which promote deliberately preserves - this is one task's lifecycle, not two. |
| The `parked` fired latch (`parked_fired_for`) | Keyed to the report mtime it fired for, not to a pane, and the report channel is itself carried. Promote restamps `pane_started_at`, so the ship's evidence time is floored at the promotion instant - strictly later than anything the scout's silence could have fired against - and the latch cannot suppress the ship's own episode. |
| The report mtime `parked` measures silence from | Carried with the report channel, but *floored* at the pane-start instant `pane_started_at` records (a row written before that column existed is backfilled to its last observed `status_changed_at`, or `created_at` when no status was ever observed - the value the floor already computed for it, so the migration neither slides a live task's floor nor hands a task promoted before it the scout's creation instant). Promote leaves the scout's last append - and so its mtime - untouched while clearing the `last_report_state` that had the scout's silence bounded by the long done/failed tier, so an unfloored mtime would hand a pane seconds old the scout's whole accumulated silence, now measured against the short bound, and fire `parked` on it immediately. |

**The floor reads `pane_started_at`, never `status_changed_at`.**
The two answer different questions and cannot share a field.
A watcher tracking an unreachable pane persists `status_changed_for = unknown` with `status_changed_at` restamped to the outage-detection time, so a floor reading that field let an outage - or a blink that raises no event at all - slide it forward and forget up to a full bound of real report silence.
Reading `created_at` instead is not the remedy either: after a promote `created_at` is the *scout's* creation, which reopens exactly the hazard the floor exists to close.
Only a fact written by spawn and by `hand promote`, and by nothing that merely observes the pane, answers "when did this pane start" without also trying to answer "when was the last herdr transition seen" (atqamz/secondhand#128).

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
- Another watcher already attached to this fleet home: names the incumbent's pid on stderr and exits `3` before any polling, whether it refused outright or a `--takeover` could not confirm the incumbent was gone (see "One watcher per fleet home").
- Herdr not running (fatal: exit `1`, the reachability probe answering with a failure). Under `--until-event` that probe is additionally raced against `--timeout` so a wedged daemon can't strand the wait, and *losing that race* is exit `4`, not `1` or `5`: the window closed with nothing delivered, which is what `4` means wherever in the process it happens, and stderr names herdr as what it was still waiting on. A signal during the same probe is `4` for the same reason.
- Individual task probe failure (graceful: report as "unknown" state, and entered into tracking with a dwell clock running rather than left out of it - see the `failed` bullet above).
- `--until-event` reaching its `--timeout`, or being signaled, without delivering an event: a line on stderr and exit `4`, never a silent exit `0`. This covers the timeout elapsing anywhere in arming - the herdr reachability probe as well as the per-task probe sweep - as well as during the poll: the window is over either way, and no one task is at fault.
- `--until-event` failing to arm because a task's herdr pane answers its probe with a failure: names the task on stderr and exits `5`, distinct from both `4` (no event: either arming succeeded and nothing happened, or the window closed mid-arm) and `0` (arming succeeded and something did). Unlike the streaming path's graceful "unknown" above, `--until-event` cannot tolerate an unprobeable task at all: a task invisible to the arm-time probe would never enter `states` and so could never produce the transition the caller is blocking on, silently degrading the wait into a guaranteed timeout. A task first sighted after arming is not this case at all - it enters `states` through the live poll's own first-sighting tracking, dwell and all; see the `failed` bullet above.

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
   - List `treehouse.toml` in the clone's `info/exclude` if it is not already, which repairs a clone
     registered before `hand project add` started excluding it: without that, its untracked pool
     config reads as dirt and every sync from then on skips the project.
   - If on default branch and clean: fast-forward to `origin/<default>`.
   - If dirty, on non-default branch, or diverged: skip with warning.
2. Prune local branches whose remote tracking branch is gone.

Output:
```
count: 3
advanced: 1
failed: 0
projects[3]{name,result,detail}:
  nsr,fast-forwarded,"origin/develop, was 3 behind"
  yes2infra,skipped,dirty working tree
  no-mistakes,up-to-date,none
```

`advanced` counts the clones that actually moved, so a caller learns whether the sync changed
anything without reading every row. A project whose sync errored outright carries no row: with one
named project that error is the command's exit, and across the whole registry it is a stderr warning
the run continues past, counted in `failed` and named by a `help[]` line so the row count is never
read as the whole story.

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
- `--skip-gate-check`: dispatch into a `no-mistakes` project even if its gate is not initialized, its
  clone path is missing from disk, or that path is not a git repository (see "Gate preflight").

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
7. Rewrite the task's row in place: `kind` changes from `scout` to `ship`, and `harness`, `model`, `effort`, `worktree`, `lease_id` and the `herdr` coordinates describe the new worker. Every field anchored to the scout's pane is reset: `done_verified` to false, `pane_started_at` and `status_changed_at` restamped to the promotion time with `status_changed_for` cleared, `usage_limit_retry_at` / `usage_limit_attempts` cleared, and `last_report_state` / `last_report_note` and `delivered_at` / `delivered_reason` emptied. A `limit` hold on the id is released alongside those columns (only that kind, and a failure to is a warning - the promotion itself has landed). Every pane-independent field is carried, including `created_at` and the watcher's `report_offset` / `report_digest` - see "Pane-anchored facts across `hand promote`", which classifies each of them and covers the matching in-memory cache a live `hand watch` has to drop.
8. Only now tear down the scout's herdr tab and return its worktree; a failure here is a warning, not an error.

The scout side is torn down last on purpose: the same rollback contract as `hand spawn` applies up
to step 7, so a promotion that fails partway still leaves the scout's pane and worktree intact
instead of stranding the task with nothing to look at.

Output:
```
id: investigate-crash
result: promoted
kind: ship
was: scout
project: nsr
harness: claude
worktree: /home/user/.treehouse/nsr-abc/2/nsr
help[2]:
  - The scout's worktree and pane are gone; run `hand status investigate-crash` to read the ship worker
  - The scout's delivery no longer counts for this task, so `hand deliver investigate-crash` runs again on the code
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
The same `internal/notify.Send` this command calls is also the watcher's in-process notify hook - see "Delivering an
event to a supervisory agent" for that half.

```
hand notify "fix-login PR is ready for review"
```

Behavior:
1. Read `config/notify`. If absent, or empty once trimmed, nothing is delivered - see "Errors" below.
2. The notify config contains a shell command template. The message is available as the `$HAND_MESSAGE` environment variable.
3. Execute the command with `HAND_MESSAGE` set in the environment, under a 10s timeout: a template that hangs must not
   hang its caller, which for the watcher's hook is the poll loop itself.
4. Print `notified: <message>` to stdout only once the command above has actually succeeded. A template that
   backgrounds its work (`... &`) counts as succeeded once its own process exits `0`: the send is not held open for a
   grandchild that outlives it, and its eventual outcome is not observable here either way.

Example `config/notify`:
```
curl -s -X POST "https://api.telegram.org/bot$TELEGRAM_TOKEN/sendMessage" -d "chat_id=$TELEGRAM_CHAT&text=$HAND_MESSAGE"
```

Or for macOS:
```
osascript -e "display notification \"$HAND_MESSAGE\" with title \"secondhand\""
```

`hand init --setup` still does not write `config/notify` - it covers `harness`, `model`, and `effort` only - so a fresh
fleet home leaves the channel unconfigured until an operator adds the file by hand. That absence is expected and quiet
in the watcher's hook (see "Notifying a supervisory agent with no session watching"); `hand notify` itself is the one
place it is loud, per the exit code below.

Output:
```
result: notified
message: fix-login PR is ready for review
```

Errors:
- `config/notify` absent or empty, or its command failed or timed out - all exit `1`. Earlier, an absent config printed
  the `notified:` line and exited `0` regardless, so "not configured" and "delivered" were the same observable outcome:
  the one path meant to reach an operator with no session watching could report a delivery it never made. An empty file
  is the same case wearing a different shape - `touch config/notify` or a truncated one would otherwise run `sh -c ""`,
  succeed, and claim a delivery just as wrongly - so it is treated as unconfigured, not as a template. All of these mean
  the same thing to a caller - nothing reached the channel - so all are the same general error rather than a warning
  behind exit `0`.

---

### `hand search <query> [flags]`

Full-text search the prose corpus under `data/`.

```
hand search login auth decision
hand search --json "no-mistakes gate"
hand search --rebuild deploy failure
```

Flags:
- `--fields <a,b,c>`: which columns the `hits` block emits, in the order given. Any of `path`, `title`, `snippet`; all three by default. An unknown name is a usage error (`2`) naming the known ones.
- `--json`: output as JSON, one object per hit with `Path`, `Title` and `Snippet`. An empty result is `[]`, never `null`, so a caller can iterate without special-casing it. Rejects `--fields` as a usage error (`2`).
- `--rebuild`: discard and re-derive the index before searching. The recovery for an index that is present but wrong; an index that is simply *missing* needs no flag.
- `--limit <n>`: maximum hits, default 20.

Output:
```
query: gate preflight
count: 2
hits[2]{path,title,snippet}:
  data/task-12/brief.md,Rework the gate preflight,... the gate preflight runs before ...
  data/learnings.md,Learnings,... a gate preflight that cannot reach ...
help[1]:
  - Read a hit's path for the whole document; the snippet is a window, not the match in full
```

Behavior:
1. Scan `data/` for markdown files, comparing each against the index by mtime and size, and index what changed. Refreshing on every query rather than on a schedule keeps every other command free of the index entirely: the index is derived, so a stale answer is always settled by the corpus, and nothing else in `hand` has to know the index exists.
2. Match against the FTS5 index, ranked by bm25, and emit `path`, `title` and a snippet per hit.
3. With no hits, stdout still carries the query, `count: 0` and the schema header, plus `help[]` lines naming the two things that produce an empty answer: a query too narrow to match, and a corpus the index never caught up with (`--rebuild`). Silence is what a search that never ran also produces, so the zero is stated rather than implied.
4. A result that came in exactly at `--limit` says so in `help[]`, naming the doubled limit that would widen it: the capped rows and a corpus that genuinely holds no more are identical from the outside.

Snippets are not truncated by `hand`: FTS5's own snippet window already bounds them to a fixed token
count, so the `--full` recovery shape `hand status` carries has nothing to recover here (see "Output
shape").

Every whitespace-separated token in the query is quoted before it reaches FTS5, so a query a supervisor would actually type - `no-mistakes gate`, `atqamz/secondhand#53` - is matched as the literal text it looks like rather than parsed as query operators.

The index lives in its own database at `state/index.db`, separate from machine state, and is safe to delete at any time (see "Machine state and the prose corpus"). Neither the search nor the rebuild reads `state/hand.db`.

`data/dashboard.md` stays excluded from the corpus even though nothing writes it any more (atqamz/secondhand#62): a home initialized before the dashboard was deleted keeps its last render on disk indefinitely, and no command refreshes it, so indexing it would answer a prose search out of a frozen snapshot of removed functionality.

Errors:
- Corpus unreadable (the rebuild names the file it could not read).
- Index unusable, past what a refresh repairs - the condition `--rebuild` exists for.

Both are general errors (`1`), never usage errors: the operator typed the command correctly and the fault is in the world.

---

### `hand doctor`

Report-only check of the resolved fleet home's `AGENTS.md` for perishable content and generated-block drift. Fixes nothing; a human or agent reads the findings and edits the file. Findings are not all equal weight - see "Behavior" below for the one that is informational rather than exit-failing.

```
hand doctor
```

Behavior:
1. Resolve the fleet home (same resolution as every other command; a `hand doctor` outside one is the same precondition failure as elsewhere).
2. Scan `AGENTS.md` line by line, tracking fenced code blocks and the `hand:generated` span (see "AGENTS.md (target)"), and flag:
   - a date (`YYYY-MM-DD`) outside the generated span, since a date only stays true as long as the day it names,
   - self-expiring phrasing outside the generated span - `until #N lands`, `once #N lands`, `awaiting #N` - the same shape of problem as a bare date. Each shape needs an issue to expire against, so a bare "awaiting" with nothing to anchor it is durable prose and is not flagged,
   - an em dash or emoji anywhere in the file, generated span included,
   - a code fence that is never closed, since it silences the date and self-expiring checks for every line after it,
   - the generated span's content having drifted from `internal/agentsmd`'s `generatedBody`, a violation,
   - the `hand:generated` markers being absent altogether - the same `generatedBlockSpan` result `agentsmd.mergeGenerated` uses to decide whether to touch the file at all, so this finding states exactly the fact a refresh already acts on: nothing in `hand` will ever update this file's template. It is informational rather than a violation (see `agentsmd.Severity`), since a marker-less file can be an accident or a deliberate choice and nothing in the file tells the two apart.
3. Emit one row per hit under a `file` field carrying the resolved fleet home's absolute path to `AGENTS.md` (`generatedBody`'s absolute-path rule applies to the checker's own output too - a bare `AGENTS.md:12:` is ambiguous once more than one fleet home is in scope), and exit `1` if any violation-severity finding was found, `0` if the file is clean or every finding present is informational.

Output:
```
file: /home/you/fleet/AGENTS.md
count: 2
violations: 1
findings[2]{line,severity,finding}:
  12,violation,"a date (2026-07-29) outside the generated block goes stale"
  none,info,no hand:generated markers - nothing in hand will ever refresh this file's template
help[2]:
  - Edit AGENTS.md to resolve each finding; hand doctor reports and never rewrites
  - Run `hand update` if the finding is generated-block drift, since that block is refreshed rather than hand-edited
```

`count` is every finding and `violations` only the exit-failing ones, so a reader learns which
verdict they got without classifying rows themselves. A whole-file finding has no line to anchor to
and reads `none` there rather than `0`, which would read as line one.

Flags:
- `--fields <a,b,c>`: which columns the `findings` block emits, in the order given. Any of `line`, `severity`, `finding`; all three by default.

A clean file is `count: 0`, `violations: 0` and the schema header, with no `help[]`: there is nothing
to act on, and silence would be indistinguishable from a checker that never ran. A run whose findings
are all informational passed, and says so in `help[]` rather than leaving a reader to infer it from
`violations: 0`.

A remedy naming a command that takes a path spells that path out: `hand init` with no argument targets the working directory, which is a new nested fleet home whenever the operator ran `hand doctor` from anywhere but the home itself.

The missing-markers finding stays informational rather than becoming a violation because `hand doctor` cannot resolve it into a pass/fail verdict on its own: a maintainer who has deliberately kept a file marker-less - this repo's own checkout being the example, see "AGENTS.md (target)" - sees that finding on every `hand doctor` run in that home, permanently, but the command still exits `0` there, so a red `hand doctor` keeps meaning something. A maintainer who left a file marker-less by accident sees the same finding and reads it as a nudge to paste the current generated block back in, not as a command failure to chase down.

A date or self-expiring phrase inside inline code (`` `...` ``) or a URL is not flagged: a changelog entry or an example command legitimately names a date or says "awaiting #12" without going stale, since it is documenting a fixed past event or literal text rather than making a claim about the present.

A missing `AGENTS.md` is not an error: `hand doctor` finds nothing to flag and reports its zero count and exits `0`, leaving `hand init` to be the one place that complains about an incomplete fleet home.

---

### Optional: qmd for semantic search

`data/` grows large over time (400+ files, 4MB+ in real usage - briefs, reports, decisions accumulate).
`hand status` solves "what's happening now".
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
cd <worktree> && CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false claude --dangerously-skip-permissions "Read the brief at <brief-path> and carry out the task it describes. <operator-decision-rule>"
```

The brief path is included in the prompt because Claude Code takes prompt text, not a file path.
`<operator-decision-rule>` is `agentsmd.OperatorDecisionRule` verbatim (see "AGENTS.md (target)"), appended to every prompt-carrying template because the worktree is outside the fleet home and the worker never reads the home's `AGENTS.md`.
When configured, `--model <name>` and `--effort <level>` are inserted before the prompt.
Claude is the only harness with an effort flag: `opencode` takes `--model` but no effort, and
`codex`, `grok` and `pi` take neither (`harness.SupportsEffort`, `harness.SupportsModel`).
A declared value a harness has no flag for is warned about on stderr rather than dropped in
silence (see `hand spawn`).
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
cd <worktree> && OPENCODE_CONFIG_CONTENT='{"permission":{"*":"allow"}}' opencode --prompt "Read the brief at <brief-path> and carry out the task it describes. <operator-decision-rule>"
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
A template that hands the brief over as a file rather than as prompt text (Codex, Grok, Pi) has no
prompt to append to, so `agentsmd.OperatorDecisionRule` and the front-matter disclaimer never reach
those workers: the brief is all they read. `harness.CarriesPrompt` reports this, and `hand spawn`
warns on stderr rather than dropping it in silence (see `hand spawn`). The warning is a stopgap, not
the fix: carrying the text properly needs an inline-prompt flag verified against a real `--help` for
each of the three, which is blocked on the same missing binaries as
atqamz/secondhand#36.

## Herdr integration detail

### Connection

`hand` connects to herdr via its CLI (`herdr`) and/or its HTTP API.
The herdr server must be running before any `hand` operation that touches tabs/panes.

### Workspace and tab model

- One workspace per project. Workspace label = `hand:<project-name>`, not the bare project name
  (atqamz/secondhand#118): herdr derives a label from a workspace root directory's basename, so it
  is neither unique nor owned by `hand` - a project cloned to a directory sharing that basename
  with something else on the machine (the fleet home itself, another tool's workspace) produces a
  second workspace with the identical bare label, and `FindWorkspaceByLabel` returning whichever one
  `herdr workspace list` happens to return first is a silent dispatch into a workspace `hand` never
  created. The `hand:` prefix is `hand`'s own namespace, mirroring the existing `hand:<task-id>`
  convention for treehouse worktree ownership - it does not make the label unique on its own, but it
  does mean a collision now requires another `hand`-managed project to coincidentally share the same
  name, not any directory on the system. A workspace already created under the bare label from
  before this change is not adopted; `hand spawn` creates a new `hand:<project-name>` workspace
  alongside it, and the old one is orphaned (still functional, just no longer found by lookup).
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
| `hand send` | poll pane until the composer is empty, bounded by `--wait` + send keys to pane |
| `hand teardown` | close tab (+ close workspace if empty) |
| `hand watch` | subscribe to agent_status_changed events, or poll pane states |

### Herdr CLI calls

```sh
# list workspaces
herdr workspace list

# create workspace without focusing it - herdr cannot create an empty workspace, so this always
# creates a root tab and pane too, at --cwd; hand points --cwd at the worktree (not the clone) and
# reuses that root tab as the first task's tab (see "herdr tab rename" below) rather than
# discarding it and creating a second tab, which would leave it behind as an orphan shell.
# --env blanks any CLAUDE_CODE_CHILD_SESSION/CLAUDE_CODE_SESSION_ID/CLAUDECODE the herdr server
# itself inherited, so a server started from inside a Claude Code session never hands its own
# session identity, or a silently disabled transcript, to a pane it creates (atqamz/secondhand#109)
herdr workspace create --no-focus --cwd <worktree> --label hand:<project-name> \
  --env CLAUDE_CODE_CHILD_SESSION= --env CLAUDE_CODE_SESSION_ID= --env CLAUDECODE=

# create tab in an already-existing workspace - same sanitized --env set as workspace create above
herdr tab create --workspace <ws-id> --no-focus --cwd <worktree> --label <task-id> \
  --env CLAUDE_CODE_CHILD_SESSION= --env CLAUDE_CODE_SESSION_ID= --env CLAUDECODE=

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

Five outcomes:
- **Initialized:** proceed.
- **Not initialized** (either history): refuse with exit code 3, naming the exact remedy verbatim -
  `no-mistakes init` is idempotent and repairs a stale `working_path` in place, so the message reads
  `no-mistakes gate not initialized for project "<name>", run: cd <clone-path> && no-mistakes init`.
- **Binary missing or not runnable:** refuse with a distinctly different message (`no-mistakes
  binary not found or not runnable: <error>`), never collapsed into "not initialized" - the remedy
  for a missing binary is not `no-mistakes init`. This is a general error (exit code `1`), not a
  precondition: the world is not in a state the operator can fix by initializing anything.
- **Clone path does not exist on disk:** refuse with `no-mistakes clone path: <stat error>`, naming
  the real cause instead of the misleading "binary not found or not runnable" a chdir failure used
  to produce. Also a general error (exit code `1`).
- **Clone path exists but is not a git repository:** `no-mistakes status` itself exits 0 and prints
  `not in a git repository` in this case - a plain-looking success that used to read as a ready
  gate. Refuse instead with `no-mistakes clone path is not a git repository: <path>`, also a general
  error (exit code `1`), never `GateReady`: a caller must never be told to dispatch into a project
  the gate cannot cover.

The last two are atqamz/secondhand#97, and neither reads `~/.no-mistakes/state.sqlite` either. The
non-git one is read from `no-mistakes status`'s own text, the same way every other outcome here is.
The missing clone path is caught by stat-ing that path before the binary is run at all, because
otherwise the failed chdir is what surfaces, as the misleading "binary not found or not runnable".

Escape hatch: `--skip-gate-check` on both `hand spawn` and `hand promote` bypasses the preflight
and prints a warning to stderr naming the project, so bypassing it is visible in the transcript
rather than a silent env var.

`hand project list` runs the same check for every `no-mistakes`-mode project and carries `not
initialized` or `unreachable` in that project's `gate` column (and `"gate_issue"` in `--json`
output) when the check doesn't come back clean, counting them in `gate_issues`, so a stale or
never-initialized gate is visible without waiting for a spawn or promote to refuse.

### Gate-run visibility

Gate preflight answers whether a project's gate is initialized. It says nothing about whether any
given shipped PR actually went through it: a `no-mistakes`-mode project's gate can be ready and
still never have run against the branch a particular task's PR came from - the project was
registered after the fact, the PR was opened by hand outside the `pr` step, or the gate was
bypassed with `--skip-gate-check`. atqamz/secondhand#92 is that gap: `hand status` had no way to
surface a `done` `ship` task with a recorded PR that never went through a gate run at all.

`hand status` answers this with `project.GateRunPRs(clonePath)`: run `no-mistakes runs --limit
10000` in the project's clone and collect the PR URL each `completed` row recorded for itself, the
same read-only, text-scraping approach `GateStatus` already uses for gate preflight, never
`~/.no-mistakes/state.sqlite` directly. A PR is gated when it is exactly one of those URLs. The
answer is per clone, not per PR, and cached for the length of one render, so a fleet with several
`done` `ship` tasks on one project pays one `no-mistakes` process rather than one per task.

This establishes only that the no-mistakes `pr` step opened this exact PR from a run that reached
`completed` - nothing more, and the wording is deliberately no stronger than that:
- It is not a per-commit answer. `no-mistakes`'s own state is keyed on `working_path`, not per-PR
  (see "Gate preflight" above), and `hand` records no head commit for a task's PR to compare
  against. A push to the same branch after the matched run completed - amending the PR without a
  new run - still reads as gated, exactly as it did before that push.
- A PR opened by hand outside the no-mistakes `pr` step reads as `no run found` even sitting behind
  a run that did complete: nothing ties that URL to that run's own bookkeeping.
- A PR with no completed run recording that exact URL reads as `no run found`. A failure to ask
  no-mistakes at all reads as `unreachable`, the same bucket `gateIssue` uses for the identical
  failure in `hand project list`, so a question the check could not answer never renders as the
  stronger claim `no run found`. That bucket covers a missing clone, an unrunnable binary, and -
  read from `no-mistakes runs`'s own output text rather than its exit code, exactly as gate
  preflight reads them - a gate that was never initialized or whose `working_path` went stale, and
  a clone path that is not a git repository. The uninitialized case matters most: no-mistakes still
  holds that repo's completed runs, so reading its refusal as an empty run list would report a
  genuinely gated PR as never gated.

A marker that claims more certainty than this data supports would be worse than no marker at all,
so `hand status` only ever says what the two outcomes above actually establish - never "this PR is
safe" or "this PR is gated", only whether a matching completed run was found.

The check applies to a `done` `ship` task with a recorded PR on a registered `no-mistakes`
project only; every other task - in progress, `scout`, no PR yet, project not registered or not
`no-mistakes` - has nothing this check can say, so it stays silent.

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

A declared effort under a harness that cannot apply one warns on stderr, as does a declared model
under `codex`, `grok` or `pi`, and so does the operator-decision rule and the front-matter
disclaimer those same three cannot carry. Whatever a given launch drops is named on one combined
line, never one line per dropped value (see `hand spawn`).

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
- Finished items roll off `## Done` into `data/done-archive.md`, and dropped or superseded ones into `data/note-archive.md` with the reason they were dropped. Rolling off rather than deleting keeps the file the agent reads at session start short without discarding the record; a live firstmate home carried a 13 KB backlog against a 50 KB done-archive (atqamz/secondhand#47).

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
- no-mistakes: https://github.com/atqamz/no-mistakes mode=direct-pr upstream=kunchenguid/no-mistakes
```

Fields:
- `<name>`: project identifier, used in all `hand` commands.
- URL or `local`: git remote URL or `local` for repos without a remote.
- `mode=<mode>`: delivery mode.
- `upstream=<owner/repo>`: optional. The repo this project's PRs are opened against when it is a fork.
  Absent for the ordinary case, where a project contributes to its own repo.
  A URL form is accepted and normalized to the slug; an `upstream=` that cannot be resolved to one refuses the line rather than importing a project whose upstream could never match. A slug can never contain whitespace, which is what keeps this projection round-trippable.

Delivery modes:
- `no-mistakes`: worker runs no-mistakes pipeline, ships via PR with validation evidence. `hand
  spawn` and `hand promote` refuse to dispatch into a `no-mistakes` project whose gate is not
  initialized (see "Gate preflight").
- `direct-pr`: worker pushes branch and opens PR directly.
- `local-only`: worker commits to a local branch. Merging into default branch via `hand merge --local`.

## State management

### Design decisions

- **sqlite, not one JSON file per task.** Machine state lives in `state/hand.db` (see "Machine state and the prose corpus"). One row per task and per project, queried rather than globbed, with the whole registry consistent at every read. Pre-sqlite `state/<id>.json` files are imported once and moved to `state/migrated/`; see "Migration" below.
- **Current state, not append-only logs.** One row per task, updated in place. History comes from events.log and herdr event streams, not from accumulating status lines.
  `state/completions.jsonl` is a deliberate exception, not a contradiction: it does not track a task's current state, which the store already owns and which teardown deletes outright. It is durable history of a state that no longer exists, kept because it is the only record of a completion once `hand teardown` removes the row and `hand status` stops showing it - the log this principle warns against is one substituting for state that should be current, not one recording state that is gone for good.
- **Nothing durable is derived from a rendering.** Every view - `hand status`, the watcher's classification - is computed from the store and the report channel at the moment it is asked for. Reading a previous rendering back in as evidence is what produced atqamz/secondhand#53's accuracy defects, and no code path does it any more.
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

- `hand watch` tails the file once per task per poll tick from a byte offset persisted as `report_offset` on the task, classifying only whole, newline-terminated lines. A partial trailing line (a write still in flight) is left unconsumed until the next tick. Because the offset is durable, a restarted `hand watch` resumes exactly where it stopped: no already-surfaced line is replayed into stdout or `state/events.log`, and no line written moments before the restart is dropped. The last state and note a line classified to are carried across a restart as `last_report_state` and `last_report_note` rather than re-read from the file, so a pane found not-busy after a restart isn't mistaken for an unexplained stop - see "What survives a `hand watch` restart".
- Blank and whitespace-only lines are skipped by every reader, so `hand status`'s history never shows an entry `hand watch` didn't surface and a stray trailing newline can't masquerade as a malformed terminal report.
- If the file shrinks below the last known offset (recreated, truncated), tailing restarts from the beginning rather than erroring.
- The reader also tolerates the channel being rewritten in place rather than appended to, which is what a worker reporting with a truncating `>` redirect does. Every offset the reader persists sits immediately past a newline, so an offset whose preceding byte is no longer a newline is pointing into the middle of a line the rewrite replaced, and tailing restarts from the beginning. Without that check the fragment read from mid-line classified as a `malformed report` naming a healthy worker and quoting a mid-word slice of its own well-formed report, and since the fragment's start is decided by the length of the *previous* report, nothing about the report itself predicted it (atqamz/secondhand#140). Re-announcing a report the operator has already seen is the deliberate direction here: a duplicate `working:` line costs a wake at worst, while a fabricated malformed report costs a wake *and* misrepresents the worker.
- **The offset is trusted only together with a digest of what it consumed**, persisted as `report_digest` beside it. The newline check above is necessary and not sufficient: when the rewrite's total length happens to *equal* the offset, the offset sits at the end of the file with the file's own final newline behind it, which is byte-for-byte what "nothing was appended" looks like, so the new report was skipped entirely - never announced, with `last_report_state` left stale. Reports are one line of house-style prose and consecutive ones run within a few characters of each other, so the collision is a matter of time rather than a contrived input, and the cost is not a missed wake: the deferred verification is gated on the last recorded report state (`ClassifyDeferredDone`), so a same-length `done:` rewrite means a worker that finished is never announced as finished (atqamz/secondhand#149). The digest covers exactly the consumed prefix, never the unconsumed tail a worker may still be writing, and a digest that no longer matches discards the offset with it and tails from the beginning. An empty digest - a row written before the column existed, or a task whose worker has yet to report a line - falls back to the newline check alone, so an upgrade replays nothing; the first tick that consumes a line records the digest. Nothing outside the file is consulted: an mtime is granular enough to miss a rewrite inside its own resolution, and an inode is unchanged by precisely the in-place rewrite at issue.
- Each classified line becomes a `report-*` event (see `hand watch`) and updates the task's last-known report state, which `hand watch`'s idle classifier and `hand status`'s report suffix both consult. Both answer from the last line that *classified*, never simply the last line - `hand watch` by only advancing its carried state on one, `hand status` by skipping trailing malformed lines when it re-reads the file - so free text appended after a real report cannot erase it or make the two commands answer differently about the same quiet pane.
- **A `done` report is never trusted alone.** A worker's belief that it's finished is a claim, not a fact; it's cross-checked against completion evidence the worker didn't produce before it's allowed to change agent state or clear a pending decision, and until then it surfaces as "reported-done", not "done" (see `classifyReportDone` in `internal/watcher/events.go`). Each task kind has its own evidence: a ship task's merge (`merged` written by `hand merge`, whichever route it took - a PR merge or a `--local` fast-forward that leaves no PR at all - or a recorded PR the watcher's own `gh pr view` poll saw merged), and a scout task's `data/<id>/report.md` - the deliverable `hand promote` itself requires. The ship check never asks which mode the project uses. Evidence usually arrives *after* the `done` line is consumed, so the watcher re-checks every tick and fires the verified `done` event once, when the evidence lands (`ClassifyDeferredDone`) - including when it landed while the watcher was stopped, since the announcement is tracked by the durable `done_verified` marker rather than re-derived from whatever evidence is on disk at startup (see "What survives a `hand watch` restart").
- A line carrying exactly one PR URL auto-records it on a task that doesn't have one yet, exactly as if `hand pr` had been called - including `hand pr`'s full validation (repo-slug match against the project clone's origin remote, plus the `gh pr view` existence check), since a recorded PR is what `hand merge` later merges for real. Both paths call the one shared `project.ValidatePR`. Neither kind of miss aborts the watcher: an attempted recording that did not complete raises `pr-not-recorded` with the underlying error appended, flattened onto the event's single line, and its remedy is a human running `hand pr`, which records the URL the watcher could not; one the task lock kept the watcher from even attempting raises `pr-record-unknown`, which claims nothing about the outcome and points at `hand status`. The report line is consumed either way, so both go to the event stream and `state/events.log` rather than only to stderr. The one exception is silent by design: losing the lock race to the `hand pr` recording that very URL is not a failure, so the watcher re-reads the task and says nothing when the URL is already on record. A line with more than one URL, or a task that already has a PR recorded, is left alone so `hand pr`'s own explicit-mismatch refusal stays the single path for correcting a wrong record.

#### Unacknowledged terminal reports

A worker can reach `done` or `failed` with nothing attached to hear it: no session, no `hand watch`, and - if `config/notify` is unset - no notify hook either.
The event is not lost, since it will be classified and announced whenever a watcher next runs, but until then it is announced to nobody, and nobody is who is left to notice.
So `hand status` answers it directly: a task whose report channel carries a terminal state past the watcher's own durable `report_offset` carries the `unacknowledged` token in the `flags` field of both views, `done: <note> (unacknowledged)` in the detail view's `report` field, and `"unacknowledged": true` in either `--json` shape - omitted when false, so a consumer written before the field sees no change on the fleet it already understands.
When the flag applies, the detail view's `report` field names the classified terminal report rather than a later unclassified line, so the clause qualifies the state it describes - the same state the `reported` field names; the worker's literal last line is still shown there when the flag does not apply, and appears in `report_history` either way.
Both views also derive the flag from the whole report file, never from the detail view's 5-line history window, so trailing free text can never leave one view calling a completion acknowledged that the other flags.

**`report_offset` is the marker; there is no second one.**
Advancing it already means announced: the poll loop persists it only after the tick's events are announced (behavior step 11 under `hand watch`), and every announcement reaches `state/events.log` and the notify hook whether or not it reached anyone's stdout - the `--until-event` baseline ticks discard stdout and still both (see "Delivering an event to a supervisory agent").
A terminal line past the offset therefore reached nobody, and one behind it reached at least the durable log.
A dedicated `acknowledged` column would be that same fact stored twice, with a way for the two to disagree.
`report_digest` is not a second marker: it records nothing about what reached anyone, only whether the offset beside it still describes the file it was taken from, and a rewritten channel therefore reads as entirely unacknowledged - which it is, since no watcher announced a line of it.

Only the last classified line of the unconsumed tail counts, which is what keeps this from flagging history: a `done` a worker followed with more `working:` was superseded rather than missed, and a resumed worker's second `done` is flagged again on its own terms even though the first was consumed.

A terminal line the worker never terminated with a newline counts too, and this is the one place a reader deliberately parts company with the watcher.
The watcher leaves an unterminated trailing line unconsumed for its next tick, since the line will still be there (see "Report channel" above); a report it has not announced yet is exactly one that has reached nobody, so leaving it unflagged would let the silent completion this exists to surface back in through the newline.
A worker mid-append is therefore flagged for the moment its line is incomplete, which is the safe direction: the same reasoning as a watcher denied the task lock, where the transient answer is calling an acknowledged report unacknowledged, never the reverse.

### Holds

A hold (atqamz/secondhand#63) records that an id is waiting on something, so "what needs the operator" is derived from the store rather than authored by hand in `data/backlog.md` - that file stays out of scope for holds entirely; a design that finds itself parsing it has gone wrong.

**A hold is its own row, keyed by an arbitrary id, not a foreign key into the task table.** The alternative - a column or side table hanging off a task row - was rejected: it would fit the common case, but `hand teardown` deletes the task row, so a hold set on a task torn down while its question stayed open would vanish exactly when it matters most. A standalone row survives that teardown, which is the motivating case the issue names - work with no task row behind it, either never dispatched or torn down mid-question.

A second, independent reason favored the same answer at the time: before atqamz/secondhand#111, `Open` had no schema-version mechanism - it applied `schema` with `CREATE TABLE IF NOT EXISTS`, which is a correct create against a table that does not exist yet but a silent no-op against one that exists and is merely missing a new column, with no error and no column added. Adding a `blocked_on`-style column to the existing `task` table would have passed every test (which build fresh databases) and silently failed to apply to any already-migrated `state/hand.db` on disk. A brand-new `hold` table sidestepped the gap entirely: every existing database was missing the whole table, so `CREATE TABLE IF NOT EXISTS` took the create branch, not the no-op one, on both a fresh database and a migrated one. See "Schema versioning" below for the mechanism atqamz/secondhand#111 added; an ordinary column addition no longer needs a workaround like this one.

Three kinds, no others invented without a new issue:
- `operator`: waiting on a human. `reason` says what for.
- `blocked`: waiting on another id. `reason` says what for, `blocked_on` names the id.
- `limit`: waiting on the harness's own quota. Set and cleared by `hand watch`, never by an operator (atqamz/secondhand#136).

Set with `hand hold set`, which upserts - a second call on the same id replaces its kind, reason, and blocked-on, so narrowing down a reason is a re-run, not a clear-then-set. Cleared with `hand hold clear`, which deletes the row outright: no residue survives a clear for `hand status` to find later.

**`limit` is the one machine-set kind, and it is a projection rather than a record.** `hand watch` sets it when a worker's harness stops on a usage limit and clears it when that worker runs again; the durable schedule that actually resumes the worker lives on the task row, not here (see "Resuming a usage-limited worker" under `hand watch`). Being a projection is what makes its rules differ from the other two in both directions: `hand hold set --kind limit` is refused with exit 2, since a hold an operator authored by hand would be a claim about a quota `hand` never observed and nothing would ever clear it; but `hand hold clear` accepts it, because refusing that would make the one hold set on the operator's behalf the one hold they cannot undo. `hand watch` and `hand promote` clear it only when the hold on that id actually is of kind `limit`, so a machine clear never silently answers an operator's own question on the same id.
The set direction is guarded the same way, and has to be: `hand watch` writes the `limit` hold only when the id holds nothing or already holds a `limit` one, because a limit hold written over an operator's would not merely hide their question - the machine clear that follows, matching its own kind now, would delete the row and the question with it.
Yielding costs the wait nothing: a held id already refuses `hand spawn`, the schedule that resumes the worker lives on the task row, and the attempt that would have refreshed the projection says so on the watcher's diagnostic stream instead.

**Surviving teardown makes id reuse a hazard, so `hand spawn` refuses a held id.** The same standalone row that keeps a torn-down task's question visible would otherwise reattach it to whatever new work claimed the id next, which is the replay hazard `Delete` already guards against for the report channel by removing `state/<id>.status` (see "Report channel"). The report channel is a volatile wake log and can simply be discarded; a hold is either an operator's open question or a live worker's quota wait, so `hand spawn` refuses with exit 3 and names `hand hold clear <id>` rather than clearing it silently - answering the question is an acknowledgement `hand` has no business making on the operator's behalf. Clearing the hold is therefore the explicit step that says the question is settled, and it is the only escape hatch, since a `--force`-style flag would be the silent clear wearing a different name.

`limit` is the one kind that does *not* outlive the task it was set on, and `hand teardown` releases it (again, only when the hold really is of that kind). The reasoning that keeps an operator hold alive past teardown inverts here: there is no question left open, no worker left to resume, and no watcher that will ever clear it, so left behind it would refuse `hand spawn` on that id forever.

**A hold that cannot be read must never read as nothing waiting.** `ListHolds`/`ReadHold` surface every row exactly as stored, inconsistent ones included - filtering here is what would let an external write's mistake silently disappear from "what is held" - and `hand status` flags an inconsistent row (an unrecognized `kind`, a `blocked` hold with no `blocked_on`, or an `operator` hold carrying one) rather than rendering it as if it were valid. A store-level failure to read holds at all - not a single bad row, the whole read - propagates as a hard error out of `hand status`, fleet or single-task, rather than degrading to an empty list: this is the one place in `hand status` that does not fail open on a read, because a `holds[0]` block and "the store couldn't be read" look identical unless the second one is a fatal error instead.

### Concurrency

- Each task is one row. Writes go through sqlite, which serializes them; `hand`'s own named `flock`s (task, project, worktree, send) sit above that and guard whole command sequences, which a per-statement database lock cannot. The send lock is its own name rather than the task lock because it is held for the whole of a `hand send`'s composer wait, which the task lock must not be (see `hand send`). The project lock is what keeps the `data/projects.md` projection whole: rendering it is a read-modify-write over the file, so a second writer rendering from its own snapshot mid-write would drop a registered project from it.
- `hand watch` is the only long-running process; all other commands are short-lived. It is also the only singleton: at most one watcher per fleet home, enforced by an `flock` held for its whole lifetime (see "One watcher per fleet home").
- File locking: machine state is written through sqlite, which serializes writers itself.
- Multiple `hand` invocations against different tasks are safe in parallel.
- Multiple `hand` invocations against the same task should be avoided (agent discipline, not locking).
- **Concurrent tasks on same project:** allowed. Each gets its own treehouse worktree, kept off every other task's by treehouse's own pool lock; the collision guard in `hand spawn` and `hand promote` is defense-in-depth over `hand`'s bookkeeping on top of that (see "Collision guard"). File-level conflicts are resolved at merge time (rebase or conflict resolution), not at spawn time. The agent should avoid spawning tasks that touch the same files when possible, but this is a judgment call, not an enforced constraint.
- **No session lock.** Multiple supervisory sessions can run `hand` commands. The agent is responsible for not conflicting with itself. sqlite's own locking prevents corruption; duplicate work is an agent-level problem, not a CLI-level problem.
- **No daemon and no connection pool.** Every command opens the database, does its work and closes it, on a single connection (see "Not Postgres, and no daemon").

### Collision guard

`hand spawn` and `hand promote` both acquire a worktree and then cross-check it against every other task row before committing to it.
What they compare is the lease identity treehouse mints per acquisition (`lease_id` in `treehouse get --lease --json`, recorded on the task row), not the worktree path.

The path is the wrong key because treehouse recycles it.
A pool slot returned to its pool keeps its directory and is handed straight back out to the next task under a brand-new identity: treehouse regenerates `lease_id` on every acquisition, including a same-holder reacquisition of the same slot, so the identity is the only part of a lease that is never reused.

Keying on the path produced a false positive, not a missed collision.
`hand teardown` returns the worktree before it removes the task's row, deliberately, so a fault in the later step leaves the whole command retryable (see `hand teardown`).
If that removal does fail, the row survives naming a path treehouse has already freed; the next spawn or promote legitimately acquires that path, matches the stale row on path equality, force-returns its own exclusive lease and fails over a collision that never existed concurrently.
The guard cannot miss a real one: `worktree.Get` always passes `--lease`, and treehouse's pool lock refuses to hand out a currently-leased slot, so two tasks cannot concurrently hold one path in the first place.
It is defense-in-depth against `hand`'s own bookkeeping going stale, and the older claim that it prevented the stale-lease-after-crash bug (firstmate #947) was wrong: that bug was pid-based ownership, which `hand` has never used.

Path comparison remains the fallback whenever either side has no identity - a task row written before the `lease_id` column existed, or a treehouse older than v2.1.0, which is the version floor for the field.
Existing rows therefore keep being guarded through the migration and gain a real identity as each task is torn down and respawned; nothing has to be rewritten in place.

Every task row is compared, done and failed ones included.
Status says nothing about whether a worktree is still held - a task keeps its lease until teardown returns it - so a status filter here would drop rows that genuinely still hold the slot.

### Recovery

On restart (new supervisory agent session):
1. Agent runs `hand status` to see active tasks with current herdr state.
2. Optionally reads `state/events.log` for events that happened during the gap.
3. For each task, herdr state shows whether the pane is busy (working/blocked), not-busy (idle/done - see "Agent state" for why these carry no task-outcome signal by themselves), unreachable (unknown), or dead.
4. Dead herdr pane = dead worker. Agent decides: respawn or teardown.
5. No special recovery logic in `hand`. The CLI shows state; the agent decides action.

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
- `migrations` in schemaversion.go is an ordered list of SQL statements, one per schema change since that baseline, each moving `user_version` from its index to index+1. An ordinary column addition - atqamz/secondhand#48's `lease_id`, or atqamz/secondhand#78's `project.upstream` and `task.delivered_at`/`delivered_reason` - is two edits that stay in step: the column goes into the `schema` constant, so every database created from then on is built with it, and the matching `ALTER TABLE` is appended to `migrations`, so every database that already exists gains it on its next open. Nothing else in the package needs hand-written detection logic for it. atqamz/secondhand#136's `task.usage_limit_retry_at`/`usage_limit_attempts` are that ordinary two-edit case and deliberately take no backfill: an empty retry stamp is exactly "this task is not waiting on quota", which is the honest reading of every row written before `hand` could detect a limit at all. A column whose empty default would be wrong for a row that already exists - atqamz/secondhand#128's `task.pane_started_at` - takes a third edit: the entry carries a backfill `UPDATE` alongside its `ALTER TABLE`, and `readLegacyTask` computes the same value, since a legacy `state/<id>.json` import lands as an `INSERT` no migration step ever runs over.
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
- `2`: usage error: wrong argument count, unknown flag, unknown command or subcommand, a required flag left out (`hand hold set --reason`), mutually exclusive or mutually dependent flags (`hand watch --timeout` or `--event` without `--until-event`, `hand hold set --blocked-on` on any kind but `blocked` and its absence on a `blocked` one), an invalid argument or flag value (malformed project URL, unknown project mode, harness or hold kind, unparsable `--poll` duration, a non-positive `--timeout`, an unrecognized `--event` kind).
  A value the invocation did not supply is not a usage error: the same malformed value read from a `config/` default is a general error (code `1`).
- `3`: precondition failed, meaning the command refuses because the world is not in the state it requires: unlanded work, red CI, a missing or unmerged PR, a missing brief or report, a task, project or hold that does not exist, an id carrying an open hold (`hand spawn`), a task in the wrong kind or state (already merged, not a completed scout, already claimed by another command), a project name or worktree already taken, a project still referenced by active tasks, a PR that conflicts with one already recorded for a task or belongs to neither the task's project's repo nor its declared upstream (`hand pr`), a PR that `gh pr view` can't confirm exists (`hand pr`), a task branch whose PRs do not resolve to a single usable winner (`hand teardown`), a `no-mistakes`-mode project whose gate is not initialized (`hand spawn`, `hand promote` - see "Gate preflight"), a fleet home that already has a watcher attached (`hand watch`, remedied by `--takeover` - see "One watcher per fleet home").
  Two more apply to every command, since each one resolves a fleet home before it does anything: the working directory has no fleet home at or above it and `HAND_HOME` is unset, or `HAND_HOME` is set to a directory that is not a fleet home. The second refuses rather than falling back to the walk up, because a silent fallback is how an operator dispatches into the wrong fleet.
  These are signalled to `cmd` as sentinel errors (`cmd/precondition.go`), each carrying only the trailing phrase and wrapped by its caller as `<noun> "<name>" <phrase>`, so one condition renders as one string wherever it surfaces.
- `4`: no event delivered, only from `hand watch --until-event`: its `--timeout` elapsed, or it was signaled, without a transition. This includes the timeout elapsing anywhere in arming, the herdr reachability probe as well as the per-task probe sweep - the window is over either way, and no one task is at fault. Distinct from `0` because there the exit *is* the event delivery, and from `1` because the watcher itself did not fail (see "Delivering an event to a supervisory agent").
- `5`: arm-time probe failure, only from `hand watch --until-event`: one named task's herdr pane answered its pre-wait probe with a failure, named on stderr. Distinct from `4` because a specific worker is at fault and can be acted on, and from `0` because nothing was delivered (see "Delivering an event to a supervisory agent").
- `6`: send undelivered, only from `hand send`: the composer stayed busy for the whole `--wait` bound, so the message never reached the pane. Distinct from `1`, which for `hand send` means the send can never succeed (no such herdr pane, herdr itself erroring) - `6` means the opposite, a transient state a caller can retry, most simply with a longer `--wait`. Not `4` or `5`: those are reserved to `hand watch --until-event`.

### Error output

Every failure renders one document on stderr, whatever command produced it:

```
error: task "nosuch" not found
kind: precondition
exit: 3
help[1]:
  - Nothing changed: this refuses until the state it names is fixed, then the same command runs again
```

`kind` names the exit code above, so a caller branches on a word rather than memorizing which number means what: `general` (1), `usage` (2), `precondition` (3), `no-event` (4), `arm-failed` (5), `send-undelivered` (6).
`error` carries the message the command wrote, quoted whenever it holds a `:`, a quote or a newline, so a multi-line error stays one field rather than becoming lines the reader mistakes for further fields.
Every kind but `general` carries a `help[]` line naming what recovers it; a `usage` one names the command that refused, as in ``Run `hand hold set --help` for the arguments and flags this command accepts``.
`general` is the one code with no recovery that can be stated in advance, so it carries no `help[]` block rather than a line that says nothing.

The document goes to stderr, where the AXI principles put it on stdout, because `hand watch` owns stdout as an event stream a supervising agent consumes line by line.
Keeping failures off that stream means a reader never has to tell an event from an error.
A non-zero exit does not retract what a command already printed: `hand doctor`'s findings block and `hand watch`'s event lines stay on stdout whatever the exit code, so a caller reads the document on stderr for why it failed and stdout for what it found.

## Testing strategy

`herdr`, `treehouse` and `gh` are faked once, in `internal/faketool`, shared by the unit and end-to-end suites alike.
A test declares the fleet it wants - which pool slots exist, which workspaces are already open, which PRs are on which branch - and the package generates the POSIX-sh script for it.
Hand-writing a fake per test is what let the same vacuous shape through repeatedly, so a new test extends the shared fake rather than writing its own.

The rule that governs it: **a fake that answers a state-changing command identically before and after that command cannot test anything about the state change.**
So every fake models the state its own commands leave behind.
Returning a treehouse worktree frees its pool slot for the next `get` while leaving the directory in place; closing a herdr tab does not merely unlist it, every later command naming that tab or its pane answers `*_not_found` on stderr with exit 1; merging a PR moves it to `MERGED` for every later `pr view` and `pr list`.

`internal/faketool/FIDELITY.md` records what the real tool does for each call the suite depends on - exit code, stream, response shape, and what the call leaves behind - observed by running the real binary, not read off its documentation.
Only calls `hand` makes are recorded; behaviour no test exercises does not belong there.
A fidelity claim that is load-bearing names the test that fails without it, so the record and the check cannot drift apart silently.

The contract tests under `tests/contract` close the loop the other direction: built only under the `contract` tag and skipped where the real binary is absent, each one runs the recorded calls against the real tool in scratch state of its own and asserts the shape `FIDELITY.md` claims.
They are opt-in because CI installs no real `herdr` or `treehouse`; they exist so a fake that has quietly gone stale against a newer tool is discoverable by running them, rather than by a defect reaching an operator.

### Unit tests

- Machine state reading/writing, and that every field survives a round trip.
- Schema versioning: an existing version-0 database opening as the baseline, a registered migration applying automatically and only once, a fresh database skipping a migration its `schema` already builds, and a refusal on a database newer than the binary.
- Legacy import: idempotence over repeated runs, a database row winning over a restored file, and a loud refusal on an unparseable one.
- Index rebuild: that deleting `state/index.db` costs neither machine state nor corpus, and that a corrupt index recovers.
- Project registry parsing.
- Harness launch command construction.
- Event classification logic.
- Usage-limit detection and resume: that the harness catalogue recognizes every wording it claims to and reads a reset instant out of each - out of the freshest refusal on screen, not an older one still in scrollback - that an uncatalogued harness declines, and - the behavior that actually matters, not merely that a message is recognized - that a limited worker is steered and released while a worker that stopped for any other reason is never read from, steered, or held. The two ways an attempt yields to an operator are covered with it: a `send:<id>` lock already held spends no attempt, and a hold of another kind on the id is left standing.
- Brief validation.
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
- Migration of a pre-sqlite fleet home the suite builds by hand, run twice.
- Promote scout-to-ship cycle.
- Collision guard with concurrent tasks.
- Hold lifecycle: set, every `hand status` surface, surviving the teardown of the task it was set on, the spawn refusal on the reused id, and clear.
- Usage-limit resume end to end: a live `hand watch` finding the refusal on a stopped worker's pane, the `limit` hold and the durable schedule it writes, a restarted watcher steering that pane once its stamp comes due, the release when the pane runs again, and a second worker that stopped without a refusal on screen never being steered at all.

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
| AFK daemon | `fm-supervise-daemon.sh`, `fm-afk-launch.sh` (2,150 lines) | `hand watch --until-event` as the agent's own background task covers the awake path; `hand watch`'s in-process notify hook (see "Notifying a supervisory agent with no session watching") covers the AFK half through `config/notify`. |
| Multiple backends | `backends/herdr.sh`, `backends/cmux.sh`, `backends/zellij.sh`, `backends/orca.sh`, `fm-backend.sh` (5,500 lines) | herdr only. Add tmux fallback later if herdr proves insufficient. |
| Dispatch profiles | `fm-dispatch-select.sh`, `config/crew-dispatch.json` (340 lines + skill) | Pass `--harness`/`--model`/`--effort` explicitly, declare `model`/`effort` in the brief, or set defaults in `config/`. |
| Decision holds | `fm-decision-hold.sh`, decision-hold-lifecycle skill (500 lines) | No longer cut: `hand hold set`/`hand hold clear` record a hold in the store and `hand status` renders it (see "Holds" under "State management"). Only the lifecycle skill wrapped around it stays out. |
| Hook-based guards | `fm-continuity-pretool-check.sh`, `fm-continuity-command-policy.mjs`, `fm-subagent-pretool-check.sh` (450 lines) | CLI refuses bad operations internally. No hooks. |
| PR-check migration | `fm-pr-check-migrate.sh` (1,148 lines) | No legacy to migrate. |
| Fleet snapshot / bearings | `fm-fleet-snapshot.sh`, `fm-bearings-snapshot.sh` (1,860 lines) | `hand status --json` covers it. |
| *-axi wrappers | `gh-axi`, `tasks-axi`, `lavish-axi` | Agent uses `gh` directly. Backlog is a file. No wrappers. |
| Vocabulary translation | AGENTS.md section 9 (~40 lines of translation rules) | No internal jargon to translate if AGENTS.md is 25 lines. |
| Wake queues | `fm-wake-lib.sh`, `state/.wake-queue` (596 lines) | Watcher prints to stdout + `state/events.log`. No durable queue. |
| Turn-end guards | `fm-turnend-guard.sh`, `docs/turnend-guard.md` | The supervisory agent's harness handles its own session lifecycle. |
| Persona / role-play | "captain", "ahoy", nautical theming | Pure functionality. Users add personality if they want. Operator context is not persona and is not cut by this row: `data/operator.md` carries identity, authority and hard constraints, which is configuration the agent must not have to guess at (see "Directory layout"). |
| Session lock | `fm-lock.sh`, `fm-lock-lib.sh` | No session lock. Atomic file writes prevent corruption. Agent avoids duplicate work. |
| Bootstrap / session-start | `fm-session-start.sh`, `fm-bootstrap.sh` (56K lines combined) | Agent runs `hand status`. No 187-line digest. The `SessionStart` hook `hand init` installs is not this row's bootstrap: it runs the bare command when the session opens rather than rendering a file the agent then reads (see "Ambient context"). |

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

`hand init` writes the runtime dirs (`data/`, `state/`, `config/`, `projects/`), creates whichever of the `data/` skeleton files are missing (see "Directory layout"), and creates `state/hand.db` if it is not already there.
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
4. After update, refresh the generated AGENTS.md template in the resolved fleet home to the latest version, preserving user edits (see "AGENTS.md (target)"), and seed whichever `data/` skeleton files that home is missing, on the same absent-only terms as `hand init` (see "Directory layout"): the refreshed template directs the agent at those files, so the command that installs it leaves them in place rather than pointing at nothing. Outside any fleet home both are skipped silently; a `HAND_HOME` that names no fleet home is a warning, not a silent skip, since that is a misconfiguration rather than an absence. Seeding creates the runtime directories first, exactly as `hand init` does: a home resolves as one on its `state/hand.db` marker alone, so the `data/` directory it seeds into is not guaranteed to be there. A refresh or a seed that fails is likewise a warning on stderr, not a failed update, since the binary is already replaced. The seed warning names every file that could not be written, in the layout's own order, so two runs against the same broken home say the same thing.
5. Refresh the session hook too, on the same warning-not-error terms: an install that moved leaves the hook pointing at a path with no binary behind it any more (see "Ambient context").
6. Emit old version, new version, whether the binary was replaced, what became of the AGENTS.md template and the session hook, and what changed (from the installed release's notes).

Every run emits the same seven fields whatever happened, so a caller reads one schema rather than a
set of lines that appear or do not:

```
hand update
current: v0.3.1
latest: v0.4.0
update_available: true
updated: true
agents_md: refreshed
session_hook: refreshed
notes[1]:
  - fix: teardown no longer strands worktrees
help[1]:
  - Run `hand doctor` to check this home's AGENTS.md against the template v0.4.0 installed
```

`agents_md` and `session_hook` are each one of `refreshed`, `unchanged`, `no-fleet-home`, `failed`,
or `not-applicable` when nothing was installed. They are reported separately because they fail
separately. The stderr warnings of steps 4 and 5 stay on stderr: they name a fault in the world
around a successful update, not a field of its result.

`--check`, and a `hand update` that finds nothing newer, emit the same document with
`updated: false`, both outcome fields `not-applicable` and `notes[0]:`. An available update adds a
`help[]` line naming `hand update`; up to date adds none, since there is nothing to do:

```
hand update --check
current: v0.3.1
latest: v0.4.0
update_available: true
updated: false
agents_md: not-applicable
session_hook: not-applicable
notes[0]:
help[1]:
  - Run `hand update` to install v0.4.0, which also refreshes this home's AGENTS.md template
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

**`.github/workflows/triage-label.yaml`:** the tracked file is authoritative - runs on `issues: opened`, labels the issue `needs-triage` unless its author (login, case-insensitive) is `atqamz`. Scoped to `issues: write` only, uses the built-in `GITHUB_TOKEN` via `gh issue edit`, no third-party action. Does not fire on reopen and does not distinguish bot authors from human ones (atqamz/secondhand#116).

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

**`tools/commentlint/`:** the tracked source is authoritative - a `go run ./tools/commentlint .` target that `make lint` and the CI workflow both invoke over the whole tree, exiting 1 with one `file:line:column` per violation.
CONTRIBUTING.md's "Comments" section owns the two rules it checks, their exemptions, and why they are the only two that are machine-checkable.

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

Deliverable: can spawn a worker, watch it work, and clean up after.

### Phase 2: Supervision and lifecycle

Make the watcher work so the supervisory agent doesn't have to poll manually.

7. `hand watch` - event loop with herdr push/poll + event log.
8. `hand merge` - merge PR (with CI check) or local fast-forward.
9. `hand project sync` - fast-forward clones.
10. `hand promote` - scout to ship promotion.
11. `hand notify` - out-of-band notifications.

Deliverable: fully supervised fleet lifecycle.

### Phase 3: Polish

12. `hand status --json` - machine-readable fleet state.
13. Error messages and edge cases.
14. Integration tests.
15. README and AGENTS.md finalization.

Deliverable: ready for daily use.

## AGENTS.md (target)

**`internal/agentsmd/agentsmd.go`'s `generatedBody` constant** is authoritative - the template `hand init` writes into a new fleet home's `AGENTS.md` and `hand update` refreshes there, delimited by `hand:generated` markers so anything a user adds outside that span survives a refresh. `hand doctor` (see the CLI specification) reports perishable content, generated-block drift, and absent generated markers in a real fleet home's `AGENTS.md` without fixing any of them - the first two fail the command, absent markers alone does not (`agentsmd.SeverityInfo`).

One rule in that template, `agentsmd.OperatorDecisionRule`, is also an exported constant, because a worker runs in a worktree that is never under the fleet home and so never loads the home's `AGENTS.md`. `internal/harness` appends that same constant to every prompt-carrying launch template (see "Harness launch templates"), the only channel the rule has to a worker. It stays one string in one place rather than two copies that drift.

This repo's own `AGENTS.md` carries no `hand:generated` markers, so `agentsmd.Refresh` declines to touch it even if a maintainer runs `hand init` in the checkout and `state/hand.db` makes `internal/home.IsHome` report true for it. Rather than hand-keep a second copy of `generatedBody` in sync (the drift #44 was filed against), this file's own Rules section points at `generatedBody` and SPECS.md by name instead of restating it: one prose template, one place it can go stale.
`hand doctor` run in that checkout therefore reports the absent markers every time - correct, since the template really will never refresh here - but that finding is informational (see "`hand doctor`"), so the command still exits `0` in this checkout rather than failing forever over a state the maintainers chose on purpose.
