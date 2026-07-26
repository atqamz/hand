# Secondhand

Talk to one agent. Ship with a crew.

Secondhand is a single Go CLI binary, `hand`, that orchestrates a fleet of coding agents across projects.
A supervisory agent runs in the secondhand directory, records tasks in a markdown backlog, and calls `hand` to spawn autonomous workers into isolated git worktrees.
The CLI owns lifecycle correctness, state management, and process supervision.
It was born from [firstmate](https://github.com/kunchenguid/firstmate), an agent fleet supervisor built as 34K lines of shell, and rebuilds that concept as a clean CLI.

## Quick start

```sh
git clone https://github.com/atqamz/secondhand
cd secondhand
make build
./hand init --setup
./hand project add https://github.com/org/repo
```

The worker lifecycle commands are available, including `hand spawn`, `hand status`, `hand send`, and `hand teardown`.

## Core concepts

- **Projects**: git repositories cloned under `projects/` and registered in `data/projects.md`. Each has a delivery mode: `no-mistakes`, `direct-pr`, or `local-only`.
- **Tasks**: units of work identified by a unique ID. Ship tasks produce a branch and PR; scout tasks investigate and produce `data/<id>/report.md`.
- **Briefs**: task instructions at `data/<id>/brief.md`, written by the supervisory agent before spawning a worker.
- **herdr tabs**: each worker runs in its own herdr tab. herdr provides semantic agent state (working/idle/done/blocked) and push events, so no terminal scraping.
- **treehouse worktrees**: workers operate in isolated git checkouts acquired from a treehouse pool, never in the project clone itself.
- **Dashboard**: `data/dashboard.md` is the living fleet overview, auto-maintained by `hand`. The agent reads it for context; the user watches it for visibility.
- **Backlog**: `data/backlog.md` is a plain markdown task queue, read and edited directly by the supervisory agent.

## CLI overview

| Command | Description | Status |
| --- | --- | --- |
| `hand init` | Initialize runtime directories and skeleton files; `--setup` runs interactive first-time configuration | Available |
| `hand project add` | Clone and register a repository | Available |
| `hand project list` | List registered projects | Available |
| `hand project remove` | Unregister a project, keeping its clone | Available |
| `hand project sync` | Fast-forward project clones to their remote default branch | Available |
| `hand spawn` | Spawn a worker agent in an isolated worktree | Available |
| `hand status` | Show fleet overview or single-task detail | Available |
| `hand send` | Send a message to a running worker | Available |
| `hand watch` | Blocking watcher that prints actionable fleet events | Available |
| `hand merge` | Merge a task's completed work | Available |
| `hand teardown` | Clean up a completed task, fail-closed on unlanded work | Available |
| `hand promote` | Promote a completed scout task into a ship task | Available |
| `hand notify` | Send an out-of-band notification via a configured command | Available |
| `hand update` | Update the installed binary from the latest GitHub Release; `--check` reports availability without installing | Available |

Run `hand --help` for details on currently available commands.

## Architecture

```mermaid
flowchart TD
    user[User] -->|"requests, decisions, merge it"| supervisor["Supervisory agent<br/>reads AGENTS.md and data/<br/>edits data/backlog.md<br/>calls hand commands"]
    supervisor --> task1["Task 1 worker<br/>herdr tab"]
    supervisor --> task2["Task 2 worker<br/>herdr tab"]
    supervisor --> taskN["Task N worker<br/>herdr tab"]
    task1 --> worktrees["Treehouse worktrees<br/>isolated git checkouts"]
    task2 --> worktrees
    taskN --> worktrees
    worktrees --> ship["Ship: branch to PR to merge to teardown"]
    worktrees --> scout["Scout: investigate to report.md to teardown"]
```

## Requirements

- Go 1.26.5 or newer (build only)
- [herdr](https://github.com/ogulcancelik/herdr) - terminal multiplexer with semantic agent state
- [treehouse](https://github.com/kunchenguid/treehouse) - git worktree pool manager
- [gh](https://github.com/cli/cli) - GitHub CLI, used for PR and release operations

Optional:

- [no-mistakes](https://github.com/yes2games/no-mistakes) - validation pipeline for projects in `no-mistakes` mode
- [qmd](https://github.com/tobi/qmd) - search over historical task data

## Installation

From source:

```sh
make build
```

Set `VERSION` to embed a release version in the binary:

```sh
make build VERSION=0.1.0
```

From nix:

```sh
nix build
```

The flake covers `aarch64-darwin`, `aarch64-linux`, and `x86_64-linux`.
On Intel macOS, use `make build` or a release binary.

From releases: download the binary for your platform from the [releases page](https://github.com/atqamz/secondhand/releases).

To update an installed binary, run `hand update`.
It downloads the release asset for the current OS and architecture, verifies its SHA256 checksum, and replaces the running binary in place.
When run inside a workspace it then refreshes the generated part of that workspace's AGENTS.md, leaving your own additions untouched, and prints the new release's notes.
`hand update --check` reports whether an update is available without installing it.
Every other command run in a workspace prints a one-line notice to stderr when a newer release exists, checked at most once a day and cached in `state/.version-check`.
Builds without an embedded version never print the notice.

## Configuration

Currently supported preferences live as plain files under `config/`: default worker harness, model, and effort.
Run `hand init --setup` to discover installed harnesses and tools and write the defaults interactively.

Workers run their harness interactively so they can be steered and watched.
For Claude Code that means first-run dialogs, and `hand spawn` and `hand promote` answer the workspace-trust and bypass-permissions ones for you, then confirm the worker is actually running before reporting success.
A worker that never comes up fails the spawn instead of being reported as started.
The exception is Claude Code's managed-settings approval prompt on hosts with organization-managed settings: accepting it is a host-wide trust decision, so `hand` refuses it and tells you to accept it yourself once and respawn.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
