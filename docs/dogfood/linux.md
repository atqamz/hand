# Linux checks

Linux without a systemd user session, such as a plain SSH login to a server or a container, and Linux arm64 if you have one. The README's Platform support table says that this setup has run only in CI. Linux with a user session runs Hand every day, and is not tested here. The shell is bash or zsh. Read [`README.md`](README.md) for the safety rules and the result format, and your entry point ([`human.md`](human.md) or [`agent.md`](agent.md)) for how to run and report.

Nobody has run these commands in a dogfood run before the first one. A command marked `unverified` may be wrong. A command that fails for a reason that is not Hand is a guide bug: file it as `<ID>: guide command fails`.

In each check, **Run** says whether an agent can run it to the end (`agent`) or it ends in a question that only a person at the screen can answer (`needs human`). **Real sessions** counts the Claude Code sessions it starts on your account.

## Which machine

- **A host with no systemd user session:** a login where `ls "$XDG_RUNTIME_DIR/systemd/private"` fails or `XDG_RUNTIME_DIR` is empty, such as a minimal container or a server where `pam_systemd` is off. This is the real test.
- **A machine that has a user session:** `L0` unsets `XDG_RUNTIME_DIR` and `DBUS_SESSION_BUS_ADDRESS` in the test shell, so that Hand takes the path that it takes without one. This emulates the setup: write `emulated` in the run issue. It does not touch your units: with the variables unset, Hand calls no `systemctl` and no `systemd-run`.
- **arm64:** if `uname -m` prints `aarch64`, the same checks are the arm64 checks: the release binary and the tested Luvus build for arm64 have never run in CI. Write the architecture in the run issue.

## Checks

The order is by risk, riskiest first. `L0` comes first because every other check needs it.

| ID | Check | Proves, in the README's Platform support table | Run | Real sessions |
|---|---|---|---|---|
| L0 | Install and the temporary home | Install script; `hand init` and the Luvus pin | agent | 0 |
| L1 | The supervisor and the watcher start without a user manager | The setup without a systemd user session (the note under the table); Start claude, and accept its trust screen | agent | 1 |
| L2 | A real worker reports, and the supervisor wakes | A worker reports through the footer | agent | 1 |
| L3 | The board starts without a unit, and keeps the watcher alive | Start the watcher on demand, and the board keeps watchers alive; `hand open` starts the board | agent | 0 |
| L4 | The watcher starts a killed Luvus server again | The watcher starts a killed Luvus server again | agent | 0 |
| L5 | `hand attach supervisor` over a real terminal | `hand attach` | needs human | 0 |

## L0: Install and the temporary home

- **Proves:** the Install script row, and the `hand init` and the Luvus pin row.
- **Run:** agent.
- **Real sessions:** 0.
- **Preconditions:** `git`, `curl`, `tar`, and `sha256sum` or `shasum`. Nothing needs `luvus` on `PATH`; if there is one, `hand init` pins it instead of downloading.

Steps:

**1.** Record the versions for the run issue:

```sh
uname -m; . /etc/os-release; echo "$PRETTY_NAME"; echo "$SHELL"; git --version; claude --version
echo "XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-unset}"
```

**2.** Make the temporary folder:

```sh
export D=$(mktemp -d /tmp/hand-dogfood.XXXXXX)
mkdir -p $D/bin $D/fleet $D/repo $D/evidence
```

**3.** Write the environment file, and load it. Load it again at the start of every new shell. It unsets the two variables that make Hand see a user manager. `redact` removes your names, and `ev ID` redacts and saves what a command prints to `$D/evidence/ID.txt`.

```sh
cat > "$D/env.sh" <<EOF
unset XDG_RUNTIME_DIR DBUS_SESSION_BUS_ADDRESS
export D="$D"
export SECONDHAND_HOME="\$D/home" LUVUS_HOME="\$D/luvus"
export PATH="\$D/bin:\$PATH"
if [ -f "\$D/vars.sh" ]; then . "\$D/vars.sh"; fi
redact() { sed -e "s#\$HOME#<home>#g" -e "s#\$(id -un)#<user>#g" -e "s#\$(hostname -s 2>/dev/null || hostname)#<machine>#g" -e 's/token=[A-Za-z0-9._~-]*/token=<token>/g' -e 's/gh[pousr]_[A-Za-z0-9]\{20,\}/<token>/g'; }
ev() { redact | tee -a "\$D/evidence/\$1.txt"; }
EOF
. $D/env.sh
```

