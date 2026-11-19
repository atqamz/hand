# Install

## Requirements

- Linux, macOS (Apple silicon; Intel builds are released but not tested), or Windows 10 or 11 (amd64). See [Platform support](platforms.md).
  - On macOS, `hand open` starts the board and `hand supervisor start` the watcher when none runs, detached, and the Luvus server starts directly, without restart on failure. See [Autostart](autostart.md#macos) to run the board at login.
  - On Windows, Hand runs natively, without WSL. See [Windows](#windows). Luvus needs the Visual C++ 2015-2022 x64 runtime, which a stock Windows lacks and neither the installer nor the wizard installs: `winget install Microsoft.VCRedist.2015+.x64`.
- git, to make worktrees. Go 1.27.3 or newer only to build Hand from source.
- Luvus, which Hand needs for its UHP 1.x protocol and was tested with at 0.14. `hand init` pins a copy in the fleet folder, under `luvus/<os>-<arch>/`, so a system upgrade never changes the Luvus a fleet runs and the copy travels with the fleet: the `luvus` on `PATH` when there is one, otherwise the tested version, which it downloads and checks against the release's SHA-256 (nothing is pinned if the check fails). `hand luvus pin` pins another one.
- At least one harness, logged in: Claude Code (`claude`), Codex (`codex`), opencode 2.x (`opencode`) or oh-my-pi (`omp`). Hand lists each harness's models itself, with a listing command and no prompt (see [Safe use](safe-use.md#what-hand-runs-to-list-models)), and keeps the lists in `~/.secondhand/models/`: without a list Hand cannot verify a model and warns, but still starts it; `hand models --refresh` makes the lists now. On Windows, Hand starts only a real `.exe` for a harness, never a `.cmd` shim: see [Windows](#windows). Hand finds a harness on `PATH`, then in mise's `shims` folder (`shims` under `MISE_DATA_DIR` when it is absolute, else `$XDG_DATA_HOME/mise`, else `~/.local/share/mise`; on Windows `%LOCALAPPDATA%\mise`). It reads only that folder: a `shims_dir` set in mise's config, the system folder `mise reshim --system` writes (`/usr/local/share/mise/shims`) and mise's command wrappers are not searched, so add such a folder to `PATH`. opencode uses the model from its own configuration unless a start names one as `provider/id`.
- Optional: the Antigravity CLI (`agy`), logged in, so that `agy models` lists its models; signed out, Hand has no agy list and warns.
- Optional: oh-my-pi (`omp`, from `s=$(curl -fsSL https://omp.sh/install) && sh -c "$s"`), logged in, for the supervisor and workers. Hand lists its models with `omp models --json`, reads its agent folder from `PI_CODING_AGENT_DIR` (give an absolute path, and the same one in the Luvus server's environment, the watcher's and the Board's), else `$PI_CONFIG_DIR/agent` (a relative value is under the home directory), else `~/.omp/agent`, and finds a supervisor's session under that folder's `sessions/`. Measured on Linux only: Windows and macOS have not run a real `omp`, and Hand starts no `.cmd` shim for it.
- Optional: the GitHub Copilot CLI (`copilot`, from `npm install -g @github/copilot`, or downloaded by running `gh copilot` once), logged in with `copilot login`, for workers only. Hand looks for `copilot` in four places and takes the first hit: `PATH`; mise's `shims` folder, as above; npm's global `bin` folder (`NPM_CONFIG_PREFIX`, else what `npm prefix -g` prints, which Hand asks once per process, 3 seconds at most, without a shell; `prefix` itself on Windows); and the folder `gh copilot` downloads to, `gh/copilot` under `$XDG_DATA_HOME`, else `~/.local/share/gh/copilot` (on Windows `%LOCALAPPDATA%\GitHub CLI\copilot`). The harness then starts by that absolute path, so a board started before you changed `PATH` finds it too. Hand does not check the login: signed out, Copilot shows `Please use /login to sign in` and drops the brief, and Hand notes the attempt blocked when it sees that line at launch.
- Optional: the native window, `hand-desktop`, on Linux amd64 and arm64 (one window per user; an alert's **Open** and `hand-desktop --open LINK` use it). It needs a display (X11 or Wayland; no cgo) and a running Board, which it reads over loopback; `hand update` installs it next to `hand` from a release that carries it, together with a launcher entry (`$XDG_DATA_HOME/applications/hand-desktop.desktop`, default `~/.local/share/applications`, and the icon `$XDG_DATA_HOME/icons/hicolor/512x512/apps/hand-desktop.png`) so any XDG launcher opens it as `Hand`, and its asset names are `hand-desktop-linux-amd64.tar.gz` and `hand-desktop-linux-arm64.tar.gz`. On Windows amd64, `hand update` installs `hand-desktop.exe` next to `hand.exe` for `edge` builds only (`hand update --channel edge`): it has opened a window on a CI runner and not yet on a real Windows PC, writes a Start Menu shortcut `Hand` (`%APPDATA%\Microsoft\Windows\Start Menu\Programs\Hand.lnk`, with `hand.ico` beside `hand-desktop.exe`), and `hand doctor` says in its `desktop window` row once it is installed that no real Windows PC has run it, and in its `start menu shortcut` row whether the shortcut is in place. On macOS (arm64 and amd64), `hand update` builds `~/Applications/Hand.app` from the window it downloaded and checked (`Contents/Info.plist`, `PkgInfo`, `MacOS/hand-desktop`, `MacOS/hand` as a symlink to the installed `hand`, and `Resources/Hand.icns`), so Finder and Spotlight open `Hand`; `hand doctor` says `untested on a real Mac` in its `desktop window` row and has a `desktop app` row for the bundle. The app is unsigned, and no real Mac has run it: CI opened its window on an arm64 runner, and the Intel build has never run anywhere. Install it with `hand update`, which is the supported route: a copy that arrives with a quarantine mark, for example through AirDrop or a browser download, is stopped by Gatekeeper. A window that does not start on a Mac is worth a bug report (the bug form), with the output of `hand doctor`. The window should find `hand` through the symlink in the bundle (read from the code, not run: CI starts the Board itself), but a Board that it starts from Finder inherits the short `PATH` Finder gives apps, so a Board started that way can miss tools such as `git` from Homebrew and your harnesses; start the Board with `hand board start` from a terminal when that matters. A stable build on Windows runs the Board in a browser (`hand open`).
- Optional: `notify-send` for desktop notifications (the Hand app's alert feed needs neither it nor a `notify` command) and `xdg-open` for `hand open` (on macOS, the built-in `osascript` and `open`; on Windows, `hand open` runs `rundll32 url.dll,FileProtocolHandler` and there are no built-in notifications; see [Alerts on your Companion](alerts.md) for a PowerShell recipe), and a systemd user session to keep the watcher, the board and Luvus running.

## Install a release

Install the latest release on Linux or macOS (amd64 or arm64):

```sh
s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && sh -c "$s"
```

You can choose another version:
- **Edge:** `s=$(curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh) && HAND_INSTALL_VERSION=edge sh -c "$s"`. Edge is the rolling build of `main` that passed CI.
- **A pinned release:** `HAND_INSTALL_VERSION=v0.14.0`.

`HAND_INSTALL_DIR` picks the folder; the default is `~/.local/bin`. The script checks the download against the release's `checksums.txt`, and installs nothing if it does not match. It also fetches `hand-iroh`, the sidecar for [Companion access over iroh](companion-app.md#the-hand-iroh-sidecar), when the release carries it and the new `hand` names it: the asset is checked against its own `.sha256` and must report the version that `hand` needs, then it goes next to `hand`. If that check fails, the script installs neither and leaves what you have. A release without it (`edge`, 0.13.0 and earlier) is not an error: the script says so and goes on. It does not fetch `hand-desktop`: on Linux, run `hand update` once after installing to put the window next to `hand` (see [Requirements](#requirements)). Do not pipe the download into `sh`: when `curl` fails, `sh` gets empty input and exits 0, as does `sh -c "$(curl ...)"`. The assignment in the command above makes a failed fetch of `install.sh` stop it with `curl`'s exit code, and a failure inside the script exits 1 with one `install.sh:` line. The command needs a POSIX shell (`sh`, `bash`, `zsh`), not fish.

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

## Uninstall

`hand uninstall` removes what `hand update` and the install scripts placed, prints every path it removed and every path it kept, and removes `hand` itself last. `hand uninstall --dry-run` prints the same two lists and changes nothing, not even creating `~/.secondhand`; a folder that does not exist is not listed.

- **Removed:** `hand`, `hand-iroh` and the window `hand-desktop` that sit beside it, and the window's launcher entry. A file that is not a regular file, such as a symlink or a folder with one of those names, is kept and listed; the command never follows a symlink.
- **Kept when they belong to another install:** the launcher entry goes only when it points at the window beside the `hand` that runs the command, so a `go build` copy in another folder leaves the entry of your installed `hand` alone, and it is listed as kept. A `hand` under another name (`hand-next`) that sits beside a file named `hand` removes only itself: `hand`, `hand-iroh`, `hand-desktop`, `hand.ico`, the launcher entry and the Windows `PATH` entry belong to that `hand`, and are kept and listed.
- **Kept, always:** your fleet folders, `~/.secondhand` (or `$SECONDHAND_HOME`: the fleet index, the worktrees and the backups) and the window's composer drafts and preferences (`$XDG_STATE_HOME/hand-desktop`, `~/.local/state/hand-desktop`, `%LOCALAPPDATA%\hand-desktop` on Windows). Delete them yourself if you want them gone.
- **Refuses**, with exit code 3 and nothing removed, while a Board or a watcher of this install runs. It names `hand board stop` and `hand watch stop` (run that one in each fleet's folder). A Board that another `hand` runs does not block it.
- **A step that fails** leaves `hand` in place, so you can run the command again. A second run, with nothing left to remove, prints `nothing to remove` and exits 0.
- **Not removed:** a systemd unit, LaunchAgent or scheduled task that you made to start the Board ([Autostart](autostart.md)), Luvus, git and the harnesses. It removes the `hand` that runs it, so a build you made yourself is removed too, with the files beside it that are its own (see above).

**Linux.** `~/.local/bin/hand`, `hand-iroh` and `hand-desktop`, and the launcher entry: `$XDG_DATA_HOME/applications/hand-desktop.desktop` and its icon `icons/hicolor/512x512/apps/hand-desktop.png`. An entry without the `X-Hand-Managed=true` line is not Hand's and is kept.

**macOS.** `~/.local/bin/hand` and `hand-iroh`, and the bundle `~/Applications/Hand.app` with the window inside it. A bundle whose `Contents/Info.plist` has no `HandManaged` key is not Hand's and is kept.

**Windows.** `hand.exe`, `hand-iroh.exe` and `hand-desktop.exe` in the install folder (`%LOCALAPPDATA%\hand\bin` by default), `hand.ico`, the Start Menu shortcut `Hand.lnk`, every `*.old` file that `hand update` left, and the install folder's entry in your user `PATH`. A `Hand.lnk` whose description is not Hand's is kept.
- A running program cannot delete its own file, so `hand.exe` is renamed to `hand.exe.old` and the command prints the one PowerShell line that deletes it; run it after the command has exited. Open a new terminal for the `PATH` change.
- An install made by the setup wizard is removed from Windows Settings, Apps, Hand (the [wizard](#the-setup-wizard) also has `unins000.exe`). `hand uninstall` detects it, says so and changes nothing.

If `hand` is already gone, delete the files above by hand.

## Windows

Native Windows is supported, without WSL. It has run in CI and not yet on a real Windows machine, so report any problem through the issue form. [Platform support](platforms.md) lists what CI covers, and [`docs/dogfood/windows.md`](dogfood/windows.md) has the checks for a real Windows machine.

Install the latest release into `%LOCALAPPDATA%\hand\bin` from PowerShell 5.1 or newer. No admin rights are needed:

```powershell
irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex
```

The script downloads `hand-windows-amd64.zip`, checks it against the release's `hand-windows-amd64.sha256`, installs nothing if it does not match, and adds the folder to your user `PATH` once. It fetches `hand-iroh-windows-amd64.zip` too, as `install.sh` fetches its sidecar, and puts `hand-iroh.exe` next to `hand.exe`; a running one is renamed to `hand-iroh.exe.old` as `hand.exe` is. The release carries `hand-desktop-windows-amd64.zip`. This script does not install it (the [setup wizard](#the-setup-wizard) does), and `hand update` installs it for `edge` builds only, untested on a real Windows PC; on a stable build use the Board in a browser. Open a new terminal afterwards.

Set the options as environment variables before the command, as in `$env:HAND_INSTALL_VERSION = 'edge'; irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex`:
- `HAND_INSTALL_VERSION`: `edge` for the rolling build of `main` that passed CI, or a pinned release such as `v0.14.0`;
- `HAND_INSTALL_DIR`: the folder, which may contain spaces;
- `HAND_INSTALL_BASE`: the releases URL, as in `install.sh`.

They stay set in that PowerShell window until you close it.

Or pass them as switches, which win over the variables:

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1))) -Edge
```

- `-Edge`, or `-Version v0.14.0`;
- `-Dir PATH`;
- `-Base URL`;
- `-TimeoutSec N`: give up on a download that takes longer than N seconds (default 300). It has no variable.

### The setup wizard

`Hand-Setup-VERSION-x64.exe` is a setup wizard for the same install, attached to the `edge` [release](https://github.com/atqamz/hand/releases/tag/edge) only, until the Windows window runs on stable builds. It installs for your user, with no administrator prompt, into `%LOCALAPPDATA%\hand\bin`, the folder `install.ps1` uses, so `hand update` keeps either install current. It puts `hand.exe`, `hand-desktop.exe`, `hand.ico` and, when the release has it, `hand-iroh.exe` there (the `edge` release has no `hand-iroh`, so its wizard has none), writes the Start Menu shortcut `Hand` for the window, adds the folder to your user `PATH`, and offers "Start Hand" on its last page. A desktop icon is off unless you tick it. It does not install Git, Luvus or a harness: `hand init` and `hand doctor` say what is missing. The window runs on Windows in `edge` builds only, so the wizard comes from the `edge` release until it runs on stable.

The wizard is not signed. A copy you download with a browser shows "Windows protected your PC": press **More info**, then **Run anyway**. Check the download first against the `.sha256` beside it; the line must print `True`:

```powershell
$want = (Get-Content .\Hand-Setup-VERSION-x64.sha256).Split(' ')[0]
(Get-FileHash .\Hand-Setup-VERSION-x64.exe -Algorithm SHA256).Hash -eq $want
```

The `irm ... | iex` command at the top of this section stays the route with no prompt: a file that PowerShell fetches carries no download mark, so SmartScreen does not ask.

Running an older wizard over a newer install, one that `hand update` has brought forward, downgrades it to the wizard's version.

To uninstall, use Settings, Apps, Hand, or run `unins000.exe` in `%LOCALAPPDATA%\hand`. It removes the files it installed, any `*.old*` file that `hand update` left, the shortcuts and the `PATH` entry, and closes the Hand window if it is open. It keeps `%USERPROFILE%\.secondhand`, your fleet folders and the window's composer drafts, and says so. It never ends a fleet's work: while a Board or a watcher of this install runs, it stops and names the two commands to run first, `hand board stop` and `hand watch stop`. A `Hand.lnk` in your Start Menu that is not Hand's is left alone, by the install and by the uninstall. In CI the wizard is installed and uninstalled silently on a Windows runner ([Platform support](platforms.md)); nobody has run it on a real Windows PC yet.

Hand runs its own git with `core.longpaths=true`, and starts Luvus with `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0` and `GIT_CONFIG_VALUE_0` set to the same, so a worker's git inherits it unless you already set `GIT_CONFIG_COUNT`. A Luvus server you started yourself does not have it. For the git you run and as a fallback, allow long paths once:

```powershell
git config --global core.longpaths true
```

Hand never starts a `.cmd` or `.bat` for a harness: `cmd.exe` would run it in the wrong folder and mangle the prompt, and text from an issue that reaches the prompt could run as a command. `npm install -g` makes such a shim (`%APPDATA%\npm\codex.cmd`) and leaves the native `.exe` inside the package under `node_modules`. For `claude` (`@anthropic-ai/claude-code`), `codex` (`@openai/codex`), `opencode` (`opencode-ai`) and `copilot` (`@github/copilot`, whose `.exe` is in its `@github/copilot-win32-x64` or `-arm64` package under it), Hand looks there beside the shim and starts that `.exe`, so the npm install works. For `agy` and `omp`, and when the exe is missing (`--omit=optional`, or `--ignore-scripts` for `claude` and `opencode`, whose install script puts it there), Hand refuses the shim, naming the file. The `omp` installer has a PowerShell path that should produce a real `omp.exe`, but a Windows install has not been run. Install the standalone Codex instead, from its [README](https://github.com/openai/codex): `powershell -ExecutionPolicy ByPass -c "irm https://chatgpt.com/codex/install.ps1 | iex"`. As of Codex 0.163 it puts `codex.exe` in `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin` and on your user `PATH`. Hand takes a `.exe` anywhere on `PATH` before any `.cmd`, so putting the folder of a `codex.exe` on `PATH` is enough too. `Get-Command codex -All` lists what `PATH` holds, and `hand doctor` and `hand models` show a harness found only as a batch file as unavailable. The board searches the `PATH` it started with, so restart it after you change `PATH` or install a harness: `hand board stop`, then `hand board`.

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
- desktop notifications are off: the watcher's notify step does nothing, while the alert feed for the Hand app still works;
- stopping a worker ends the root and its descendants at once, found by parent process ID, with no graceful phase. The root is `hand`, with the harness as its child, whereas Linux and macOS replace `hand` with the harness and signal the worker's process group instead, and Linux also ends the processes left in the attempt's worktree; macOS does not. What each reaches is in [Attempt](vocabulary.md#attempt);
- `hand update` swaps the binary by renaming the running `hand.exe` to `hand.exe.old`, stops the watcher and the board by their process IDs, and starts them again. The next `hand` run deletes the old file.

See [Autostart](autostart.md#windows) to run the board at logon.
