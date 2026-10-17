# macOS checks

Apple silicon only: `uname -m` prints `arm64`. Intel is not tested here. The shell is zsh. On macOS `/tmp` is `/private/tmp`: `hand` prints `/private/tmp/...` paths for a folder that you made as `/tmp/...`, and both name the same folder. Read [`README.md`](README.md) for the safety rules and the result format, and your entry point ([`human.md`](human.md) or [`agent.md`](agent.md)) for how to run and report.

One real Mac has run these commands (#895). A command that fails for a reason that is not Hand is a guide bug: file it as `<ID>: guide command fails`.

In each check, **Run** says whether an agent can run it to the end (`agent`) or it ends in a question that only a person at the screen can answer (`needs human`). **Real sessions** counts the Claude Code sessions it starts on your account.

## Checks

The order is by risk, riskiest first, as the README lists them. `M0` comes first because every other check needs it.

| ID | Check | Proves, in the README's Platform support table | Run | Real sessions |
|---|---|---|---|---|
| M0 | Install and the temporary home | Install script; `hand init` and the Luvus pin | agent, then needs human | 0 |
| M1 | The supervisor starts, and Hand reaches Luvus | Luvus server, panes and events; Start claude, and accept its trust screen | agent | 1 |
| M2 | A real worker reports, and the supervisor wakes | Start claude, and accept its trust screen; A worker reports through the footer | agent | 1 |
| M3 | **Start** on the board runs the supervisor | Luvus server, panes and events | needs human | 1 |
| M4 | Stop and clean a worker that has a child process | Stop a worker | agent | 1 |
| M5 | The watcher starts a killed Luvus server again | The watcher starts a killed Luvus server again | agent | 0 |
| M6 | codex, agy and opencode start | Start codex or agy; Start opencode | agent, per harness | 1 each |
| M7 | `hand attach supervisor` | `hand attach` | needs human | 0 |
| M8 | `hand open` starts the board and opens the browser | `hand open` starts the board and opens the browser | needs human | 0 |
| M9 | A notification appears | Desktop notifications | needs human | 0 |
| M10 | `hand update` from the latest release | `hand update` | agent, then needs human | 0 |
| M11 | The LaunchAgent starts the board at login | Start the board at login | agent, then needs human | 0 (a resume) |

M1, M2, M3, M5 and M7 hand the supervisor on to each other, and each says whether the supervisor must be running or stopped. A supervisor that runs spends nothing while it is idle.

## M0: Install and the temporary home

- **Proves:** the Install script row, and the `hand init` and the Luvus pin row.
- **Run:** agent, then needs human (one question).
- **Real sessions:** 0.
- **Preconditions:** Apple silicon, `git`, `curl`, `tar`. Nothing needs `luvus` on `PATH`; if there is one, `hand init` pins it instead of downloading.

Steps:

**1.** Record the versions for the run issue:

```zsh
uname -m; sw_vers -productVersion; zsh --version; git --version; claude --version
```

**2.** Make the temporary folder. Do not set `TMPDIR`: the default is what is tested. The folder is short on purpose, because a Unix socket path has a length limit.

```zsh
export D=$(mktemp -d /tmp/hand-dogfood.XXXXXX)
mkdir -p $D/bin $D/fleet $D/repo $D/evidence
```

**3.** Write the environment file, and load it. Load it again at the start of every new shell. `redact` removes your names, and `ev ID` redacts and saves what a command prints to `$D/evidence/ID.txt`.

```zsh
cat > "$D/env.sh" <<EOF
export D="$D"
export SECONDHAND_HOME="\$D/home" LUVUS_HOME="\$D/luvus"
export PATH="\$D/bin:\$PATH"
if [ -f "\$D/vars.sh" ]; then . "\$D/vars.sh"; fi
redact() { sed -e "s#\$HOME#<home>#g" -e "s#\$(id -un)#<user>#g" -e "s#\$(hostname -s)#<machine>#g" -e "s#\$(scutil --get ComputerName 2>/dev/null || echo no-such-name)#<machine>#g" -e 's/token=[A-Za-z0-9._~-]*/token=<token>/g' -e 's/gh[pousr]_[A-Za-z0-9]\{20,\}/<token>/g'; }
ev() { redact | tee -a "\$D/evidence/\$1.txt"; }
EOF
. $D/env.sh
```

A user name that is a common word also changes that word in the output: read the redacted text before you post it.

Many agent harnesses start a new shell for every command, so a value that a step sets (`T`, `A`, `CUR`) is also appended to `$D/vars.sh`, which `env.sh` loads. Start every block, and every Evidence command, with `. $D/env.sh; cd $D/fleet`.

**4.** Install the latest release into the folder, and check that this copy is the one that runs:

```zsh
curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh | HAND_INSTALL_DIR=$D/bin sh
hash -r; command -v hand; hand version
```

**5.** Make the throw-away repository. Its local git settings keep your signing configuration out of it:

```zsh
cd $D/repo && git init -q -b main && git config user.name dogfood && git config user.email dogfood@example.invalid && git config commit.gpgsign false && echo hello > README.md && git add README.md && git commit -q -m init
```

**6.** Make the fleet, register the repository, and take the backup of `hand.db`. A profile named `cheap` uses the cheapest model and effort; change `haiku` if `hand route list` shows it as not valid for you:

```zsh
cd $D/fleet && hand init && hand project add demo $D/repo && cp hand.db $D/hand.db.backup
cat > routing.json <<'EOF'
{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}}}
EOF
hand route list | head -3
FID=$(ls $SECONDHAND_HOME/fleets)
echo "export FID=$FID LUVUS_BIN=$(hand luvus show | sed -n 's/^pin: [^ ]* //p') SD=$LUVUS_HOME/sessions/secondhand-$FID" >> $D/env.sh
. $D/env.sh; hand fleet list
```

**7.** Ask the owner: *While you installed and ran `hand`, did macOS show any dialog or notification about `hand` or `luvus`, such as "cannot be opened", "cannot verify the developer", "malware" or a request to allow it?* (yes or no, and its text).

Expected:
- the install prints `installed hand version: 0.11.0 to /tmp/hand-dogfood.XXXXXX/bin/hand` (the version can be newer), and `command -v hand` prints that path;
- `hand init` prints `created: true` and a `luvus: pinned 0.14.3 (...)` line (the version is the tested one);
- `hand fleet list` shows exactly one fleet, whose home is under the temporary folder, with state `ok`;
- `hand route list` shows `cheap,claude,haiku,low,yes`;
- the answer to the question is no.

Evidence: `{ hand version; hand fleet list; hand luvus show; } 2>&1 | ev M0`, and the answer.

If it fails: file it with the failing step and its output. If `M0` fails, stop: the other checks need it.

## M1: The supervisor starts, and Hand reaches Luvus

- **Proves:** the Luvus server, panes and events row, and the Start claude and accept its trust screen row.
- **Run:** agent.
- **Real sessions:** 1 (the supervisor).
- **Preconditions:** `M0` passed. `claude` is installed and logged in; otherwise skip `M1` to `M5`. No supervisor runs.

Steps:

**1.** Start it, and give it half a minute:

```zsh
. $D/env.sh; cd $D/fleet
hand supervisor start --profile cheap
sleep 30
hand supervisor show
hand orient | sed -n 1,5p
hand luvus show
ls -l $LUVUS_HOME/sessions/secondhand-$FID
echo "TMPDIR=$TMPDIR"
```

Expected:
- `hand supervisor start` prints `supervisor: s1`, `status: running`, `trust: accepted` and `prompt: submitted` (`trust: not asked` is also fine if Claude Code did not ask);
- `hand supervisor show` prints an `agent:` of `idle`, `working` or `done`, never `blocked`;
- `hand orient` prints `supervisor: s1 claude running` and `watch: running`;
- `hand luvus show` prints `match: yes`;
- the session folder exists and holds `server.pid`.

Leave the supervisor running for `M2`.

Evidence: `{ hand supervisor show; hand orient | sed -n 1,5p; hand luvus show; ls -l $LUVUS_HOME/sessions/secondhand-$FID; echo "TMPDIR=$TMPDIR"; } 2>&1 | ev M1`.

If it fails: file the output above and the start command's output. If `status` is not `running`, or `trust` is `not pressed`, add `hand supervisor show`. If `hand luvus show` says `not running` while the supervisor runs, or an error names the socket, add `ls -l $LUVUS_HOME/sessions/secondhand-$FID` and `echo $TMPDIR`: a socket path that is too long, or in the wrong folder, is the likely cause.

## M2: A real worker reports, and the supervisor wakes

- **Proves:** the Start claude and accept its trust screen row, and the A worker reports through the footer row.
- **Run:** agent.
- **Real sessions:** 1 (the worker), and one short turn of the supervisor when it wakes.
- **Preconditions:** `M1` passed, and the supervisor runs. The supervisor may act on the report (acknowledge it, mark the task done): that is expected. Do not answer it.

Steps:

**1.** Write a one-line task and start a worker on it:

```zsh
. $D/env.sh; cd $D/fleet
cat > $D/brief.md <<'EOF'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
EOF
T=$(hand task add --goal "Add hello.txt" demo "dogfood hello" | sed -n 's/^task: //p')
hand task start $T
CUR=$(hand orient | sed -n 's/^cursor: //p')
hand attempt start --profile cheap --prompt-file $D/brief.md $T | tee $D/m2-start.txt
A=$(sed -n 's/^attempt: //p' $D/m2-start.txt)
echo "export T=$T A=$A CUR=$CUR" >> $D/vars.sh
```

**2.** Wait for the wake. This blocks until an event arrives; run it again with the cursor it prints, at most three times, until a row has the kind `attempt.reported`:

```zsh
. $D/env.sh; cd $D/fleet
hand wait --after $CUR --timeout 8m
```

**3.** Read the result:

```zsh
. $D/env.sh; cd $D/fleet
hand report list
hand attempt show $A
git -C $D/repo log --oneline hand/$FID/$T-$A
hand orient | sed -n '/^recent/,/^operator_memory/p'
```

Expected:
- `hand attempt start` prints `status: running` and `trust: accepted`;
- `hand wait` returns a row `attempt.reported` for the task, within 8 minutes, in at most three waits;
- `hand report list` has a report with status `done` for the attempt;
- the branch `hand/<fleet id>/<task>-<attempt>` holds a commit `dogfood` over `init`;
- `hand orient` lists `supervisor.delivered` after `attempt.reported`, which is the wake reaching the supervisor (the event name is read from the code: if only that row is missing, say so and add `hand supervisor show`).

Evidence: `{ hand report list; hand attempt show $A; git -C $D/repo log --oneline hand/$FID/$T-$A; hand orient | sed -n '/^recent/,/^operator_memory/p'; } 2>&1 | ev M2`.

If it fails: file the output above. If `trust` is `not pressed` or the attempt is `blocked`, add `hand attempt read $A`, which prints the worker's screen. If the worker finished but no report exists, add the `hand attempt read $A` output: the report footer is what must reach it.

## M3: **Start** on the board runs the supervisor

- **Proves:** the Luvus server, panes and events row, through the board.
- **Run:** needs human.
- **Real sessions:** 1 (the supervisor).
- **Preconditions:** `M0` passed.

Steps:

**1.** Stop the supervisor that `M1` left, and start a board for this home. If something already listens on port 7777 (probably your real board), start this one on another port; do not stop yours:

```zsh
. $D/env.sh; cd $D/fleet
hand supervisor stop
if lsof -nP -iTCP:7777 -sTCP:LISTEN >/dev/null; then ($D/bin/hand board --addr 127.0.0.1:7790 > $D/board.log 2>&1 &); sleep 3; fi
hand open --print
```

The last command prints a link that holds this fleet's token. Show it to the owner in the terminal only, and never put it in an issue. Starting a board is also part of `M8`; `M3` does not judge it.

**2.** Ask the owner to open that link in a browser, and press **Start** on the fleet page. The profile is `cheap`. Then ask: *Within one minute, did the page show the supervisor as running, with no error banner?* (yes or no).

**3.** Check from the shell:

```zsh
sleep 20; hand supervisor show; hand orient | sed -n 1,5p
```

Expected: the answer is yes, `hand supervisor show` prints `status: running` and an `agent:` that is not `blocked`, and `hand orient` prints `supervisor: s2 claude running` and `watch: running`.

Leave the supervisor running, and the board.

Evidence: `{ hand supervisor show; hand orient | sed -n 1,5p; } 2>&1 | ev M3`, and the answer. Add the board's own log when it fails: `tail -20 $D/home/board.log $D/board.log 2>/dev/null | ev M3`.

If it fails: file what the owner saw (the text of any banner), the output above, and the log.

## M4: Stop and clean a worker that has a child process

- **Proves:** the Stop a worker row, with a real harness.
- **Run:** agent.
- **Real sessions:** 1 (the worker).
- **Preconditions:** `M0` passed and `claude` works. Run `hand supervisor stop` first, so that the supervisor does not react to the worker.

Steps:

**1.** Start a worker that runs a long command, so that a child process exists:

```zsh
. $D/env.sh; cd $D/fleet
hand supervisor stop
cat > $D/brief4.md <<'EOF'
Run exactly this shell command and wait for it to finish: sleep 1234
Do nothing else.
EOF
T=$(hand task add --goal "Hold a child process" demo "dogfood child" | sed -n 's/^task: //p')
hand task start $T
A=$(hand attempt start --profile cheap --prompt-file $D/brief4.md $T | sed -n 's/^attempt: //p')
echo "export T=$T A=$A" >> $D/vars.sh
for i in $(seq 24); do pgrep -f '[s]leep 1234' >/dev/null && break; sleep 5; done
pgrep -fl '[s]leep 1234'
```

If no `sleep 1234` appears within two minutes, the worker did not run it: run the step once more with a new task; if it fails again, mark `M4` `skip` with that reason.

**2.** Stop the worker, and look for the child:

```zsh
. $D/env.sh; cd $D/fleet
hand attempt stop $A
sleep 3
pgrep -fl '[s]leep 1234' || echo "child gone"
hand attempt show $A
```

**3.** Clean it:

```zsh
. $D/env.sh; cd $D/fleet
hand attempt clean $A
ls $SECONDHAND_HOME/worktrees/$FID
git -C $D/repo branch --list "hand/$FID/*"
```

Expected:
- `hand attempt stop` prints `status: stopped` and `reason: stopped by operator`;
- `child gone` prints: no `sleep 1234` is left;
- `hand attempt clean` prints `removed:` with the worktree path, and the folder no longer exists;
- the branch `hand/<fleet id>/<task>-<attempt>` is kept.

Evidence: `{ hand attempt show $A; ls $SECONDHAND_HOME/worktrees/$FID; git -C $D/repo branch --list "hand/$FID/*"; } 2>&1 | ev M4`.

If it fails: file the failing step's output. If a process is left, add `ps -o pid,ppid,command -p <pid>` for it.

## M5: The watcher starts a killed Luvus server again

- **Proves:** the watcher starts a killed Luvus server again row, with a real harness instead of a fake one.
- **Run:** agent.
- **Real sessions:** 0: it resumes the supervisor's session, which spends nothing until a message arrives.
- **Preconditions:** `M3` left a supervisor running, or one is started with `hand supervisor start --profile cheap`. The watcher runs (`hand orient` prints `watch: running`).

Steps:

**1.** Kill the server, and wait up to two minutes for a new one:

```zsh
. $D/env.sh; cd $D/fleet
cat $SD/server.pid
OLD=$(cut -d' ' -f1 $SD/server.pid)
kill -9 $OLD
for i in $(seq 24); do sleep 5; [ -f $SD/server.pid ] && [ "$(cut -d' ' -f1 $SD/server.pid)" != "$OLD" ] && break; done
cat $SD/server.pid
sleep 15
hand luvus show
hand supervisor show
hand orient | sed -n 1,5p
```

**2.** Resume the supervisor:

```zsh
hand supervisor resume
sleep 20
hand supervisor show
```

Expected:
- within two minutes `server.pid` names a different process, and `hand luvus show` prints `match: yes`;
- the old supervisor is `interrupted` (its pane died with the server), and `hand orient` says so;
- `hand supervisor resume` prints `status: running`, and the new supervisor's `agent:` is not `blocked`;
- `watch: running` throughout.

Evidence: `{ hand luvus show; hand supervisor show; hand orient | sed -n 1,5p; } 2>&1 | ev M5`.

If it fails: file the output above, and `tail -30 $D/fleet/watch.log` if it exists. If no new server comes within two minutes, say so, and add `hand luvus show`.

## M6: codex, agy and opencode start

- **Proves:** the Start codex or agy row, and the Start opencode row.
- **Run:** agent, once per harness. Skip a harness that is not installed, or that you have no account for, with the reason.
- **Real sessions:** 1 for each harness that runs.
- **Preconditions:** `M0` passed. Run `hand supervisor stop` first. Run codex once yourself before you use it, so that its model cache exists. For agy, `agy models` must list models. opencode uses the model from its own configuration.

Steps (repeat for each harness, with its own `H`, `MODEL` and `EFFORT` from the cheapest row of `hand route list` under `models`; opencode takes no model and no effort here):

**1.** List what is valid for you, and pick the cheapest model and effort:

```zsh
. $D/env.sh; cd $D/fleet
hand supervisor stop
hand route list
echo 'export H=codex MODEL=<model> EFFORT=<effort>' >> $D/vars.sh
```

Write the real harness, model and effort into that last line (for agy, leave `EFFORT` empty).

**2.** Write the brief, and start one worker on it. For codex and agy:

```zsh
. $D/env.sh; cd $D/fleet
cat > $D/brief.md <<'EOF'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
EOF
T=$(hand task add --goal "Add hello.txt" demo "dogfood $H" | sed -n 's/^task: //p')
hand task start $T
A=$(hand attempt start --harness $H --model $MODEL --effort $EFFORT --prompt-file $D/brief.md $T | sed -n 's/^attempt: //p')
echo "export T=$T A=$A" >> $D/vars.sh
```

For agy, leave out `--effort`. For opencode: `hand attempt start --harness opencode --prompt-file $D/brief.md $T`.

**3.** Wait up to eight minutes for the report, then read it:

```zsh
. $D/env.sh; cd $D/fleet
hand wait --timeout 8m
hand attempt show $A
hand report list
```

**4.** Stop and clean the worker: `. $D/env.sh; cd $D/fleet; hand attempt stop $A; hand attempt clean $A`.

Expected: the attempt is `running` right after the start, and its `launch:` line shows the harness's own flags (see README, The supervisor); the worker is not left `blocked` on a trust or permission screen that Hand should have handled; a report with status `done` arrives. If the harness asks something Hand does not answer, the attempt shows `blocked_on`: that is a failure.

Evidence: `{ hand attempt show $A; hand report list; } 2>&1 | ev M6`.

If it fails: file one issue per harness, titled `M6: <harness> ...`, with `hand attempt show $A` and `hand attempt read $A`.

## M7: `hand attach supervisor`

- **Proves:** the `hand attach` row, the one that is `not run` on macOS.
- **Run:** needs human. The owner opens a real terminal window; the agent cannot attach.
- **Real sessions:** 0 (a resume).
- **Preconditions:** `M0` passed. A supervisor runs: `hand supervisor resume`, or `hand supervisor start --profile cheap`.

Steps:

**1.** Tell the owner to open a new Terminal window and run these two lines, with the value of `$D` (print it with `echo $D`) in place of `/tmp/hand-dogfood.XXXXXX`:

```zsh
. /tmp/hand-dogfood.XXXXXX/env.sh
cd $D/fleet && hand attach supervisor
```

**2.** Ask the owner: *Did the supervisor's Claude Code screen appear in that window, and when you typed `x`, did it show in the input box?* (yes or no). Then ask them to delete the `x` and leave: close that Terminal window. (The key that detaches from a Luvus pane is not documented in this repository: unverified.)

**3.** Check that attaching did not harm it:

```zsh
hand supervisor show
```

Expected: the answer is yes, and the supervisor is still `running` afterwards.

Evidence: the answer, and `hand supervisor show | ev M7`.

If it fails: file what the owner saw, and what `hand attach supervisor` printed (for example `attach needs a terminal`).

## M8: `hand open` starts the board and opens the browser

- **Proves:** the `hand open` starts the board and opens the browser row.
- **Run:** needs human.
- **Real sessions:** 0.
- **Preconditions:** `M0` passed. No board of this home runs: stop one that you started, with `kill $(cut -d' ' -f1 $SECONDHAND_HOME/board.pid)`. Nothing listens on port 7777: `lsof -nP -iTCP:7777 -sTCP:LISTEN` prints nothing. Otherwise `skip` with `port 7777 busy`, and do not stop what listens.

Steps:

**1.** Run it, and look at the board:

```zsh
. $D/env.sh; cd $D/fleet
hand open
sleep 3
cat $SECONDHAND_HOME/board.addr
```

**2.** Ask the owner: *Did your default browser open the Hand fleet page, already logged in (no question about a token)?* (yes or no, and which browser).

Expected: `hand open` prints `opened: "http://127.0.0.1:7777/<fleet id>/"` (the URL is in quotes); `board.addr` holds `127.0.0.1:7777`; the answer is yes.

Evidence: `{ hand open; cat $SECONDHAND_HOME/board.addr; tail -5 $SECONDHAND_HOME/board.log; } 2>&1 | ev M8`, and the answer.

If it fails: file the output above. If the board did not start, add the end of `$SECONDHAND_HOME/board.log` (the command's error also names how to read it).

## M9: A notification appears

- **Proves:** the Desktop notifications row, with `osascript`.
- **Run:** needs human.
- **Real sessions:** 0.
- **Preconditions:** `M0` passed. The watcher runs: a supervisor runs (`hand supervisor resume`), or start the watcher by hand with `($D/bin/hand watch > $D/watch-m9.log 2>&1 &)` (the full path, so that the Teardown can tell it is yours). macOS may ask you to allow notifications for the script runner, and Focus mode hides them: allow it, or turn Focus off, for this check.

Steps:

**1.** Ask a decision: the watcher alerts on it.

```zsh
. $D/env.sh; cd $D/fleet
T=$(hand task add --goal "Notify me" demo "dogfood notify" | sed -n 's/^task: //p')
hand decision ask $T "Did the notification show?"
```

**2.** Ask the owner, within 30 seconds: *Did a macOS notification appear with the title `fleet` and a text starting `t` followed by the task number and ` decision:`? Look in Notification Center too.* (yes or no, and the text, and whether it was a banner or only in Notification Center). When a person has to relay the answer, 30 seconds cannot hold: resend it live with `hand decision ask $T "Did the notification show again?"` while the owner watches, or accept Notification Center, and write which one in the answer.

Expected: the answer is yes.

Evidence: the answer, and `tail -5 $D/fleet/watch.log $D/watch-m9.log 2>/dev/null | ev M9`.

If it fails: file what the owner saw, and whether macOS asked for permission.

## M10: `hand update` from the latest release

- **Proves:** the `hand update` row.
- **Run:** agent, then needs human (one question).
- **Real sessions:** 0.
- **Preconditions:** `M0` passed. Nothing listens on port 7777 (the restarted board uses it): `lsof -nP -iTCP:7777 -sTCP:LISTEN` prints nothing, and no board of `$D/home` runs. Otherwise `skip` with `port 7777 busy`. This check uses its own home, `$D/home2`, and its own copy of the binary in `$D/old`: it never updates the copy in `$D/bin` or any other `hand`.

Steps:

**1.** Install v0.11.0 or a later release as a second copy (the update stops processes by pid file from v0.11.0 on, so an older copy cannot restart the watcher and the board on macOS), make a fleet in a second home, and start its watcher and board with that old copy:

```zsh
. $D/env.sh
mkdir -p $D/old $D/fleet2
curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh | HAND_INSTALL_DIR=$D/old HAND_INSTALL_VERSION=v0.11.0 sh
H2=$D/home2
cd $D/fleet2 && SECONDHAND_HOME=$H2 $D/old/hand init
cp $D/fleet2/hand.db $D/hand2.db.backup
(SECONDHAND_HOME=$H2 $D/old/hand watch > $D/watch2.log 2>&1 &)
(SECONDHAND_HOME=$H2 $D/old/hand board > $D/board2.log 2>&1 &)
sleep 5
cat $D/fleet2/watch.pid $H2/board.pid
SECONDHAND_HOME=$H2 $D/old/hand update --channel edge --check
```

**2.** Update to the rolling build, and look at the result:

```zsh
. $D/env.sh; H2=$D/home2; cd $D/fleet2
SECONDHAND_HOME=$H2 $D/old/hand update --channel edge
SECONDHAND_HOME=$H2 $D/old/hand version
cat $D/fleet2/watch.pid $H2/board.pid
sleep 5
SECONDHAND_HOME=$H2 $D/old/hand orient | grep '^watch:'
SECONDHAND_HOME=$H2 $D/old/hand open --print > /dev/null && echo board-answers
```

**3.** Ask the owner: *During the update, did macOS show any dialog or notification about `hand`, such as "cannot be opened" or "malware"?* (yes or no, and its text).

Expected:
- the check prints `status: checked`; the update prints `status: updated`, `from: 0.11.0 stable ...` and `to: 0.11.0 edge ...` (`to` can be newer, and `from` is the release you installed), a `backups` list, and `init ok` for the fleet;
- `hand version` now prints `channel: edge`;
- both PID files changed, `orient` prints `watch: running`, and `board-answers` prints: the watcher and the board run again, started by the update;
- the answer is no.

Evidence: `{ SECONDHAND_HOME=$H2 $D/old/hand version; cat $D/fleet2/watch.pid $H2/board.pid; tail -5 $D/watch2.log $D/board2.log; } 2>&1 | ev M10`, the update's output, and the answer.

If it fails: file the update's output. The journal `$H2/update.json` (if it exists) belongs in the evidence. If the binary was replaced but the board or the watcher did not return, say which.

## M11: The LaunchAgent starts the board at login

- **Proves:** the Start the board at login row. The board's `PATH` must find your tools.
- **Run:** agent, then needs human (logout and login).
- **Real sessions:** 0 (a resume).
- **Preconditions:** `M0` passed. This check writes one file outside the temporary folder, `~/Library/LaunchAgents/dev.secondhand.dogfood.board.plist`, which it removes. The label is not `dev.secondhand.board`, so it cannot collide with your own. A supervisor runs (`hand supervisor resume`), so that the board has a fleet to keep. No board of this home runs.

Steps:

**1.** Check that the file is free. The command must print `free`; if it does not, skip `M11` with the reason `plist exists`, and do not overwrite or remove that file:

```zsh
test ! -e ~/Library/LaunchAgents/dev.secondhand.dogfood.board.plist && echo free
```

**2.** Write the plist from the README's Autostart recipe, with this temporary home, another port, and a `PATH` that finds `claude` and `git`:

```zsh
. $D/env.sh; cd $D/fleet
PLIST=~/Library/LaunchAgents/dev.secondhand.dogfood.board.plist
kill $(cut -d' ' -f1 $SECONDHAND_HOME/board.pid) 2>/dev/null
TOOLS=$(dirname $(command -v claude)):$(dirname $(command -v git))
cat > $PLIST <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.secondhand.dogfood.board</string>
  <key>ProgramArguments</key>
  <array>
    <string>$D/bin/hand</string>
    <string>board</string>
    <string>--addr</string>
    <string>127.0.0.1:7790</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>$TOOLS:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
    <key>SECONDHAND_HOME</key>
    <string>$SECONDHAND_HOME</string>
    <key>LUVUS_HOME</key>
    <string>$LUVUS_HOME</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
EOF
launchctl load $PLIST
sleep 10
launchctl list | grep dev.secondhand.dogfood
hand open --print > /dev/null && echo board-answers
ps eww -p $(cut -d' ' -f1 $SECONDHAND_HOME/board.pid) | tr ' ' '\n' | grep '^PATH='
```

**3.** The board must keep the watcher alive. Kill the watcher, and wait up to 60 seconds:

```zsh
. $D/env.sh; cd $D/fleet
kill $(cut -d' ' -f1 $D/fleet/watch.pid)
for i in $(seq 12); do sleep 5; hand orient | grep -q '^watch: running' && break; done
hand orient | sed -n 1,5p
```

**4.** Prepare the logout: make the board resume the supervisor by itself, which needs `claude` on the board's `PATH`:

```zsh
. $D/env.sh
cat > $D/fleet/routing.json <<'EOF'
{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}},"supervisor":{"autoresume":true}}
EOF
```

**5.** Print the line that the owner will run, with the real path, and give it to them exactly:

```zsh
. $D/env.sh
echo ". $D/env.sh; cd $D/fleet; hand orient | sed -n 1,5p; launchctl list | grep dev.secondhand.dogfood; hand open --print > /dev/null && echo board-answers"
```

Ask the owner: *Log out of macOS from the Apple menu, log in again, wait one minute, then open Terminal and run that line. Paste the output here. Did it print `board-answers`, a `launchctl` line with a number in its first column, and `supervisor: ... running`?* (yes or no, and the output).

Logging out ends this session, and the supervisor. An agent cannot finish this step: it leaves the check `waiting for owner`, and the owner runs the Teardown after they answer.

Expected:
- the plist loads, `launchctl list` shows the label with a process ID, `board-answers` prints, and the board's `PATH=` holds the folders of `claude` and `git`;
- the board starts a new watcher within 60 seconds of the kill;
- after login, the board runs again, and the board's watcher resumed the supervisor (`supervisor: s... claude running`, perhaps `(resumes s...)`), which proves that the board's `PATH` finds `claude`;
- the answer is yes.

Evidence: `{ launchctl list | grep dev.secondhand.dogfood; hand orient | sed -n 1,5p; ps eww -p $(cut -d' ' -f1 $SECONDHAND_HOME/board.pid) | tr ' ' '\n' | grep '^PATH='; } 2>&1 | ev M11`, and the owner's answer.

If it fails: file the output above and `tail -20 $SECONDHAND_HOME/board.log`.

## Teardown

Run it at the end, even after a failure, and again after a logout. In its first line, write the real path of `D`. It removes the LaunchAgent that `M11` made (and only a file that names your temporary folder), stops every process of this run, and deletes the temporary folder. Post your evidence first: the evidence files are in that folder.

```zsh
. /tmp/hand-dogfood.XXXXXX/env.sh
PLIST=~/Library/LaunchAgents/dev.secondhand.dogfood.board.plist
if grep -q "$D/" $PLIST 2>/dev/null; then launchctl unload $PLIST; rm -f $PLIST; fi
for f in $D/fleet*/watch.pid(N) $D/home*/board.pid(N); do
  [ -f $f ] || continue
  p=$(cut -d' ' -f1 $f)
  ps -p $p -o command= | grep -q "^$D/" && kill $p
done
[ -f $D/caffeinate.pid ] && ps -p $(cat $D/caffeinate.pid) -o command= | grep -q "^caffeinate" && kill $(cat $D/caffeinate.pid)
for s in $(ls $LUVUS_HOME/sessions 2>/dev/null); do $LUVUS_BIN session stop $s; done
sleep 2
pgrep -fl "^$D/"
lsof +D $D 2>/dev/null
```

The last two commands must print nothing: a process that remains runs from `$D`, or holds a file under it, and belongs to this run, so stop it by its process ID. Then:

```zsh
launchctl list | grep dev.secondhand.dogfood
ls ~/Library/LaunchAgents | grep dev.secondhand.dogfood
rm -rf $D
```

The first two must print nothing, unless the `M11` guard found a file of your own, which stays. Your real `~/.secondhand` was never used. Claude Code keeps the temporary folders in its list of trusted folders; that is harmless and is not edited here.