A user name that is a common word also changes that word in the output: read the redacted text before you post it.

Many agent harnesses start a new shell for every command, so a value that a step sets (`T`, `A`, `CUR`) is also appended to `$D/vars.sh`, which `env.sh` loads. Start every block, and every Evidence command, with `. $D/env.sh; cd $D/fleet`.

**4.** Install the latest release into the folder, and check that this copy is the one that runs:

```sh
curl -fsSL https://raw.githubusercontent.com/atqamz/hand/main/install.sh | HAND_INSTALL_DIR=$D/bin sh
hash -r; command -v hand; hand version
```

**5.** Make the throw-away repository. Its local git settings keep your signing configuration out of it:

```sh
cd $D/repo && git init -q -b main && git config user.name dogfood && git config user.email dogfood@example.invalid && git config commit.gpgsign false && echo hello > README.md && git add README.md && git commit -q -m init
```

**6.** Make the fleet, register the repository, and take the backup of `hand.db`. A profile named `cheap` uses the cheapest model and effort; change `haiku` if `hand route list` shows it as not valid for you:

```sh
cd $D/fleet && hand init && hand project add demo $D/repo && cp hand.db $D/hand.db.backup
cat > routing.json <<'EOF'
{"profiles":{"cheap":{"harness":"claude","model":"haiku","effort":"low"}}}
EOF
hand route list | head -3
FID=$(ls $SECONDHAND_HOME/fleets)
echo "export FID=$FID LUVUS_BIN=$(hand luvus show | sed -n 's/^pin: [^ ]* //p') SD=$LUVUS_HOME/sessions/secondhand-$FID" >> $D/env.sh
. $D/env.sh; hand fleet list
```

Expected:
- the install prints `installed hand version: 0.10.0 to /tmp/hand-dogfood.XXXXXX/bin/hand` (the version can be newer), and `command -v hand` prints that path;
- `hand init` prints `created: true` and a `luvus: pinned 0.14.3 (...)` line (the version is the tested one; on arm64 the download is the `aarch64-unknown-linux-musl` build);
- `hand fleet list` shows exactly one fleet, whose home is under the temporary folder, with state `ok`;
- `hand route list` shows `cheap,claude,haiku,low,yes`;
- after `. $D/env.sh`, `echo "${XDG_RUNTIME_DIR:-unset}"` prints `unset`.

Evidence: `{ uname -m; hand version; hand fleet list; hand luvus show; } 2>&1 | ev L0`.

If it fails: file it with the failing step and its output. If `L0` fails, stop: the other checks need it.

## L1: The supervisor and the watcher start without a user manager

- **Proves:** the setup without a systemd user session, which has run only in CI, and the Start claude, and accept its trust screen row, with the real harness. The README's lifecycle text says what to expect: Luvus starts directly, and the watcher runs detached with its output in `watch.log`.
- **Run:** agent.
- **Real sessions:** 1 (the supervisor).
- **Preconditions:** `L0` passed. `claude` is installed and logged in; otherwise skip `L1` to `L4`. No supervisor runs.

Steps:

**1.** Start it, and give it half a minute:

```sh
. $D/env.sh; cd $D/fleet
hand supervisor start --profile cheap
sleep 30
hand supervisor show
hand orient | sed -n 1,5p
hand luvus show
ls $D/fleet/watch.log $D/fleet/watch.pid
cut -d' ' -f1 $D/fleet/watch.pid | xargs -I{} ps -o pid,ppid,comm -p {}
cat $SD/server.pid
cut -d' ' -f1 $SD/server.pid | xargs -I{} cat /proc/{}/cgroup
```

