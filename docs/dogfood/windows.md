# Windows checks

Windows 10 or 11, amd64, in an account without admin rights. The shell is PowerShell, 5.1 or 7; Git Bash appears only where a check says so. Read [`README.md`](README.md) for the safety rules and the result format, and your entry point ([`human.md`](human.md) or [`agent.md`](agent.md)) for how to run and report.

Nobody has run these commands on a real Windows machine before the first dogfood run. A command marked `unverified` may be wrong. A command that fails for a reason that is not Hand is a guide bug: file it as `<ID>: guide command fails`.

In each check, **Run** says whether an agent can run it to the end (`agent`) or it ends in a question that only a person at the screen can answer (`needs human`). **Real sessions** counts the Claude Code sessions it starts on your account.

## Two runs: with and without Git Bash

Run the checks twice, and file two run issues.

- **Run A, with Git for Windows.** Git for Windows is installed, so Git Bash (`bash.exe`) exists. Add `with Git for Windows` after the OS version in the run issue title.
- **Run B, without Git Bash.** No Git for Windows: `git.exe` comes from another place, and there is no Git Bash. Add `no Git Bash` after the OS version. See `W0` for how. If you cannot get a `git.exe` without Git Bash, skip run B with that reason.

Which checks differ:

| Check | Run A | Run B |
|---|---|---|
| `W0`, `W1`, `W4`, `W8`, `W9`, `W11`, `W14` | run | run again: Claude Code may behave differently when there is no Git Bash, and that is a finding |
| `W3` | both halves: the report from PowerShell and from Git Bash | only the PowerShell half; skip the Git Bash half, with the reason |
| `W2`, `W5`, `W6`, `W7`, `W10`, `W12`, `W13` | run | skip with `covered by run A`: they do not depend on Git Bash |

## Checks

The order is by risk, riskiest first, as the README lists them. `W0` comes first because every other check needs it.

