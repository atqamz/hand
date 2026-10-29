# Install

## Requirements

- Linux, macOS (Apple silicon; Intel builds are released but not tested), or Windows 10 or 11 (amd64). See [Platform support](platforms.md).
  - On macOS, `hand open` starts the board and `hand supervisor start` the watcher when none runs, detached, and the Luvus server starts directly, without restart on failure. See [Autostart](autostart.md#macos) to run the board at login.
  - On Windows, Hand runs natively, without WSL. See [Windows](#windows).
- git, to make worktrees. Go 1.26.5 or newer only to build Hand from source.
- Luvus, which Hand needs for its UHP 1.x protocol and was tested with at 0.14. `hand init` pins a copy under `~/.secondhand/luvus/`, so a system upgrade never changes the Luvus a fleet runs: the `luvus` on `PATH` when there is one, otherwise the tested version, which it downloads and checks against the release's SHA-256 (nothing is pinned if the check fails). `hand luvus pin` pins another one.
- At least one harness, logged in: Claude Code (`claude`), Codex (`codex`) or opencode 2.x (`opencode`). Hand lists each harness's models itself, with a listing command and no prompt (see [Safe use](safe-use.md#what-hand-runs-to-list-models)), and keeps the lists in `~/.secondhand/models/`: without a list Hand cannot verify a model and warns, but still starts it; `hand models --refresh` makes the lists now. On Windows, Hand starts only a real `.exe` for a harness, never a `.cmd` shim: see [Windows](#windows). opencode uses the model from its own configuration unless a start or profile names one as `provider/id`.
- Optional: the Antigravity CLI (`agy`), logged in, so that `agy models` lists its models; signed out, Hand has no agy list and warns.
- Optional: the GitHub Copilot CLI (`copilot`, from `npm install -g @github/copilot`, or downloaded by running `gh copilot` once), logged in with `copilot login`, for workers only. Hand looks for `copilot` in three places and takes the first hit: `PATH`; npm's global `bin` folder (`NPM_CONFIG_PREFIX`, else what `npm prefix -g` prints, which Hand asks once per process, 3 seconds at most, without a shell; `prefix` itself on Windows); and the folder `gh copilot` downloads to, `gh/copilot` under `$XDG_DATA_HOME`, else `~/.local/share/gh/copilot` (on Windows `%LOCALAPPDATA%\GitHub CLI\copilot`). The harness then starts by that absolute path, so a board started before you changed `PATH` finds it too. Hand does not check the login: signed out, Copilot shows `Please use /login to sign in` and drops the brief, and Hand notes the attempt blocked when it sees that line at launch.
- Optional: `notify-send` for desktop notifications and `xdg-open` for `hand open` (on macOS, the built-in `osascript` and `open`; on Windows, `hand open` runs `rundll32 url.dll,FileProtocolHandler` and there are no built-in notifications; see [Alerts on your phone](alerts.md) for a PowerShell recipe), and a systemd user session to keep the watcher, the board and Luvus running.

## Install a release

Install the latest release on Linux or macOS (amd64 or arm64):

```sh
s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && sh -c "$s"
```

You can choose another version:
- **Edge:** `s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && HAND_INSTALL_VERSION=edge sh -c "$s"`. Edge is the rolling build of `main` that passed CI.
- **A pinned release:** `HAND_INSTALL_VERSION=v0.12.0`.

`HAND_INSTALL_DIR` picks the folder; the default is `~/.local/bin`. The script checks the download against the release's `checksums.txt`, and installs nothing if it does not match. Do not pipe the download into `sh`: when `curl` fails, `sh` gets empty input and exits 0, as does `sh -c "$(curl ...)"`. The assignment in the command above makes a failed fetch of `install.sh` stop it with `curl`'s exit code, and a failure inside the script exits 1 with one `install.sh:` line. The command needs a POSIX shell (`sh`, `bash`, `zsh`), not fish.

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
- `HAND_INSTALL_VERSION`: `edge` for the rolling build of `main` that passed CI, or a pinned release such as `v0.12.0`;
- `HAND_INSTALL_DIR`: the folder, which may contain spaces;
- `HAND_INSTALL_BASE`: the releases URL, as in `install.sh`.

They stay set in that PowerShell window until you close it.

Or pass them as switches, which win over the variables:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1))) -Edge
```

- `-Edge`, or `-Version v0.12.0`;
- `-Dir PATH`;
- `-Base URL`;
- `-TimeoutSec N`: give up on a download that takes longer than N seconds (default 300). It has no variable.

Hand runs its own git with `core.longpaths=true`, and starts Luvus with `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0` and `GIT_CONFIG_VALUE_0` set to the same, so a worker's git inherits it unless you already set `GIT_CONFIG_COUNT`. A Luvus server you started yourself does not have it. For the git you run and as a fallback, allow long paths once:

```powershell
git config --global core.longpaths true
```

Hand never starts a `.cmd` or `.bat` for a harness: `cmd.exe` would run it in the wrong folder and mangle the prompt, and text from an issue that reaches the prompt could run as a command. `npm install -g` makes such a shim (`%APPDATA%\npm\codex.cmd`) and leaves the native `.exe` inside the package under `node_modules`. For `claude` (`@anthropic-ai/claude-code`), `codex` (`@openai/codex`), `opencode` (`opencode-ai`) and `copilot` (`@github/copilot`, whose `.exe` is in its `@github/copilot-win32-x64` or `-arm64` package under it), Hand looks there beside the shim and starts that `.exe`, so the npm install works. For `agy`, and when the exe is missing (`--omit=optional`, or `--ignore-scripts` for `claude` and `opencode`, whose install script puts it there), Hand refuses the shim, naming the file. Install the standalone Codex instead, from its [README](https://github.com/openai/codex): `powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"`. As of Codex 0.163 it puts `codex.exe` in `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin` and on your user `PATH`. Hand takes a `.exe` anywhere on `PATH` before any `.cmd`, so putting the folder of a `codex.exe` on `PATH` is enough too. `Get-Command codex -All` lists what `PATH` holds, and `hand doctor` and `hand route list` show a harness found only as a batch file as unavailable. The board searches the `PATH` it started with, so restart it after you change `PATH` or install a harness: `hand board stop`, then `hand board`.