Expected:
- `hand supervisor start` prints `supervisor: s1`, `status: running`, `trust: accepted` and `prompt: submitted` (`trust: not asked` is also fine if Claude Code did not ask);
- `hand supervisor show` prints an `agent:` of `idle`, `working` or `done`, never `blocked`;
- `hand orient` prints `supervisor: s1 claude running` and `watch: running`;
- `hand luvus show` prints `match: yes`;
- `watch.log` and `watch.pid` exist, and the watcher's process is `hand`: it runs detached, not as a unit;
- the server's cgroup is the cgroup of your login, not a `*.scope` of its own: without a user manager Hand starts Luvus directly. Say what the cgroup line shows, whatever it is (on a host without systemd `/proc/<pid>/cgroup` may be empty or `0::/`).

Leave the supervisor running for `L2`.

Evidence: `{ hand supervisor show; hand orient | sed -n 1,5p; hand luvus show; cut -d' ' -f1 $SD/server.pid | xargs -I{} cat /proc/{}/cgroup; tail -5 $D/fleet/watch.log; } 2>&1 | ev L1`.

If it fails: file the output above and the start command's output. If `status` is not `running`, or `trust` is `not pressed`, add `hand supervisor show`. If the watcher is `missing`, add `tail -20 $D/fleet/watch.log`.

## L2: A real worker reports, and the supervisor wakes

- **Proves:** the A worker reports through the footer row, without a user manager.
- **Run:** agent.
- **Real sessions:** 1 (the worker), and one short turn of the supervisor when it wakes.
- **Preconditions:** `L1` passed, and the supervisor runs. The supervisor may act on the report (acknowledge it, mark the task done): that is expected. Do not answer it.

Steps:

**1.** Write a one-line task and start a worker on it:

```sh
. $D/env.sh; cd $D/fleet
cat > $D/brief.md <<'EOF'
Create a file named hello.txt in the repository root containing exactly: hello
Commit it with the message: dogfood
Do nothing else.
EOF
T=$(hand task add --goal "Add hello.txt" demo "dogfood hello" | sed -n 's/^task: //p')
hand task start $T
CUR=$(hand orient | sed -n 's/^cursor: //p')
hand attempt start --profile cheap --prompt-file $D/brief.md $T | tee $D/l2-start.txt
A=$(sed -n 's/^attempt: //p' $D/l2-start.txt)
echo "export T=$T A=$A CUR=$CUR" >> $D/vars.sh
```

**2.** Wait for the wake. This blocks until an event arrives; run it again with the cursor it prints, at most three times, until a row has the kind `attempt.reported`:

```sh
. $D/env.sh; cd $D/fleet
hand wait --after $CUR --timeout 8m
```

**3.** Read the result:

```sh
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

Evidence: `{ hand report list; hand attempt show $A; git -C $D/repo log --oneline hand/$FID/$T-$A; hand orient | sed -n '/^recent/,/^operator_memory/p'; } 2>&1 | ev L2`.

If it fails: file the output above. If `trust` is `not pressed` or the attempt is `blocked`, add `hand attempt read $A`, which prints the worker's screen.

## L3: The board starts without a unit, and keeps the watcher alive

- **Proves:** the Start the watcher on demand, and the board keeps watchers alive row, and the `hand open` starts the board and opens the browser row, on a host that may have no browser.
- **Run:** agent.
- **Real sessions:** 0.
- **Preconditions:** `L1` passed, and the supervisor runs, so that the board has a fleet to keep. No board of this home runs. Nothing listens on port 7777: `ss -ltn 'sport = :7777'` lists no listener (or `lsof -nP -iTCP:7777 -sTCP:LISTEN` prints nothing). Otherwise `skip` with `port 7777 busy`, and do not stop what listens.

Steps:

**1.** Open the fleet. On a host without `xdg-open`, or without a display, the command cannot open a browser: that is expected, and it must still start the board and say what to do:

```sh
. $D/env.sh; cd $D/fleet
hand open; echo "exit=$?"
sleep 3
cat $SECONDHAND_HOME/board.addr
ls $SECONDHAND_HOME/board.log
hand open --print > /dev/null && echo board-answers
```

**2.** Kill the watcher. The board starts it again within 30 seconds; wait up to 60:

```sh
kill $(cut -d' ' -f1 $D/fleet/watch.pid)
for i in $(seq 12); do sleep 5; hand orient | grep -q '^watch: running' && break; done
hand orient | sed -n 1,5p
```

Expected:
- the board runs detached, with its output in `board.log`: `board.addr` holds `127.0.0.1:7777`, and `board-answers` prints;
- `hand open` either prints `opened: http://127.0.0.1:7777/<fleet id>/` with exit 0, or fails with a message that names the missing opener and tells you to open the page yourself (with the board still started); a hang is a failure;
- `watch: running` returns within 60 seconds of the kill.