| ID | Check | Proves, in the README's Platform support table | Run | Real sessions |
|---|---|---|---|---|
| W0 | Install and the temporary home | Install script; `hand init` and the Luvus pin; Download the tested Luvus | agent, then needs human | 0 |
| W1 | The supervisor starts under the pseudo console, and Hand accepts the trust screen | Start claude, and accept its trust screen; Luvus server, panes and events | agent | 1 |
| W2 | **Start** on the board runs the supervisor | Luvus server, panes and events | needs human | 1 |
| W3 | A worker reports, from PowerShell and Git Bash, and the supervisor wakes | A worker reports through the footer | agent | 1 |
| W4 | codex through its npm shim, agy and opencode start | Start codex or agy; Start opencode | agent, per harness | 1 each |
| W5 | `hand update` renames the running `hand.exe` | `hand update` | agent, then needs human | 0 |
| W6 | `hand open` starts the board, and no console window flashes | `hand open` starts the board and opens the browser | needs human | 0 |
| W7 | `hand attach supervisor` in Windows Terminal | `hand attach` | needs human | 0 |
| W8 | Stop and clean a worker that has a child process | Stop a worker | agent | 1 |
| W9 | A worker commits in a worktree with paths over 260 characters | Paths over 260 characters | agent | 1 |
| W10 | The Startup shortcut starts the board after a logon | Start the board at login | agent, then needs human | 0 (a resume) |
| W11 | A folder name with a space and a non-ASCII letter | (the README's open item on user profile folders) | agent | 1 |
| W12 | The toast recipe shows a toast | Desktop notifications | needs human | 0 |
| W13 | The ssh note: the `ssh-agent` service | (the README's note on signing and ssh) | agent | 0 |
| W14 | The watcher starts a killed Luvus server again | The watcher starts a killed Luvus server again | agent | 0 |

The checks hand the supervisor on to each other, and each says whether the supervisor must be running or stopped. A supervisor that runs spends nothing while it is idle.

## W0: Install and the temporary home

- **Proves:** the Install script row, the `hand init` and the Luvus pin row, and the Download the tested Luvus row.
- **Run:** agent, then needs human (one question).
- **Real sessions:** 0.
- **Preconditions:** Windows 10 or 11 amd64, no admin rights. `git`. In run B, a `git.exe` without Git Bash. Best: no `luvus.exe` on `PATH`, so that `hand init` has to download it (step 2 shows whether there is one).

Steps:

**1.** Record the versions for the run issue:

```powershell
$PSVersionTable.PSVersion.ToString(); (Get-CimInstance Win32_OperatingSystem).Caption; [Environment]::OSVersion.VersionString; $env:PROCESSOR_ARCHITECTURE
git --version; claude --version
```

**2.** Make the temporary folder. Run B also needs MinGit: see step 4.

```powershell
$D = Join-Path $env:TEMP ('hd-' + [guid]::NewGuid().ToString('N').Substring(0, 6))
New-Item -ItemType Directory -Path "$D\bin", "$D\fleet", "$D\repo", "$D\evidence" | Out-Null
$env:SECONDHAND_HOME = "$D\home"; $env:LUVUS_HOME = "$D\luvus"
Get-Command luvus -All -ErrorAction SilentlyContinue | Select-Object Source
```

The last command should print nothing. If it prints a path, `hand init` will pin that copy instead of downloading one, and the download is not tested: write `download not tested, luvus.exe on PATH` in the run issue, and go on. Do not change your `PATH` to hide it, because its folder may also hold tools that you need.

**3.** Write the environment file, and load it. Load it again at the start of every new shell. `Redact` removes your names, and `Ev ID` redacts and saves what a command prints to `$D\evidence\ID.txt`:

```powershell
@"
`$D = '$D'
`$env:SECONDHAND_HOME = "`$D\home"
`$env:LUVUS_HOME = "`$D\luvus"
`$env:Path = "`$D\bin;`$env:Path"
if (Test-Path "`$D\vars.ps1") { . "`$D\vars.ps1" }
function Redact { process { ("`$_" -replace [regex]::Escape(`$env:USERPROFILE), '<home>' -replace [regex]::Escape(`$env:USERNAME), '<user>' -replace [regex]::Escape(`$env:COMPUTERNAME), '<machine>' -replace 'token=[A-Za-z0-9._~-]+', 'token=<token>' -replace 'gh[pousr]_[A-Za-z0-9]{20,}', '<token>') } }
function Ev([string]`$Id) { `$input | Redact | Tee-Object -FilePath "`$D\evidence\`$Id.txt" -Append }
function Find-Ping { Get-CimInstance Win32_Process -Filter "Name = 'ping.exe'" | Where-Object { `$_.CommandLine -like '*-n 1234*' } | Select-Object ProcessId, ParentProcessId }
"@ | Set-Content -Path "$D\env.ps1"
. "$D\env.ps1"
```

A user name that is a common word also changes that word in the output: read the redacted text before you post it.

Many agent harnesses start a new shell for every command, so a value that a step sets (`$t`, `$a`, `$cur`, `$wt`, `$deep`) is also appended to `$D\vars.ps1`, which `env.ps1` loads. Start every block, and every Evidence command, with `. "$D\env.ps1"; Set-Location "$D\fleet"`.

**4.** **Run B only:** take Git Bash out of every shell that loads `env.ps1`. Unzip a MinGit 64-bit zip (not the busybox one; it comes from the Git for Windows project's release page) into `$D\mingit`, then (`unverified`: it assumes that MinGit has no `bash.exe`):

```powershell
Add-Content "$D\env.ps1" @"
`$env:Path = ((`$env:Path.Split(';') | Where-Object { `$_ -notmatch '\\Git\\(cmd|bin|usr\\bin|mingw64\\bin)\\?$' }) -join ';')
`$env:Path = "`$D\mingit\cmd;`$env:Path"
Remove-Item Env:CLAUDE_CODE_GIT_BASH_PATH -ErrorAction SilentlyContinue
"@
. "$D\env.ps1"
(Get-Command git).Source
Get-Command bash -All -ErrorAction SilentlyContinue | Select-Object Source
```

`git` must be under `$D\mingit`, and `bash` must print nothing, or only `C:\Windows\System32\bash.exe`, which is WSL's and not Git Bash.

**5.** Install the latest release into the folder. `install.ps1` also adds that folder to your user `PATH` for good: the Teardown removes it. Put the folder first in this shell, so that this copy is the one that runs:

```powershell
$env:HAND_INSTALL_DIR = "$D\bin"
irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1 | iex
Remove-Item Env:HAND_INSTALL_DIR
$env:Path = "$D\bin;$env:Path"
(Get-Command hand).Source; hand version
```

**6.** Make the throw-away repository. Its local git settings keep your signing configuration out of it:

```powershell
git init -q -b main "$D\repo"
git -C "$D\repo" config user.name dogfood
git -C "$D\repo" config user.email dogfood@example.invalid
git -C "$D\repo" config commit.gpgsign false
Set-Content -Path "$D\repo\README.md" -Value hello
git -C "$D\repo" add README.md
git -C "$D\repo" commit -q -m init
```

**7.** Make the fleet, register the repository, and take the backup of `hand.db`. A profile named `cheap` uses the cheapest model and effort; change `haiku` if `hand route list` shows it as not valid for you:

```powershell
Set-Location "$D\fleet"
hand init
hand project add demo "$D\repo"
Copy-Item "$D\fleet\hand.db" "$D\hand.db.backup"
Set-Content -Path "$D\fleet\routing.json" -Encoding ascii -Value '{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}}}'
hand route list | Select-Object -First 3
$fid = (Get-ChildItem "$env:SECONDHAND_HOME\fleets").Name
$luvus = (hand luvus show | Select-String '^pin: \S+ (.*)$').Matches[0].Groups[1].Value
Add-Content "$D\env.ps1" "`$fid = '$fid'`n`$luvus = '$luvus'"
. "$D\env.ps1"; hand fleet list
```

**8.** Ask the owner: *While you ran `install.ps1` and `hand`, did SmartScreen, Defender or Windows Security show any prompt, warning or notification about `hand.exe` or `luvus.exe`?* (yes or no, and its text).

Expected:
- the install prints `installed hand version: 0.11.0 to ...\bin\hand.exe` (the version can be newer), and `(Get-Command hand).Source` is that path;
- `hand init` prints `created: true` and a `luvus: pinned 0.14.3 (...)` line: when no `luvus.exe` was on `PATH`, it downloaded and pinned the tested Luvus, and the pinned file is under `$D\home\luvus`;
- `hand fleet list` shows exactly one fleet, whose home is under the temporary folder, with state `ok`;
- `hand route list` shows `cheap,claude,haiku,low,yes`;
- the answer is no.

Evidence: `& { hand version; hand fleet list; hand luvus show } 2>&1 | Ev W0`, and the answer.

If it fails: file it with the failing step and its output. If `W0` fails, stop: the other checks need it.

## W1: The supervisor starts under the pseudo console, and Hand accepts the trust screen

- **Proves:** the Start claude, and accept its trust screen row, and the Luvus server, panes and events row, over a named pipe.
- **Run:** agent.
- **Real sessions:** 1 (the supervisor).
- **Preconditions:** `W0` passed. `claude` is installed and logged in; otherwise skip `W1` to `W3`, `W8`, `W9`, `W11` and `W14`. No supervisor runs.

Steps:

**1.** Start it, and give it half a minute:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor start --profile cheap
Start-Sleep -Seconds 30
hand supervisor show
hand orient | Select-Object -First 5
hand luvus show
Get-ChildItem "$env:LUVUS_HOME\sessions\secondhand-$fid"
& $luvus session list --json
```

Expected:
- `hand supervisor start` prints `supervisor: s1`, `status: running`, `trust: accepted` and `prompt: submitted` (`trust: not asked` is also fine if Claude Code did not ask);
- `hand supervisor show` prints an `agent:` of `idle`, `working` or `done`, never `blocked`;
- `hand orient` prints `supervisor: s1 claude running` and `watch: running`;
- `hand luvus show` prints `match: yes`;
- `luvus session list --json` lists `secondhand-<fleet id>` with `"transport": "windows_named_pipe"` and `"running": true`. The session folder may not be under `$env:LUVUS_HOME`: `LUVUS_HOME` on Windows is `unverified`, and then the Teardown must stop the session by name.

Leave the supervisor running for `W3`.

Evidence: `& { hand supervisor show; hand orient | Select-Object -First 5; hand luvus show; & $luvus session list --json } 2>&1 | Ev W1`.

If it fails: file the output above and the start command's output. If `status` is not `running`, or `trust` is `not pressed`, add `hand supervisor show`. If it is run B, say whether Claude Code started at all, and its message.

## W2: **Start** on the board runs the supervisor

- **Proves:** the Luvus server, panes and events row, through the board.
- **Run:** needs human.
- **Real sessions:** 1 (the supervisor).
- **Preconditions:** `W0` passed. Run A only.

Steps:

**1.** Stop the supervisor that `W1` left, and start a board for this home. If something already listens on port 7777 (probably your real board), start this one on another port; do not stop yours:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor stop
if (Get-NetTCPConnection -LocalPort 7777 -State Listen -ErrorAction SilentlyContinue) { Start-Process -FilePath (Get-Command hand).Source -ArgumentList 'board', '--addr', '127.0.0.1:7790' -WindowStyle Hidden -RedirectStandardOutput "$D\board.out" -RedirectStandardError "$D\board.err"; Start-Sleep -Seconds 3 }
hand open --print
```

The last command prints a link that holds this fleet's token. Show it to the owner in the terminal only, and never put it in an issue.

**2.** Ask the owner to open that link in a browser, and press **Start** on the fleet page. The profile is `cheap`. Then ask: *Within one minute, did the page show the supervisor as running, with no error banner?* (yes or no).

**3.** Check from the shell:

```powershell
Start-Sleep -Seconds 20; hand supervisor show; hand orient | Select-Object -First 5
```

Expected: the answer is yes, `hand supervisor show` prints `status: running` and an `agent:` that is not `blocked`, and `hand orient` prints `supervisor: s2 claude running` and `watch: running`.

Leave the supervisor running, and the board.

Evidence: `& { hand supervisor show; hand orient | Select-Object -First 5 } 2>&1 | Ev W2`, and the answer. When it fails, add the board's log: `Get-Content "$D\home\board.log", "$D\board.err" -Tail 20 -ErrorAction SilentlyContinue | Ev W2`.

If it fails: file what the owner saw (the text of any banner), the output above, and the log.

## W3: A worker reports, from PowerShell and Git Bash, and the supervisor wakes

- **Proves:** the A worker reports through the footer row, and the wake.
- **Run:** agent.
- **Real sessions:** 1 (the worker), and one short turn of the supervisor when it wakes.
- **Preconditions:** `W1` or `W2` passed, and the supervisor runs. The supervisor may act on the report (acknowledge it, mark the task done): that is expected. Do not answer it. For the Git Bash half: run A, and `bash.exe` of Git for Windows exists.

Steps:

**1.** Write a one-line task and start a worker on it:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
Set-Content -Path "$D\brief.md" -Value @'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
'@
$t = (hand task add --goal "Add hello.txt" demo "dogfood hello" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand task start $t
$cur = (hand orient | Select-String '^cursor: (\d+)').Matches[0].Groups[1].Value
$out = hand attempt start --profile cheap --prompt-file "$D\brief.md" $t
$out
$a = ($out | Select-String '^attempt: (\S+)').Matches[0].Groups[1].Value
$wt = ($out | Select-String '^worktree: (.*)$').Matches[0].Groups[1].Value
Add-Content "$D\vars.ps1" "`$t = '$t'`n`$a = '$a'`n`$cur = '$cur'`n`$wt = '$wt'"
```

**2.** While the worker runs, report by hand from inside its worktree, from PowerShell and from Git Bash, the way the footer tells a Windows worker to. These are `progress` reports, so they wake nobody. Find Git Bash first (not WSL's `bash.exe`):

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
Set-Content "$D\r-ps.md" "report from PowerShell"
Set-Content "$D\r-gb.md" "report from Git Bash"
Push-Location $wt; hand report add --status progress --file "$D\r-ps.md"; Pop-Location
$bash = @("$env:ProgramFiles\Git\bin\bash.exe", "${env:ProgramFiles(x86)}\Git\bin\bash.exe", "$env:LOCALAPPDATA\Programs\Git\bin\bash.exe") | Where-Object { Test-Path $_ } | Select-Object -First 1
$wtf = $wt -replace '\\', '/'; $df = $D -replace '\\', '/'
if ($bash) { & $bash -c "cd '$wtf' && hand report add --status progress --file '$df/r-gb.md'" }
```

**3.** Wait for the worker's own report. This blocks until an event arrives; run it again with the cursor it prints, at most three times, until a row has the kind `attempt.reported` with detail `done`:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand wait --after $cur --timeout 8m
```

**4.** Read the result:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand report list
hand attempt show $a
git -C "$D\repo" log --oneline "hand/$fid/$t-$a"
hand orient | Select-String -Pattern '^recent' -Context 0, 12
```

Expected:
- `hand attempt start` prints `status: running` and `trust: accepted`;
- both hand reports are accepted (each prints a report ref, with no error), and the list shows them with status `progress`; no console window or error comes from the Git Bash one;
- `hand wait` returns a row `attempt.reported` within 8 minutes, in at most three waits, and `hand report list` has a report with status `done` for the attempt;
- the branch `hand/<fleet id>/<task>-<attempt>` holds a commit `dogfood` over `init`;
- `hand orient` lists `supervisor.delivered` after `attempt.reported`, which is the wake reaching the supervisor (the event name is read from the code: if only that row is missing, say so and add `hand supervisor show`).

Evidence: `& { hand report list; hand attempt show $a; git -C "$D\repo" log --oneline "hand/$fid/$t-$a" } 2>&1 | Ev W3`.

If it fails: file the output above. File the Git Bash half as its own issue (`W3: report from Git Bash ...`) when only it fails. If `trust` is `not pressed` or the attempt is `blocked`, add `hand attempt read $a`, which prints the worker's screen. If the worker finished but wrote no report, add the `hand attempt read $a` output: the Windows footer must reach it.

## W4: codex through its npm shim, agy and opencode start

- **Proves:** the Start codex or agy row, and the Start opencode row. The codex check includes the `.cmd` shim that `npm` makes for it.
- **Run:** agent, once per harness. Skip a harness that is not installed, or that you have no account for, with the reason.
- **Real sessions:** 1 for each harness that runs.
- **Preconditions:** `W0` passed. Run `hand supervisor stop` first. Run codex once yourself before you use it, so that its model cache exists. For agy, `agy models` must list models. opencode uses the model from its own configuration. `Get-Command codex` should name a `codex.cmd`: say what it names in the evidence.

Steps (repeat for each harness, with its own `$h`, `$model` and `$effort` from the cheapest row of `hand route list` under `models`):

**1.** List what is valid for you, and pick the cheapest model and effort:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor stop
hand route list
Get-Command codex, agy, opencode -ErrorAction SilentlyContinue | Select-Object Name, Source
Add-Content "$D\vars.ps1" "`$h = '<harness>'`n`$model = '<model>'`n`$effort = '<effort>'"
```

Write the real harness, model and effort into that last line (for agy, leave `$effort` empty), by editing `$D\vars.ps1`.

**2.** Write the brief, and start one worker on it. For codex and agy:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
Set-Content -Path "$D\brief.md" -Value @'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
'@
$t = (hand task add --goal "Add hello.txt" demo "dogfood $h" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand task start $t
$a = (hand attempt start --harness $h --model $model --effort $effort --prompt-file "$D\brief.md" $t | Select-String '^attempt: (\S+)').Matches[0].Groups[1].Value
Add-Content "$D\vars.ps1" "`$t = '$t'`n`$a = '$a'"
```

For agy, leave out `--effort`. For opencode: `hand attempt start --harness opencode --prompt-file "$D\brief.md" $t`.

**3.** Wait up to eight minutes for the report, then read it:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand wait --timeout 8m
hand attempt show $a
hand report list
```

**4.** Stop and clean the worker: `. "$D\env.ps1"; Set-Location "$D\fleet"; hand attempt stop $a; hand attempt clean $a`.

Expected: the attempt is `running` right after the start, and its `launch:` line shows the harness's own flags (see README, The supervisor); the worker is not left `blocked` on a trust or permission screen that Hand should have handled; a report with status `done` arrives. If the harness asks something Hand does not answer, the attempt shows `blocked_on`: that is a failure. For codex, a quoting error from `cmd.exe` in the `launch:` line or on the screen is the failure to look for.

Evidence: `& { hand attempt show $a; hand report list; Get-Command $h | Select-Object Name, Source } 2>&1 | Ev W4`.

If it fails: file one issue per harness, titled `W4: <harness> ...`, with `hand attempt show $a` and `hand attempt read $a`.

## W5: `hand update` renames the running `hand.exe`

- **Proves:** the `hand update` row.
- **Run:** agent, then needs human (one question).
- **Real sessions:** 0.
- **Preconditions:** `W0` passed. Nothing listens on port 7777 (the restarted board uses it): `Get-NetTCPConnection -LocalPort 7777 -State Listen -ErrorAction SilentlyContinue` prints nothing, and no board of `$D\home` runs. Otherwise `skip` with `port 7777 busy`. This check uses its own home, `$D\home2`, and its own copy of the binary in `$D\old`: it never updates the copy in `$D\bin` or any other `hand`. The install adds `$D\old` to your user `PATH`: the Teardown removes it.

Steps:

**1.** Install v0.11.0 or a later release as a second copy (the update stops processes by pid file from v0.11.0 on), make a fleet in a second home, and start its watcher and board with that old copy:

```powershell
. "$D\env.ps1"
New-Item -ItemType Directory -Path "$D\fleet2" | Out-Null
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/atqamz/hand/main/install.ps1))) -Dir "$D\old" -Version v0.11.0
$env:SECONDHAND_HOME = "$D\home2"
Set-Location "$D\fleet2"
& "$D\old\hand.exe" init
Copy-Item "$D\fleet2\hand.db" "$D\hand2.db.backup"
Start-Process -FilePath "$D\old\hand.exe" -ArgumentList 'watch' -WorkingDirectory "$D\fleet2" -WindowStyle Hidden -RedirectStandardOutput "$D\watch2.out" -RedirectStandardError "$D\watch2.err"
Start-Process -FilePath "$D\old\hand.exe" -ArgumentList 'board' -WorkingDirectory "$D\fleet2" -WindowStyle Hidden -RedirectStandardOutput "$D\board2.out" -RedirectStandardError "$D\board2.err"
Start-Sleep -Seconds 5
Get-Content "$D\fleet2\watch.pid", "$env:SECONDHAND_HOME\board.pid"
& "$D\old\hand.exe" update --channel edge --check
```

**2.** Update to the rolling build, and look at the result:

```powershell
. "$D\env.ps1"; $env:SECONDHAND_HOME = "$D\home2"; Set-Location "$D\fleet2"
& "$D\old\hand.exe" update --channel edge
Test-Path "$D\old\hand.exe.old"
& "$D\old\hand.exe" version
Test-Path "$D\old\hand.exe.old"
Get-Content "$D\fleet2\watch.pid", "$env:SECONDHAND_HOME\board.pid"
Start-Sleep -Seconds 5
& "$D\old\hand.exe" orient | Select-String '^watch:'
& "$D\old\hand.exe" open --print *> $null; if ($LASTEXITCODE -eq 0) { 'board-answers' }
```

**3.** Ask the owner: *During the install and the update, did SmartScreen, Defender or Windows Security show any prompt, warning or notification about `hand.exe`?* (yes or no, and its text).

Expected:
- the check prints `status: checked`; the update prints `status: updated`, `from: 0.11.0 stable ...` and `to: 0.11.0 edge ...` (`to` can be newer, and `from` is the release you installed), a `backups` list, and `init ok` for the fleet;
- the first `Test-Path` after the update prints `True` (the running `hand.exe` was renamed to `hand.exe.old`), and the second, after one more `hand` run, prints `False`;
- `hand version` prints `channel: edge`;
- both PID files changed, `orient` prints `watch: running`, and `board-answers` prints: the watcher and the board run again, started by the update;
- the answer is no.

Evidence: `& { & "$D\old\hand.exe" version; Get-Content "$D\fleet2\watch.pid", "$env:SECONDHAND_HOME\board.pid"; Get-Content "$D\watch2.err", "$D\board2.err" -Tail 5 } 2>&1 | Ev W5`, the update's output, and the answer.

If it fails: file the update's output. The journal `$env:SECONDHAND_HOME\update.json` (if it exists) belongs in the evidence. If the binary was replaced but the board or the watcher did not return, say which. If `hand.exe.old` is never deleted, say so.

The next block of the next check loads `env.ps1` again, which sets the home back.

## W6: `hand open` starts the board, and no console window flashes

- **Proves:** the `hand open` starts the board and opens the browser row.
- **Run:** needs human. The owner watches the screen while the agent runs the steps.
- **Real sessions:** 0.
- **Preconditions:** `W1` or `W2` passed, and the supervisor runs, so that the board has a fleet to keep. No board of this home runs: stop one that you started, with `Stop-Process -Id ([int](Get-Content "$env:SECONDHAND_HOME\board.pid").Split(' ')[0])`. Nothing listens on port 7777; otherwise `skip` with `port 7777 busy`, and do not stop what listens. Run A only.

Steps:

**1.** Ask the owner to watch the whole screen for the next two minutes, and to tell you if a console window (black, `cmd` or PowerShell) appears, even for an instant. Then run:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand open
Start-Sleep -Seconds 5
Get-Content "$env:SECONDHAND_HOME\board.addr"
Stop-Process -Id ([int](Get-Content "$D\fleet\watch.pid").Split(' ')[0])
Start-Sleep -Seconds 60
hand orient | Select-String '^watch:'
```

The kill makes the board start the watcher again within 30 seconds, which starts Luvus and git commands too: that is the activity to watch.

**2.** Ask the owner two questions: *Did your default browser open the Hand fleet page, already logged in (no question about a token)?* and *In those two minutes, did any console window appear, even briefly?* (yes or no for each, and which browser).

Expected: `hand open` prints `opened: "http://127.0.0.1:7777/<fleet id>/"` (the URL is in quotes); `board.addr` holds `127.0.0.1:7777`; `watch: running` returns within 60 seconds; the first answer is yes and the second is no.

Evidence: `& { hand open; Get-Content "$env:SECONDHAND_HOME\board.addr"; hand orient | Select-String '^watch:'; Get-Content "$env:SECONDHAND_HOME\board.log" -Tail 5 -ErrorAction SilentlyContinue } 2>&1 | Ev W6`, and both answers.

If it fails: file the output above and what the owner saw. A console window that flashes is a failure, even when everything else works: say which step it came with.

## W7: `hand attach supervisor` in Windows Terminal

- **Proves:** the `hand attach` row, the one that is `not run` on Windows.
- **Run:** needs human. The owner opens a real Windows Terminal window; the agent cannot attach.
- **Real sessions:** 0 (a resume).
- **Preconditions:** `W0` passed. A supervisor runs: `hand supervisor resume`, or `hand supervisor start --profile cheap`. Run A only.

Steps:

**1.** Print the lines that the owner will run, with the real path, and give them to them exactly:

```powershell
. "$D\env.ps1"
Write-Output ". `"$D\env.ps1`"; Set-Location `"$D\fleet`"; hand attach supervisor"
```

Tell the owner to open Windows Terminal (PowerShell) and paste that line.

**2.** Ask the owner: *Did the supervisor's Claude Code screen appear in that tab, and when you typed `x`, did it show in the input box?* (yes or no). Then ask them to delete the `x` and leave: close that tab. (The key that detaches from a Luvus pane is not documented in this repository: unverified.)

**3.** Check that attaching did not harm it:

```powershell
hand supervisor show
```

Expected: the answer is yes, and the supervisor is still `running` afterwards.

Evidence: the answer, and `hand supervisor show | Ev W7`.

If it fails: file what the owner saw, and what `hand attach supervisor` printed (for example `attach needs a terminal`).

## W8: Stop and clean a worker that has a child process

- **Proves:** the Stop a worker row, with a real harness.
- **Run:** agent.
- **Real sessions:** 1 (the worker).
- **Preconditions:** `W0` passed and `claude` works. Run `hand supervisor stop` first, so that the supervisor does not react to the worker.

Steps:

**1.** Start a worker that runs a long command, so that a child process exists. The `ping` runs for about 20 minutes and has its working folder in the worktree, which would block `clean` if it survived:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor stop
Set-Content -Path "$D\brief8.md" -Value @'
Run exactly this command and wait for it to finish: ping -n 1234 127.0.0.1
Do nothing else.
'@
$t = (hand task add --goal "Hold a child process" demo "dogfood child" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand task start $t
$a = (hand attempt start --profile cheap --prompt-file "$D\brief8.md" $t | Select-String '^attempt: (\S+)').Matches[0].Groups[1].Value
Add-Content "$D\vars.ps1" "`$t = '$t'`n`$a = '$a'"
foreach ($i in 1..24) { if (Find-Ping) { break }; Start-Sleep -Seconds 5 }
Find-Ping
```

If no `ping.exe` appears within two minutes, the worker did not run it: run the step once more with a new task; if it fails again, mark `W8` `skip` with that reason.

**2.** Stop the worker, and look for the child:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand attempt stop $a
Start-Sleep -Seconds 3
if (Find-Ping) { 'child left' } else { 'child gone' }
hand attempt show $a
```

**3.** Clean it:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand attempt clean $a
Get-ChildItem "$env:SECONDHAND_HOME\worktrees\$fid" -ErrorAction SilentlyContinue
git -C "$D\repo" branch --list "hand/$fid/*"
```

Expected:
- `hand attempt stop` prints `status: stopped` and `reason: stopped by operator`;
- `child gone` prints: no `ping.exe` of the worker is left (the whole process tree ended, with no graceful phase);
- `hand attempt clean` prints `removed:` with the worktree path, with no "in use" or "access denied" error, and the folder no longer exists;
- the branch `hand/<fleet id>/<task>-<attempt>` is kept.

Evidence: `& { hand attempt show $a; Get-ChildItem "$env:SECONDHAND_HOME\worktrees\$fid" -ErrorAction SilentlyContinue; git -C "$D\repo" branch --list "hand/$fid/*" } 2>&1 | Ev W8`.

If it fails: file the failing step's output. If a process is left, add `Get-CimInstance Win32_Process -Filter "ProcessId = <pid>" | Select-Object ProcessId, ParentProcessId, Name`. If `clean` fails, add its message in full.

## W9: A worker commits in a worktree with paths over 260 characters

- **Proves:** the Paths over 260 characters row, for a worker's git, which is `not run`.
- **Run:** agent.
- **Real sessions:** 1 (the worker).
- **Preconditions:** `W0` passed and `claude` works. Do not set `git config --global core.longpaths`: Hand's own git and the worker's inherit it from Hand, and that is what is tested. Run `hand supervisor stop` first.

Steps:

**1.** Put a file with a long path into the throw-away repository, using git's plumbing so that nothing is checked out in it. Each folder name is 61 characters; the relative path is 256, and with the worktree's folder the whole path is over 260:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor stop
$deep = ((1..4 | ForEach-Object { ('d' * 60) + $_ }) -join '/') + '/deep.txt'
$blob = 'one' | git -C "$D\repo" hash-object -w --stdin
git -C "$D\repo" update-index --add --cacheinfo "100644,$blob,$deep"
git -C "$D\repo" commit -q -m deep
$deep.Length
Add-Content "$D\vars.ps1" "`$deep = '$deep'"
```

**2.** Start a worker that edits the file and commits:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
Set-Content -Path "$D\brief9.md" -Value @"
Append the line changed to the file $deep (the path is relative to your worktree root), then run git add -A and git commit -m long. If your file tool refuses the path as too long, use git, or PowerShell with a path that starts with \\?\. Do nothing else.
"@
$t = (hand task add --goal "Edit a deep file" demo "dogfood long path" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand task start $t
$out = hand attempt start --profile cheap --prompt-file "$D\brief9.md" $t
$a = ($out | Select-String '^attempt: (\S+)').Matches[0].Groups[1].Value
Add-Content "$D\vars.ps1" "`$t = '$t'`n`$a = '$a'"
($out | Select-String '^worktree: (.*)$').Matches[0].Groups[1].Value.Length + $deep.Length + 1
hand wait --timeout 8m
```

**3.** Read the result:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand attempt show $a
git -C "$D\repo" -c core.longpaths=true show --stat --format=%s "hand/$fid/$t-$a"
```

Expected:
- the length printed in step 2 is over 260;
- the branch `hand/<fleet id>/<task>-<attempt>` has a commit `long` that changes the deep file;
- no "Filename too long" error appears on the worker's screen or in the attempt.

Evidence: `& { hand attempt show $a; git -C "$D\repo" -c core.longpaths=true show --stat --format=%s "hand/$fid/$t-$a" } 2>&1 | Ev W9`.

If it fails: file the output above and `hand attempt read $a`. Say whether the failure came from git or from the worker's file tool: only a git failure is Hand's.

## W10: The Startup shortcut starts the board after a logon

- **Proves:** the Start the board at login row.
- **Run:** agent, then needs human (logoff and logon).
- **Real sessions:** 0 (a resume).
- **Preconditions:** `W0` passed. A supervisor runs (`hand supervisor resume`). This check writes one file outside the temporary folder, `Hand dogfood board.lnk` in your Startup folder, which it removes; its name is not `Hand board.lnk`, so it cannot overwrite your own. Run A only.

The shortcut differs from the README's recipe in one way: a shortcut cannot set environment variables, so it starts `cmd.exe` to set `SECONDHAND_HOME` and `LUVUS_HOME` for this temporary home first. A shortcut that ran `hand.exe` directly would use your real home.

Steps:

**1.** Check that the shortcut's name is free. The command must print `False`; if it prints `True`, skip `W10` with the reason `shortcut exists`, and do not overwrite or remove that file:

```powershell
Test-Path "$([Environment]::GetFolderPath('Startup'))\Hand dogfood board.lnk"
```

**2.** Make the shortcut, and make the board resume the supervisor by itself:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
$lnk = "$([Environment]::GetFolderPath('Startup'))\Hand dogfood board.lnk"
$link = (New-Object -ComObject WScript.Shell).CreateShortcut($lnk)
$link.TargetPath = "$env:SystemRoot\System32\cmd.exe"
$link.Arguments = "/c set `"SECONDHAND_HOME=$D\home`"&& set `"LUVUS_HOME=$D\luvus`"&& `"$D\bin\hand.exe`" board --addr 127.0.0.1:7790"
$link.WindowStyle = 7
$link.Save()
Test-Path $lnk
Set-Content -Path "$D\fleet\routing.json" -Encoding ascii -Value '{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}},"supervisor":{"autoresume":true}}'
```

`Test-Path` must print `True`. The `cmd.exe` wrapper and its quoting are `unverified`.

**3.** Print the line that the owner will run, with the real path, and give it to them exactly:

```powershell
. "$D\env.ps1"
Write-Output ". `"$D\env.ps1`"; Set-Location `"$D\fleet`"; hand orient | Select-Object -First 5; hand open --print *> `$null; if (`$LASTEXITCODE -eq 0) { 'board-answers' }"
```

Ask the owner: *Log off from Windows, log on again, wait two minutes, then open PowerShell and run that line. Paste the output here. Did it print `board-answers`, `watch: running`, and a supervisor line that says `running` (resumed by the board's watcher) or `interrupted` (then run `hand supervisor resume`, and say whether it says `running` afterwards)?* (yes or no, and the output).

Logging off ends this session, and the supervisor. An agent cannot finish this step: it leaves the check `waiting for owner`, and the owner runs the Teardown after they answer.

Expected: after the logon the board runs (it was started by the shortcut, minimized), the board starts the watcher (`watch: running`), and the supervisor is running again, or `hand supervisor resume` makes it run and continue its session; the answer is yes.

Evidence: the owner's output, redacted with `Redact`; and `Get-Content "$D\home\board.log" -Tail 10 -ErrorAction SilentlyContinue | Ev W10`.

If it fails: file what happened after the logon, step by step, with the output above.

## W11: A folder name with a space and a non-ASCII letter

- **Proves:** the README's open item, that a user profile folder with a space or a non-ASCII character is untested. Your own profile folder is the real test; if its name has a space or a non-ASCII character, say so in the run issue, and `W0` already covers it. This check puts a folder with both into the temporary folder, which tests the same paths in Hand's state, worktrees and Luvus pin, though not Claude Code's own use of your profile.
- **Run:** agent.
- **Real sessions:** 1 (the worker).
- **Preconditions:** `W0` passed, and `claude` works.

Steps:

Run steps 1 and 2 in one shell: step 2 uses what step 1 sets.

**1.** Build a second temporary folder `$D2` with the letters made from their code points, so that no console encoding matters, and set it up like `W0`, steps 2, 6 and 7, with `$D2` in place of `$D`: its own `SECONDHAND_HOME` and `LUVUS_HOME`, repository, fleet and `routing.json`. Do not install again: use `hand` from `$D\bin`:

```powershell
. "$D\env.ps1"
$D2 = Join-Path $D ('Hand D' + [char]0xF6 + 'gfood ' + [char]0xDC)
New-Item -ItemType Directory -Path "$D2\fleet", "$D2\repo" | Out-Null
$env:SECONDHAND_HOME = "$D2\home"; $env:LUVUS_HOME = "$D2\luvus"
git init -q -b main "$D2\repo"
git -C "$D2\repo" config user.name dogfood
git -C "$D2\repo" config user.email dogfood@example.invalid
git -C "$D2\repo" config commit.gpgsign false
Set-Content -Path "$D2\repo\README.md" -Value hello
git -C "$D2\repo" add README.md
git -C "$D2\repo" commit -q -m init
Set-Location "$D2\fleet"
hand init
hand project add demo "$D2\repo"
Set-Content -Path "$D2\fleet\routing.json" -Encoding ascii -Value '{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}}}'
```

**2.** Run a worker to its report, as `W3` steps 1 and 3 do, with `$D2` in place of `$D` and the new fleet. Stop and clean it, then stop the second session:

```powershell
$fid2 = (Get-ChildItem "$env:SECONDHAND_HOME\fleets").Name
Set-Content -Path "$D2\brief.md" -Value @'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
'@
$t = (hand task add --goal "Add hello.txt" demo "dogfood space" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand task start $t
$a = (hand attempt start --profile cheap --prompt-file "$D2\brief.md" $t | Select-String '^attempt: (\S+)').Matches[0].Groups[1].Value
hand wait --timeout 8m
hand report list
hand attempt stop $a
hand attempt clean $a
& $luvus session stop "secondhand-$fid2"
```

Then load `env.ps1` again, for the checks that follow.

Expected: every command works with the folder `Hand Dögfood Ü` in its paths: `hand init` and `hand project add` succeed, the attempt reaches `trust: accepted`, a `done` report arrives, and `hand attempt clean` removes the worktree.

Evidence: `& { hand report list; hand fleet list } 2>&1 | Ev W11` (run with the `$D2` home set, before you reload `env.ps1`).

If it fails: file the failing command and its output, and say which path contained the space or the letter in the error text.

## W12: The toast recipe shows a toast

- **Proves:** the Desktop notifications row (`no` in the table), through the README's toast recipe.
- **Run:** needs human.
- **Real sessions:** 0.
- **Preconditions:** `W0` passed. Windows may suppress toasts while Focus assist or Do not disturb is on: turn it off for this check.

Steps:

**1.** Stop the supervisor and its watcher, so that the watcher you start reads the recipe, then put the recipe into the throw-away `routing.json`. The line comes from this repository's README, section "Alerts on your phone", so that it is the one that README documents (the check fails with a message when that line is not found). `$rev` is the docs revision that an agent recorded in `vars.ps1`, so that the recipe is the one of the guide it follows, and `main` when none was recorded:

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
hand supervisor stop
if (Test-Path "$D\fleet\watch.pid") { $p = [int](Get-Content "$D\fleet\watch.pid").Split(' ')[0]; if ((Get-Process -Id $p -ErrorAction SilentlyContinue).ProcessName -eq 'hand') { Stop-Process -Id $p } }
$rev = if ($docsRev) { $docsRev } else { 'main' }
$readme = irm "https://raw.githubusercontent.com/atqamz/hand/$rev/README.md"
$line = ($readme -split "`n" | ForEach-Object { $_.Trim() } | Where-Object { $_.StartsWith('"notify": ["powershell.exe"') })[0]
if (-not $line) { throw 'no toast recipe found in the README' }
Set-Content -Path "$D\fleet\routing.json" -Encoding ascii -Value ('{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}},' + $line + '}')
hand route list | Select-Object -First 2
```

**2.** Run the watcher, which reads the recipe, and ask a decision, which it alerts on:

```powershell
$w = Start-Process -FilePath (Get-Command hand).Source -ArgumentList 'watch' -WorkingDirectory "$D\fleet" -WindowStyle Hidden -RedirectStandardOutput "$D\watch-w12.out" -RedirectStandardError "$D\watch-w12.err" -PassThru
Start-Sleep -Seconds 3
$t = (hand task add --goal "Notify me" demo "dogfood toast" | Select-String '^task: (\S+)').Matches[0].Groups[1].Value
hand decision ask $t "Did the toast show?"
```

`hand watch` exits with status 3 and a message if another watcher still holds this fleet's lock: wait a few seconds, and run it again.

**3.** Ask the owner, within 30 seconds: *Did a Windows toast appear with the title `d1` (the decision's ref, for example `d1`) and a text starting with the task's ref and ` decision:`?* (yes or no, and the text). Then stop the watcher:

```powershell
Stop-Process -Id $w.Id
```

Expected: the answer is yes, and no console window flashed while the command ran (say if one did).

Evidence: the answer, and `Get-Content "$D\watch-w12.err", "$D\watch-w12.out" -Tail 5 -ErrorAction SilentlyContinue | Ev W12`.

If it fails: file what the owner saw, and the watcher's output. The recipe is `unverified`: a toast that never appears is the finding to file, with the Focus assist state.

## W13: The ssh note: the `ssh-agent` service

- **Proves:** the paragraph that starts `Signing and ssh` in the README's Windows section, which the README says nobody has verified on a Windows machine. This check reads three facts and changes nothing; it does not push, and does not test signing, which needs the `ssh-agent` service started with admin rights.
- **Run:** agent.
- **Real sessions:** 0.
- **Preconditions:** none.

Steps:

**1.** Read the facts:

```powershell
Get-Service ssh-agent -ErrorAction SilentlyContinue | Format-List Name, Status, StartType
Get-Command ssh -All | Select-Object Source
git config --global --get core.sshCommand
```

Expected: the `ssh-agent` service exists, with `StartType` `Disabled` (the README says it is disabled by default; any other value means that the note needs a correction, and it is not a failure of Hand); `Get-Command ssh -All` lists Windows' OpenSSH (`C:\Windows\System32\OpenSSH\ssh.exe`) and, with Git for Windows, its own `ssh.exe`; the last command prints a value only if you set one. Pass when the facts match the README's note, and file a documentation issue when they do not. Skip the rest of the note (HTTPS with `gh auth login`, an ssh push) with the reason `needs a remote and admin rights`.

Evidence: `& { Get-Service ssh-agent -ErrorAction SilentlyContinue | Format-List Name, Status, StartType; Get-Command ssh -All | Select-Object Source } 2>&1 | Ev W13`.

If it fails: file it as a documentation issue, with the facts.

## W14: The watcher starts a killed Luvus server again

- **Proves:** the watcher starts a killed Luvus server again row, with a real harness instead of a fake one.
- **Run:** agent.
- **Real sessions:** 0: it resumes the supervisor's session, which spends nothing until a message arrives.
- **Preconditions:** a supervisor runs (`hand supervisor start --profile cheap` if none does), and the watcher runs (`hand orient` prints `watch: running`).

Steps:

**1.** Kill the server, and wait up to two minutes for a new one. The session folder is `$sdir`; if `$env:LUVUS_HOME\sessions\secondhand-$fid` does not exist, take `session_dir` of `secondhand-<fleet id>` from `& $luvus session list --json` (`unverified`):

```powershell
. "$D\env.ps1"; Set-Location "$D\fleet"
$sdir = "$env:LUVUS_HOME\sessions\secondhand-$fid"
Get-Content "$sdir\server.pid"
$old = [int](Get-Content "$sdir\server.pid").Split(' ')[0]
Stop-Process -Id $old -Force
foreach ($i in 1..24) { Start-Sleep -Seconds 5; if ((Test-Path "$sdir\server.pid") -and ([int](Get-Content "$sdir\server.pid").Split(' ')[0] -ne $old)) { break } }
Get-Content "$sdir\server.pid"
Start-Sleep -Seconds 15
hand luvus show
hand supervisor show
hand orient | Select-Object -First 5
```

**2.** Resume the supervisor:

```powershell
hand supervisor resume
Start-Sleep -Seconds 20
hand supervisor show
```

Expected:
- within two minutes `server.pid` names a different process, and `hand luvus show` prints `match: yes`;
- the old supervisor is `interrupted` (its pane died with the server), and `hand orient` says so;
- `hand supervisor resume` prints `status: running`, and the new supervisor's `agent:` is not `blocked`;
- `watch: running` throughout.

Evidence: `& { hand luvus show; hand supervisor show; hand orient | Select-Object -First 5 } 2>&1 | Ev W14`.

If it fails: file the output above, and `Get-Content "$D\fleet\watch.log" -Tail 30 -ErrorAction SilentlyContinue` if it exists. If no new server comes within two minutes, say so, and add `hand luvus show`.

## Teardown

Run it at the end, even after a failure, and again after a logoff. In its first line, write the real path of `D`. It removes the Startup shortcut that `W10` made (and only a shortcut whose arguments name your temporary folder) and the `PATH` entries, stops every process of this run, and deletes the temporary folder. Post your evidence first: the evidence files are in that folder.

```powershell
. "C:\Users\<user>\AppData\Local\Temp\hd-xxxxxx\env.ps1"
$lnk = "$([Environment]::GetFolderPath('Startup'))\Hand dogfood board.lnk"
if ((Test-Path $lnk) -and (New-Object -ComObject WScript.Shell).CreateShortcut($lnk).Arguments -like "*$D*") { Remove-Item $lnk }
foreach ($f in @(Get-ChildItem "$D\fleet*\watch.pid", "$D\home*\board.pid" -ErrorAction SilentlyContinue).FullName) {
  if (Test-Path $f) { $p = [int](Get-Content $f).Split(' ')[0]; $proc = Get-Process -Id $p -ErrorAction SilentlyContinue; if ($proc -and $proc.Path -like "$D\*") { Stop-Process -Id $p -Force } }
}
foreach ($s in @(Get-ChildItem "$D\luvus\sessions" -Directory -ErrorAction SilentlyContinue)) { & $luvus session stop $s.Name }
& $luvus session list --json | Select-String '"running": true'
Get-Process | Where-Object { $_.Path -like "$D\*" } | Select-Object Id, Path
```

The last two commands must print nothing; a `secondhand-` session that still runs belongs to this run, so stop it with `& $luvus session stop <name>`, and a process that runs from `$D` is yours, so stop it by its process ID. Then take the temporary folders out of your user `PATH`, where `install.ps1` put them, and delete the folder:

```powershell
$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
$path = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
$keep = $path.Split(';', [StringSplitOptions]::RemoveEmptyEntries) | Where-Object { -not $_.StartsWith($D) }
$key.SetValue('Path', ($keep -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
$key.Close()
[Environment]::GetEnvironmentVariable('Path', 'User').Split(';') | Where-Object { $_.StartsWith($D) }
Set-Location $env:TEMP
Remove-Item -Recurse -Force $D
```

The `Where-Object` line must print nothing. If `Remove-Item` reports a file in use, find the process (`Get-Process | Where-Object { $_.Path -like "$D*" }`), stop it, and run it again; if a file stays locked, delete the folder after the next restart. The shortcut is gone: `Test-Path $lnk` prints `False`, unless the `W10` check found a shortcut of your own, which stays. Your real `~\.secondhand` was never used. Claude Code keeps the temporary folders in its list of trusted folders; that is harmless and is not edited here.
