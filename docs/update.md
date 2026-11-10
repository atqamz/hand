# Update

## What `hand update` does

To update an installed Hand, run `hand update` from anywhere. It does the following:
- downloads the newest build of the channel the running `hand` came from, and checks it;
- backs up every fleet's `hand.db` and the old binary under `~/.secondhand/backups/`;
- replaces the binary, then stops each fleet's watcher and the board by the process IDs they record (`watch.pid` in the fleet folder, `board.pid` in `~/.secondhand`) and starts them again as `hand supervisor start` and `hand open` do, on every OS;
- runs `hand init` in every fleet.

It also fetches the `hand-iroh` sidecar of that release, when the new `hand` names one (the `iroh:` line of `hand version`) and the release carries it, and puts it next to `hand`: see [The hand-iroh sidecar](phone.md#the-hand-iroh-sidecar). The asset and its `.sha256` are checked, and the sidecar must report the version the new `hand` needs; if any check fails, nothing is replaced and the update stops before it touches `hand`, a fleet or a process. A release without the asset (0.13.0 and earlier, and `edge`) is not an error: the report says `iroh: none in this release` and an installed sidecar stays. The sidecar is placed before `hand` is, so a failure to place it leaves the old `hand` in place, and the board's restart at the end starts the new sidecar with the new board. If placing `hand` itself then fails, the old `hand` stays with the new sidecar: `hand doctor` warns about the mismatch, and the next `hand update` replaces both. An update that finds `hand` already current does not replace the hand, but it still installs the sidecar when it is missing or is not the version that hand needs, and the release carries that version: this is how an update run by 0.13.0, which installs the new `hand` without a sidecar, is repaired by the new hand's next update. `hand update --check` reports `iroh: would install` for that, or `iroh: none in this release` when the release carries no sidecar, and changes nothing. On Windows the running sidecar is renamed to `hand-iroh.exe.old` as `hand.exe` is, and the next `hand` run removes it.

It also fetches `hand-desktop`, the native window, on Linux amd64 and arm64, and puts it next to `hand`. The release names the asset `hand-desktop-linux-amd64.tar.gz` (or `hand-desktop-linux-arm64.tar.gz`), with a `.sha256` beside it; both are checked, and the binary must print the version and commit that the new `hand` reports in `hand version` (`hand-desktop VERSION COMMIT`) before anything is replaced. A release without the asset is not an error: the report says `desktop: none in this release` and an installed window stays. On macOS, Windows and any other architecture the report says `desktop: unsupported on OS/ARCH` and nothing is downloaded; the Board works in a browser there. The window is placed before `hand` is, as the sidecar is, so a failure leaves the old `hand` and the old window in place. An update that finds `hand` current still installs a window that is missing or does not carry that version and commit; when that download fails a check, the report says `desktop: failed: <reason>`, the window stays as it was, and the switch of the stale fleets still runs. `hand update --check` reports `desktop: would install`, `desktop: none in this release` or `desktop: unsupported on OS/ARCH`, and changes nothing.

It also pins the Luvus version that build was tested with when that is newer than the pin. On every OS it compares the version a UHP `ping` reports with the pin's: a server that does not answer is not running and has nothing to switch, and one that answers without a version is `unknown` and is never switched.

A server that runs another version than the pin is switched, fleet by fleet, only when the fleet is quiet and settled. Quiet: no worker attempt is live, the supervisor is idle, no message waits for it, and no pane is live in that server besides the supervisor's and the shell Luvus opens when it starts. Settled: the fleet recorded no event for 15 minutes. The checks run in this order, and a fleet that fails one stays `pending: <reason>` in the `fleets` rows, which names a pane Hand did not start, since stopping the server ends it (a check before step 2 changes nothing, and the watcher keeps running):
1. quiet and settled;
2. stop that fleet's watcher, so nothing wakes the supervisor;
3. the supervisor's screen revision (`content_revision`) is unchanged over 30 seconds, and the fleet is still quiet;
4. `hand supervisor stop`, then check quiet again: a message queued meanwhile resumes the supervisor and leaves the fleet pending;
5. stop the server (it sends `server.stop` and waits up to 10 seconds for the process in the session folder's `server.pid` to exit; it ends the process itself, its whole process tree on Windows, only when the stop was acknowledged or the server no longer answers, so a server that answers `ping` but refuses `server.stop` is left running, and a recycled process ID is never signalled), start the pinned copy, and require that `ping` reports the pin's version;
6. resume the supervisor, start the watcher, and record the event `luvus.switched` (`0.14.3 -> 0.15.0`), which `hand orient` lists and the board shows as one line in the chat.

Any failure at step 4 to 6 (the supervisor's stop, the server's stop or start, a wrong version) records `luvus.failed` with the reason (a server that comes back with another version is one), puts the supervisor and the watcher back, and leaves `hand update` with a non-zero exit. The board's timer does not try that pin again; your own `hand update` does, once the fleet is settled again. `luvus.failed` and `luvus.switched` are events like the others, so the 15 minutes start again after each. A `hand update` run inside a fleet's Luvus panes (`LUVUS_SESSION` names that fleet's session) leaves that fleet pending; the board's timer runs outside every pane and may switch it later.

What a switch costs:
- a `hand attach` viewer is disconnected (Luvus cannot tell Hand who is attached): attach again;
- text typed but not sent in the supervisor's pane is lost if the screen stayed still for the 30 seconds (a supervisor whose screen keeps changing while idle never passes that check, so its fleet stays pending);
- no worker loses work, because a switch needs no live attempt and no pane that Hand did not start; the files in the worktrees stay;
- the supervisor comes back as a new `sN` on the same session.

When the binary is already current and no newer Luvus needs pinning, `hand update` visits only the fleets whose server is stale: it does not stop the board, does not run `hand init`, and leaves every other fleet's watcher alone. `hand update --check` lists a fleet's server as `not running` or `unknown` under that name, plans no switch for it, and shows `would switch` or `would stay pending: <reason>` for a stale one. `hand update` signals only a process whose recorded binary is the one being updated, and only while its recorded start time still matches, and it restarts only what it stopped. It stops a process with SIGTERM (`TerminateProcess` on Windows), which exits cleanly, so systemd does not restart an installed unit itself: an installed board unit is started again, and an installed watch unit stays inactive while the watcher runs as the transient unit `secondhand-watch-<fleet id>` until your next login. If both ever run, the second watcher finds `watch.lock` held and exits with status 3, and a second board fails to bind the board's address. While an update runs, the board starts no watcher. Once it starts changing things it ignores Ctrl-C, and its own steps run outside the terminal's process group, so it does not leave a fleet without its watcher. If it is killed anyway, the next `hand update` finishes the steps it left, from its journal at `~/.secondhand/update.json`. A supervisor that could not be resumed after a switch stays in that journal, so the next `hand update`, including the board's timer, resumes it first. A failed update can leave the new Luvus pin in place; a running server ignores it, and the next `hand update` carries on. A failed switch never edits `pin.json` itself: the pin moves before any fleet is visited, and `hand luvus pin BIN` changes it by hand.

## Restore a backup

To restore a backup:
1. stop the board, then the fleet's watcher, so the board cannot start it again: run `hand board stop`, then `hand watch stop` in the fleet (or with `--home DIR`). Each signals a process only when its recorded start time and binary still match, as `hand update` does, and reports `stopped`, or `not running` when none runs or the pid file was stale (it removes that file). If a process is another binary's, it exits 3 and leaves it running; if it is still alive 10 seconds after SIGTERM, it exits 3 too. The rest of this step is the fallback for a machine where `hand` itself is not usable: their process IDs start `board.pid` in `~/.secondhand` and `watch.pid` in the fleet folder, in one line `PID MARKER PATH`, where `MARKER` is the process's start time as Hand read it. A stale file can name an ID that another program now holds, so prove the process is the one that wrote the file before you stop it, and leave a file that fails the check alone. Run the snippet for your OS, then again with `watch.pid`:
   - Linux: the start time must equal `MARKER`, which is what Hand checks:
     ```sh
     same() { [ "$(sed 's/.*) //' /proc/$1/stat 2>/dev/null | cut -d' ' -f20)" = "$2" ]; }
     read pid marker _ < ~/.secondhand/board.pid
     same $pid $marker && kill $pid
     while same $pid $marker; do sleep 1; done
     ```
   - macOS: the start time in whole seconds must equal `MARKER` before its dot:
     ```sh
     same() { [ "$(date -j -f '%a %b %e %T %Y' "$(ps -o lstart= -p $1 2>/dev/null)" +%s 2>/dev/null)" = "${2%%.*}" ]; }
     read pid marker _ < ~/.secondhand/board.pid
     same $pid $marker && kill $pid
     while same $pid $marker; do sleep 1; done
     ```
   - Windows: the start time as a file time must be within one second (10,000,000 ticks) of `MARKER` after `windows:`:
     ```powershell
     $id, $marker, $null = (Get-Content ~\.secondhand\board.pid).Split(' ')
     if ([math]::Abs((Get-Process -Id $id).StartTime.ToFileTime() - [int64]$marker.Replace('windows:', '')) -lt 1e7) { Stop-Process -Id $id; Wait-Process -Id $id -ErrorAction SilentlyContinue }
     ```
2. run `hand supervisor stop` in the fleet if its supervisor runs, at a time when no worker runs, so nothing writes to `hand.db`;
3. remove `hand.db-wal` and `hand.db-shm` next to the fleet's `hand.db`;
4. copy the backup over `hand.db`: a `hand.db.<stamp>` from `hand update`, or a daily `hand-YYYYMMDD.db` from the watcher, both under `$SECONDHAND_HOME/backups/<fleet id>/` (`~/.secondhand/backups/<fleet id>/` unless you set it);
5. put the old binary back from `~/.secondhand/backups/hand.<stamp>` if the new one is the problem (Hand backs up `hand`, not the sidecar: an older `hand` that pins another sidecar version shows `hand-iroh` as failed in `hand doctor` until you install the matching one); an older `hand` refuses a database that a newer one migrated (`newer than this hand`), so step 4 is not optional with it;
6. run `hand open` to start the board again, then start or resume the supervisor from the fleet's page.

## Updating from before 0.13.0

This build migrates `hand.db` only from schema 16, the one Hand 0.13.0 writes. It refuses an older database before it migrates anything, with `PATH has schema N, older than this hand reads (16 to 20); hand 0.13.0 is the last release that migrates it`, where PATH is the database file (a migration that finds the old schema by itself says `state schema N, older ...` instead). This is a break, with no switch to go back. From an older release, install 0.13.0 first (`HAND_INSTALL_VERSION=v0.13.0`, see [Install](install.md)), run `hand orient` in each fleet so it migrates the database (`hand version` does not open it), then update. A `hand update` run by an older release swaps the binary before anything reads the database, so run the 0.13.0 step first. If one already swapped it, every `hand` command that opens the database now refuses it: install 0.13.0 over it with `HAND_INSTALL_VERSION=v0.13.0`, run `hand orient` in each fleet, then run `hand update` again.

## Updating from 0.13.0

`hand update` runs the code of the build you have until it has swapped the binary, and restarts the watcher and the board with that code. From 0.13.0, with both a watcher and the board running, the new watcher migrates `hand.db` first, and 0.13.0 then refuses to open the migrated database to start the board. The update still replaces the binary, backs everything up and refreshes the fleet, restarts the watcher, and then exits 1 with the unit row `board,start,"failed: invalid: state schema N is newer than this hand (16)"` and the help line `could not start board`. Nothing is lost: run `hand open` to start the board on the new binary. With only the board running, or with neither, the update succeeds without that line.

## Workers start without MCP servers

A Claude worker no longer loads the MCP servers of your own Claude config or of a repository's `.mcp.json`: it starts with `--strict-mcp-config` and none. This is a break, with no switch to go back. If your workers rely on a browser or another MCP server, put the servers in `mcp.json` in the fleet home and add `"mcp": "mcp.json"` to those profiles in `routing.json` (see [MCP](vocabulary.md#mcp)); `hand doctor` fails a profile whose file is missing.

## Options

The options:
- `hand update --check` shows what would change and changes nothing; its `units` rows list the stop and start steps a real run would take for each fleet's watcher and the board, each marked `would run`;
- `hand update --channel edge` (or `stable`) moves to the other channel;
- `hand update --keep-luvus` never switches a fleet's Luvus server: a version difference stays `pending: --keep-luvus`, and a current binary returns at once;
- `hand update --auto` is what the board's timer passes ([Update on a timer](#update-on-a-timer)): it does not try a switch again for a pin that already failed;
- a `hand` built from source needs `--channel`.

## Update on a timer

`hand board --update-every 1h` makes the running board start `hand update` every hour, detached, on every OS. It is off by default (`0`), and the least it accepts is `10m`. The automatic run uses the channel the installed binary was built for, so a build from source cannot use it: the board says so once and starts without it. The timer is what makes Hand switch a fleet's Luvus server by itself: each run switches the stale fleets that are quiet and settled (see [What `hand update` does](#what-hand-update-does)), and with a current binary it visits only those. Turn the timer off (`0`), or run `hand update --keep-luvus` by hand, to never switch. A second `hand update` while one runs exits with a message, and the board skips its turn. The last automatic run's output is in `update.log` in `~/.secondhand`. When the update restarts the board, the new board keeps the interval, which the board records in `board.update-every` (the last board start wins: one started without the flag clears it). A turn skipped because an update runs or the lock is held is tried again within 30 seconds. On Linux, add the flag to `ExecStart=` in `~/.config/systemd/user/secondhand-board.service` and add `KillMode=process` under `[Service]`, so that systemd does not end the update when the board stops, then run `systemctl --user daemon-reload` and `systemctl --user restart secondhand-board`:

```ini
ExecStart="/home/you/.local/bin/hand" board --addr 127.0.0.1:7777 --update-every 1h
KillMode=process
```

On macOS add `<string>--update-every</string><string>1h</string>` after `<string>board</string>` in the plist, and on Windows add ` --update-every 1h` to the shortcut's `Arguments`.