Evidence: `{ hand open; cat $SECONDHAND_HOME/board.addr; tail -5 $SECONDHAND_HOME/board.log; hand orient | sed -n 1,5p; } 2>&1 | ev L3`. Never print `hand open --print` into an issue.

If it fails: file the output above and the end of `$SECONDHAND_HOME/board.log`.

## L4: The watcher starts a killed Luvus server again

- **Proves:** the watcher starts a killed Luvus server again row, with a real harness instead of a fake one, and without the scope that a user manager gives the server.
- **Run:** agent.
- **Real sessions:** 0: it resumes the supervisor's session, which spends nothing until a message arrives.
- **Preconditions:** a supervisor runs (`hand supervisor start --profile cheap` if none does), and the watcher runs (`hand orient` prints `watch: running`).

Steps:

**1.** Kill the server, and wait up to two minutes for a new one:

```sh
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

```sh
hand supervisor resume
sleep 20
hand supervisor show
```

Expected:
- within two minutes `server.pid` names a different process, and `hand luvus show` prints `match: yes`;
- the old supervisor is `interrupted` (its pane died with the server), and `hand orient` says so;
- `hand supervisor resume` prints `status: running`, and the new supervisor's `agent:` is not `blocked`;
- `watch: running` throughout.

Evidence: `{ hand luvus show; hand supervisor show; hand orient | sed -n 1,5p; } 2>&1 | ev L4`.

If it fails: file the output above, and `tail -30 $D/fleet/watch.log`. If no new server comes within two minutes, say so, and add `hand luvus show`.

## L5: `hand attach supervisor` over a real terminal

- **Proves:** the `hand attach` row, over a real terminal and not the CI's fake.
- **Run:** needs human. The owner uses a real terminal (an SSH session with a tty); the agent cannot attach.
- **Real sessions:** 0 (a resume).
- **Preconditions:** `L0` passed. A supervisor runs: `hand supervisor resume`, or `hand supervisor start --profile cheap`.

Steps:

**1.** Tell the owner to open a terminal on this machine and run, with the real path of `D` for the first line:

```sh
. /tmp/hand-dogfood.XXXXXX/env.sh
cd $D/fleet && hand attach supervisor
```

**2.** Ask the owner: *Did the supervisor's Claude Code screen appear in that terminal, and when you typed `x`, did it show in the input box?* (yes or no). Then ask them to delete the `x` and leave: close that terminal. (The key that detaches from a Luvus pane is not documented in this repository: unverified.)

**3.** Check that attaching did not harm it:

```sh
hand supervisor show
```

Expected: the answer is yes, and the supervisor is still `running` afterwards.

Evidence: the answer, and `hand supervisor show | ev L5`.

If it fails: file what the owner saw, and what `hand attach supervisor` printed (for example `attach needs a terminal`).

## Teardown

Run it at the end, even after a failure. It stops every process of this run, and deletes the temporary folder. In its first line, write the real path of `D`. Post your evidence first: the evidence files are in that folder.

```sh
. /tmp/hand-dogfood.XXXXXX/env.sh
for f in $D/fleet/watch.pid $D/home/board.pid; do
  [ -f $f ] || continue
  p=$(cut -d' ' -f1 $f)
  ps -p $p -o command= | grep -q "^$D/" && kill $p
done
for s in $(ls $LUVUS_HOME/sessions 2>/dev/null); do $LUVUS_BIN session stop $s; done
sleep 2
pgrep -fa "^$D/"
```

The last command must print nothing; a process that remains runs from `$D`, belongs to this run, and is stopped by its process ID. Then delete the folder:

```sh
rm -rf $D
```

Your real `~/.secondhand` and your units were never used. Claude Code keeps the temporary folders in its list of trusted folders; that is harmless and is not edited here.
