# Secondhand

Talk to one agent. Ship with a crew.

Secondhand is a single Go CLI binary, `hand`, that orchestrates a fleet of coding agents across projects.
A supervisory agent runs in the secondhand directory, records tasks in a markdown backlog, and calls `hand` to spawn autonomous workers into isolated git worktrees.
The CLI owns lifecycle correctness, state management, and process supervision.
It was born from firstmate, an agent fleet supervisor built as 34K lines of shell, and rebuilds that concept as a clean CLI.

## Quick start

```sh
git clone https://github.com/yes2games/secondhand
cd secondhand
make build
./hand init --setup
./hand project add https://github.com/org/repo
./hand spawn fix-login repo
```

Write `data/fix-login/brief.md` before spawning; `hand spawn` refuses to start a worker without a brief.

## Core concepts

- **Projects**: git repositories cloned under `projects/` and registered in `data/projects.md`. Each has a delivery mode: `no-mistakes`, `direct-pr`, or `local-only`.
- **Tasks**: units of work identified by a unique ID. Ship tasks produce a branch and PR; scout tasks investigate and produce `data/<id>/report.md`.
- **Briefs**: task instructions at `data/<id>/brief.md`, written by the supervisory agent before spawning a worker.
- **herdr tabs**: each worker runs in its own herdr tab. herdr provides semantic agent state (working/idle/done/blocked) and push events, so no terminal scraping.
- **treehouse worktrees**: workers operate in isolated git checkouts acquired from a treehouse pool, never in the project clone itself.
- **Dashboard**: `data/dashboard.md` is the living fleet overview, auto-maintained by `hand`. The agent reads it for context; the user watches it for visibility.
- **Backlog**: `data/backlog.md` is a plain markdown task queue, read and edited directly by the supervisory agent.

## CLI overview

| Command | Description |
| --- | --- |
| `hand init` | Initialize runtime directories and skeleton files; `--setup` runs interactive first-time configuration |
| `hand project add` | Clone and register a repository |
| `hand project list` | List registered projects |
| `hand project remove` | Unregister a project, keeping its clone |
| `hand project sync` | Fast-forward project clones to their remote default branch |
| `hand spawn` | Spawn a worker agent in an isolated worktree |
| `hand status` | Show fleet overview or single-task detail |
| `hand send` | Send a message to a running worker |
| `hand watch` | Blocking watcher that prints actionable fleet events |
| `hand merge` | Merge a task's completed work |
| `hand teardown` | Clean up a completed task, fail-closed on unlanded work |
| `hand promote` | Promote a completed scout task into a ship task |
| `hand notify` | Send an out-of-band notification via a configured command |

Run `hand --help` or `hand <command> --help` for full details.

## Architecture

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

## Requirements

- Go 1.26.5 or newer (build only)
- [herdr](https://github.com/yes2games/herdr) - terminal multiplexer with semantic agent state
- [treehouse](https://github.com/yes2games/treehouse) - git worktree pool manager
- `gh` - GitHub CLI, used for PR operations

Optional:

- [no-mistakes](https://github.com/yes2games/no-mistakes) - validation pipeline for projects in `no-mistakes` mode
- `qmd` - search over historical task data

## Installation

From source:

```sh
make build
```

From nix:

```sh
nix build
```

From releases: download the binary for your platform from the [releases page](https://github.com/yes2games/secondhand/releases).

## Configuration

Local preferences live as plain files under `config/`: default worker harness, model, effort, notification command, and watcher timings.
Run `hand init --setup` to discover installed harnesses and tools and write the defaults interactively.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
