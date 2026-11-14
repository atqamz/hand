# Hand

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/atqamz/hand)

Hand is a personal supervisor layer for agents. You talk to one supervisor agent. It captures every request as a task, starts Claude Code, Codex, opencode, Antigravity, GitHub Copilot or oh-my-pi workers in isolated git worktrees through [Luvus](https://github.com/RizRiyz/luvus), waits for them with zero tokens, reads their reports, and asks you only through decisions. Each fleet is a folder with its own SQLite state, and one `hand board` shows every fleet.

![The Board in a browser, dark theme. On the left, Needs holds one open decision, "Which theme should new users get?", with three choices and a Send answer button, and one unread worker report; below it, the task list shows two active tasks and one in the inbox. On the right, the chat with the supervisor: a request to register a project and fix a typo, the worker's report, and the supervisor's replies.](docs/images/board-desktop.png)

<img src="docs/images/board-phone.png" width="260" alt="The same Board at phone width: the Needs tab with the decision's three choices, a note field and a Send answer button, and the worker's report below.">

A native window, `hand-desktop` on Linux, can read the same fleet beside the browser: it refreshes the selected fleet's status, open screen and conversation every second while focused and every 5 seconds while not, and not at all while minimised, sending the API's change cursor so an unchanged board answers `304`, and it keeps the composer's draft across a refresh.

## Platform support

Hand is built for one operator, who runs it every day on Linux. macOS (Apple silicon) and native Windows (amd64) are supported best effort, with no timeline for fixes: CI runs the tests on both, one real Mac has run a fleet with a stand-in `claude`, and no real Windows machine has run one yet, so they rely on your reports. [`docs/platforms.md`](docs/platforms.md) says how each part is verified.

## Installation

> [!WARNING]
> Hand needs git and at least one harness that can run the supervisor (every listed harness except Copilot), installed and logged in before use. It starts only these:
>
> - **Claude Code** (`claude`): install [Claude Code](https://claude.com/product/claude-code) and run `claude auth login`
> - **Codex** (`codex`): install [Codex CLI](https://developers.openai.com/codex/cli) and run `codex login`
> - **opencode 2.x** (`opencode`): install [opencode 2](https://opencode.ai/v2/docs/) and run `opencode auth login`
> - **oh-my-pi** (`omp`, optional; measured on Linux only): install [oh-my-pi](https://omp.sh) and run `omp login`
> - **Antigravity** (`agy`, optional): install [Antigravity CLI](https://antigravity.google/docs/getting-started?tab=cli), then run `agy` once and sign in
> - **GitHub Copilot** (`copilot`, workers only): install [Copilot CLI](https://docs.github.com/copilot/how-tos/copilot-cli) and run `copilot login`

### Command line

On Linux or macOS:

```sh
s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && sh -c "$s"
```

On Windows, in PowerShell, then open a new terminal; [Windows](docs/install.md#windows) has two settings to make once:

```powershell
irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex
```

Then run `hand init` in a folder of its own, then `hand open`: init pins Luvus, downloading the tested version when none is on `PATH`, and open starts a board when none answers.

`hand update` moves to a newer release. `hand doctor` checks your setup and prints a fix for each problem.

On Linux amd64 and arm64, `hand update` also installs `hand-desktop` next to `hand`: start it for the native window, which starts the board itself with `hand board start` when none answers and never stops one. It needs a display and no cgo, a launcher entry opens it from any XDG launcher, and `hand doctor` has a `desktop` row and a `desktop entry` row for it. macOS and Windows use the board in a browser.

For the rolling `edge` build of `main`, or a pinned release such as `v0.14.0`, set `HAND_INSTALL_VERSION` first: `s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && HAND_INSTALL_VERSION=edge sh -c "$s"`, or `$env:HAND_INSTALL_VERSION = 'edge'` before the PowerShell line. Releases after v0.11.0 and the edge build also get a build attestation, best effort: `gh attestation verify hand-linux-amd64.tar.gz --repo atqamz/hand` checks who built a download.

[`docs/install.md`](docs/install.md) has every install option and building from source; [Requirements](docs/install.md#requirements) has the rest. [`docs/update.md`](docs/update.md) says what `hand update` does and how to restore a backup.

## Safe use

Read this before you start a supervisor:

- **No sandbox.** Every worker runs as your own OS user, in a git worktree that is not a fence. It can read whatever your account can: `~/.ssh`, `~/.config/gh`, `~/.gnupg`, other fleets and each fleet's `board.token`.
- **Prompts skipped.** The supervisor and every worker run with their permission prompts skipped, such as `claude --dangerously-skip-permissions`, so ordinary commands run without a prompt. Claude Code still asks before it removes a critical path such as your home folder, and denies after two minutes.
- **What that means.** A worker that obeys text planted in an issue, a PR or a review can use all of that access. Run Hand as a dedicated OS user or in a VM, with a narrow GitHub token and a protected default branch. [`docs/safe-use.md`](docs/safe-use.md) has the exact flags, the steps ranked by value, and what no setup prevents.

## Getting started

You use two commands, then the browser. The other `hand` commands are for the supervisor, the agent that runs your fleet.

1. **Make a fleet.** In a folder of its own:

   ```sh
   mkdir -p ~/fleets/demo && cd ~/fleets/demo
   hand init
   ```

   This writes `hand.db`, the folder's `AGENTS.md` and `CLAUDE.md`, the `secondhand` skill, a starter `routing.json` and `memory/operator.md`, registers the folder under `~/.secondhand`, and pins Luvus in the folder's `luvus/<os>-<arch>/`, downloading the tested version when none is on `PATH`. The folder also keeps its own `backups/` and `cache/models/`. It starts nothing. Running it again is safe.
2. **Open it.** `hand open` opens the fleet's page in your browser and logs you in. `hand open --print` prints the login link for another browser or a phone; keep it private, since it holds the fleet's token. When no board answers, `hand open` starts one first ([Keep the watcher and the board running](docs/autostart.md#keep-the-watcher-and-the-board-running)).
3. **Work in the browser.** A new fleet has no supervisor yet: press **Start** on a harness, with the model and effort you want. That starts the supervisor in the fleet's Luvus session and its watcher. Then:
   - write to the supervisor in the **Chat** tab, and name the repositories it should work on by absolute path. A first message could be: *Register /abs/path/to/repo as project demo, add a task to fix a typo in its README, and start a quick worker.*
   - answer its decisions under **Needs**;
   - read its reports, and press **Mark read**.

The other commands (`hand task`, `hand attempt`, `hand decision`, `hand report`, `hand orient`, `hand wait` and the rest) are what the supervisor and its workers run. You rarely need them, and the [command table](docs/commands.md) is a reference.

## Autostart

One autostart is enough: run `hand board` once at login, and the board starts every fleet's watcher that needs one. On Linux that is the systemd user unit `hand unit board` prints, on macOS a LaunchAgent, and on Windows a shortcut in the Startup folder. [`docs/autostart.md`](docs/autostart.md) has each recipe, and how to remove the per-fleet watch units of earlier versions.

## If something does not work

Run `hand doctor` in the fleet folder. It checks Hand, git, the harnesses, Luvus, the fleet, each project's git settings, the board, the phone address and the watcher, and prints a fix for each problem. It repairs nothing and spends no tokens, so paste its output into a bug report. The board finds `claude` and the other harnesses on the `PATH` it started with, which can differ from your shell's: restart the board after you install one with `hand board stop` and `hand board`, or restart its unit (a systemd unit keeps the `PATH` from when you ran `hand unit board`, see [Linux](docs/autostart.md#linux)).

Report a problem with the [bug form](https://github.com/atqamz/hand/issues/new?template=bug.yml), and a vulnerability as [`SECURITY.md`](SECURITY.md) says.

## Testing on macOS or Windows

If you have a Mac or a Windows machine, you can help by running the checks in [`docs/dogfood/`](docs/dogfood/README.md), yourself or with a coding agent: they use a throw-away home that never touches your own fleets, and each failure becomes an issue. A passing run moves a row of the [platform table](docs/platforms.md) from "CI" to a real-machine result.

## Documentation

- [How Hand works](docs/how-it-works.md): why it exists, the flow from you to the supervisor and its workers, and the daily loop.
- [Install](docs/install.md): requirements, install options, Windows notes and building from source.
- [Update](docs/update.md): what `hand update` does, how it switches Luvus, how to restore a backup, and updating on a timer.
- [Platform support](docs/platforms.md): how each part is verified on Linux, macOS and Windows.
- [Fleets](docs/fleets.md): where Hand finds a fleet, and what its folder and `~/.secondhand` hold.
- [Autostart](docs/autostart.md): how the watcher and the board keep running, and how to start the board at login.
- [The board](docs/board.md): the fleet page, `hand open` and reaching the board from a phone.
- [JSON API](docs/api.md): the board's `/api/v1` views and its actions (a message, a decision's answer, a report's ack and the supervisor controls), the keys guards, the change cursor, and the auth, CSRF and error contract.
- [Reach the board from a phone](docs/phone.md): the Hand app over iroh, and an SSH tunnel, Cloudflare Tunnel with Access, WireGuard and a LAN address, with what each exposes.
- [The supervisor](docs/supervisor.md): its harness flags, trust screens, attachments, reboots, idle wakes and timers.
- [Alerts on your phone](docs/alerts.md): send every alert to your own command.
- [Clean the inbox](docs/inbox.md): prune old attachments.
- [Safe use](docs/safe-use.md): what the agents can do with prompts skipped, how to run Hand safer, and what nothing prevents yet.
- [Commands](docs/commands.md): every `hand` command.
- [Design and non-goals](docs/spec.md) and [vocabulary](docs/vocabulary.md).

## Media

Images for issues and pull requests live on the `blob` branch of this repository. That branch is append-only, holds no code, and is never merged into `main`. A clone fetches every branch, so it downloads the media too; fetch only the default branch to keep it small:

```sh
git clone --single-branch --branch main https://github.com/atqamz/hand.git
```

Measured 2026-11-12 with `du -sb .git`: 18,489,594 bytes (18.5 MB) against 115,522,218 bytes (115.5 MB) for a full clone; add `--depth 1` for a one-shot checkout (5,344,819 bytes, 5.3 MB). The media branch grows, so the full-clone figure will too.

## Development

Read [`AGENTS.md`](AGENTS.md) and [`docs/spec.md`](docs/spec.md) before changing anything. [`CONTRIBUTING.md`](CONTRIBUTING.md) says how to send a change and asks for a sign-off on each commit. The dependencies are `modernc.org/sqlite`, `golang.org/x/sys` for macOS process start times, and `github.com/egoist/mygo` for the `cmd/hand-desktop` window, which `.github/desktop/smoke.sh` builds with the release stamps and checks with `--version`. Before committing, run:

```sh
gofmt -l .
go vet ./...
go test -race ./...
```

The tests set `SECONDHAND_HOME` to a temporary folder and never touch the real one. CI runs the full suite with `-race` on Linux only, because the race detector slows the SQLite driver 5 to 14 times. macOS and Windows run the full suite with plain `go test`. All three operating systems run the `TestLive` tests with `-race`.

On Windows, `.gitattributes` keeps text files LF whatever `core.autocrlf` says, and a short `TEMP` (for example `D:\ht`) keeps paths short. A few tests need `sleep` on `PATH` (Git for Windows' `usr\bin`) or the privilege to create symlinks (Developer Mode), and fail without them. Without a C compiler, `go test -race` cannot run.

## License

MIT