Claude asks once per folder whether to trust it, and every worktree is a new folder. Trust the worktrees folder once from PowerShell, so it does not ask in each one:

```powershell
$w = if ($env:SECONDHAND_HOME) { "$env:SECONDHAND_HOME\worktrees" } else { "$HOME\.secondhand\worktrees" }; New-Item -ItemType Directory -Force $w | Out-Null; cd $w; claude
```

Codex asks once per repository whether to trust it, and a worktree is trusted through its main repository. Run `codex` once in the repository root and choose Trust, and it asks in none of that repository's worktrees: Hand then presses nothing and prints `trust: not asked`. Without that, Hand presses the screen when it names the worker's own worktree and the project's repository, which saves the trust for that repository in `$env:CODEX_HOME\config.toml` (`~\.codex\config.toml` by default) for all of your Codex use; [Safe use](safe-use.md) says what that means. Hand never edits `config.toml`.

Hand tells a Windows worker to write its report to a file outside the worktree and run `hand report add --status done --file PATH`, which works in PowerShell and in Git Bash.

Signing and ssh, from secondary sources that nobody has verified on a Windows machine running Hand: HTTPS with `gh auth login` is the low-ceremony way to push, and needs no agent. Windows' `ssh-agent` service is disabled by default, and Git for Windows' bundled ssh does not talk to it unless you set `git config --global core.sshCommand C:/Windows/System32/OpenSSH/ssh.exe`. A worker that signs commits with an ssh key needs both.

Windows user profile folders whose names contain spaces or non-ASCII characters are untested.

What differs from Linux and macOS:
- there are no units: no service keeps the watcher, the board or Luvus alive. `hand supervisor start` starts the watcher when it is free, `hand open` starts the board when none answers, the running board starts the watchers again within 30 seconds of a crash, and Luvus starts directly;
- desktop notifications are off: the watcher's notify step does nothing;
- stopping a worker ends the root and its descendants at once, found by parent process ID, with no graceful phase. Linux and macOS signal the worker's process group instead, and Linux also ends the processes left in the attempt's worktree; macOS does not. What each reaches is in [Attempt](vocabulary.md#attempt);
- `hand update` swaps the binary by renaming the running `hand.exe` to `hand.exe.old`, stops the watcher and the board by their process IDs, and starts them again. The next `hand` run deletes the old file.

See [Autostart](autostart.md#windows) to run the board at logon.
