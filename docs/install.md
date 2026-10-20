# Install

## Requirements

- Linux, macOS (Apple silicon; Intel builds are released but not tested), or Windows 10 or 11 (amd64). See [Platform support](platforms.md).
  - On macOS, `hand open` starts the board and `hand supervisor start` the watcher when none runs, detached, and the Luvus server starts directly, without restart on failure. See [Autostart](autostart.md#macos) to run the board at login.
  - On Windows, Hand runs natively, without WSL. See [Windows](#windows).
- git, to make worktrees. Go 1.26.5 or newer only to build Hand from source.
- Luvus, which Hand needs for its UHP 1.x protocol and was tested with at 0.14. `hand init` pins a copy under `~/.secondhand/luvus/`, so a system upgrade never changes the Luvus a fleet runs: the `luvus` on `PATH` when there is one, otherwise the tested version, which it downloads and checks against the release's SHA-256 (nothing is pinned if the check fails). `hand luvus pin` pins another one.
- At least one harness, logged in: Claude Code (`claude`), Codex (`codex`) or opencode 2.x (`opencode`). Run Codex once before using it, so its model cache exists. opencode uses the model from its own configuration.
- Optional: the Antigravity CLI (`agy`), logged in, so that `agy models` lists its models.
- Optional: `notify-send` for desktop notifications and `xdg-open` for `hand open` (on macOS, the built-in `osascript` and `open`; on Windows, `hand open` runs `rundll32 url.dll,FileProtocolHandler` and there are no built-in notifications; see [Alerts on your phone](alerts.md) for a PowerShell recipe), and a systemd user session to keep the watcher, the board and Luvus running.

## Install a release

Install the latest release on Linux or macOS (amd64 or arm64):

```sh
curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh | sh
```

You can choose another version:
- **Edge:** `curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh | HAND_INSTALL_VERSION=edge sh`. Edge is the rolling build of `main` that passed CI.
- **A pinned release:** `HAND_INSTALL_VERSION=v0.11.0`.

`HAND_INSTALL_DIR` picks the folder; the default is `~/.local/bin`. The script checks the download against the release's `checksums.txt`, and installs nothing if it does not match.

On Windows, run this in PowerShell; [Windows](#windows) has the options:

```powershell
irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex
```

Releases after v0.11.0 and the edge build also get a signed build attestation, best effort: a failed signing does not hold back the release. To check who built a download: `gh attestation verify hand-linux-amd64.tar.gz --repo atqamz/hand` (the GitHub CLI, signed in with `gh auth login`). If it fails, treat the download as unverified. The installers and `hand update` do not check it.

## Build from source

To build from source instead:

```sh
git clone https://github.com/atqamz/hand
cd hand
go build -o ~/.local/bin/hand .
```

To try it next to an older Hand, build it under another name such as `~/.local/bin/hand-next`. Hand calls itself by its binary's name in the fleet's `AGENTS.md`, its skill, its help lines and its errors, so a supervisor in that fleet runs `hand-next`. When you later install it as plain `hand`, run that `hand init` in each fleet to rewrite them.

## Windows

Native Windows is supported, without WSL. It has run in CI and not yet on a real Windows machine, so report any problem through the issue form. [Platform support](platforms.md) lists what CI covers, and [`docs/dogfood/windows.md`](dogfood/windows.md) has the checks for a real Windows machine.

Install the latest release into `%LOCALAPPDATA%\hand\bin` from PowerShell 5.1 or newer. No admin rights are needed:

```powershell
irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex
```

The script downloads `hand-windows-amd64.zip`, checks it against the release's `hand-windows-amd64.sha256`, installs nothing if it does not match, and adds the folder to your user `PATH` once. Open a new terminal afterwards.

Set the options as environment variables before the command, as in `$env:HAND_INSTALL_VERSION = 'edge'; irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex`:
- `HAND_INSTALL_VERSION`: `edge` for the rolling build of `main` that passed CI, or a pinned release such as `v0.11.0`;
- `HAND_INSTALL_DIR`: the folder, which may contain spaces;
- `HAND_INSTALL_BASE`: the releases URL, as in `install.sh`.

They stay set in that PowerShell window until you close it.

Or pass them as switches, which win over the variables:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1))) -Edge
```

- `-Edge`, or `-Version v0.11.0`;
- `-Dir PATH`;
- `-Base URL`;
- `-TimeoutSec N`: give up on a download that takes longer than N seconds (default 300). It has no variable.

Hand runs its own git with `core.longpaths=true`, and starts Luvus with `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0` and `GIT_CONFIG_VALUE_0` set to the same, so a worker's git inherits it unless you already set `GIT_CONFIG_COUNT`. A Luvus server you started yourself does not have it. For the git you run and as a fallback, allow long paths once:

```powershell
git config --global core.longpaths true
```

Claude asks once per folder whether to trust it, and every worktree is a new folder. Trust the worktrees folder once from PowerShell, so it does not ask in each one:

```powershell
$w = if ($env:SECONDHAND_HOME) { "$env:SECONDHAND_HOME\worktrees" } else { "$HOME\.secondhand\worktrees" }; New-Item -ItemType Directory -Force $w | Out-Null; cd $w; claude
```

Hand tells a Windows worker to write its report to a file outside the worktree and run `hand report add --status done --file PATH`, which works in PowerShell and in Git Bash.

Signing and ssh, from secondary sources that nobody has verified on a Windows machine running Hand: HTTPS with `gh auth login` is the low-ceremony way to push, and needs no agent. Windows' `ssh-agent` service is disabled by default, and Git for Windows' bundled ssh does not talk to it unless you set `git config --global core.sshCommand C:/Windows/System32/OpenSSH/ssh.exe`. A worker that signs commits with an ssh key needs both.

Windows user profile folders whose names contain spaces or non-ASCII characters are untested.

What differs from Linux and macOS:
- there are no units: no service keeps the watcher, the board or Luvus alive. `hand supervisor start` starts the watcher when it is free, `hand open` starts the board when none answers, the running board starts the watchers again within 30 seconds of a crash, and Luvus starts directly;
- desktop notifications are off: the watcher's notify step does nothing;
- stopping a worker ends its whole process tree at once, with no graceful phase;
- `hand update` swaps the binary by renaming the running `hand.exe` to `hand.exe.old`, stops the watcher and the board by their process IDs, and starts them again. The next `hand` run deletes the old file.

See [Autostart](autostart.md#windows) to run the board at logon.
