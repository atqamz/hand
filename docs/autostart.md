# Autostart

## Keep the watcher and the board running

`hand supervisor start`, `resume` and `switch` start the fleet's watcher when none runs. In a systemd user session it runs as the transient unit `secondhand-watch-<fleet id>`. Otherwise it runs detached, with its output in `watch.log` in the fleet folder. `hand orient` shows `watch: running` or `watch: missing`. That watcher stops with the user session, and systemd restarts it after a crash, except when it exits with status 3: another watcher already holds `watch.lock`, the fleet is not found, or the pinned Luvus changed. Retrying would not help in any of them. `hand open` starts the board the same way when none answers: `systemctl --user start secondhand-board` when that unit is installed, otherwise the transient unit `secondhand-board`, otherwise detached with its output in `board.log` in `~/.secondhand`. It fails with the log or journal command to read when the board does not answer within 10 seconds. On every OS, the running board also keeps the watchers alive: at startup and every 30 seconds it starts the watcher of each fleet that has a live supervisor, or that has `autoresume` set in `routing.json` and an ended supervisor to resume, when its `watch.lock` is free. A fleet with neither gets no watcher, and a failure to start one is logged once to the board's log and does not stop the board. See [Autostart](#run-the-board-at-login) to run the board at login.

In a systemd user session, Hand starts each fleet's Luvus server in a transient scope (`systemd-run --user --scope`), so the server leaves the cgroup of whatever started it, and restarting the watcher or the board never stops the agents. The scope has no fixed name and nothing restarts the server in it: when the server dies, the watcher's reconnect starts a new, empty one. Without a user session, for example over plain SSH, or when `systemd-run` fails, Hand starts Luvus directly, and its cgroup is its starter's.

## Run the board at login

One autostart is enough: run `hand board` once at login. The board starts every fleet's watcher that needs one, so there is no per-fleet unit. Add `--update-every` to its command ([Update on a timer](update.md#update-on-a-timer)) and it also keeps Hand up to date and switches the Luvus server of each fleet that is quiet; without the flag nothing switches by itself. An autostart that already exists needs the flag added to its command (the unit, the plist or the shortcut), and the board restarted, before it does.

### Linux

```sh
mkdir -p ~/.config/systemd/user
hand unit board > ~/.config/systemd/user/secondhand-board.service
systemctl --user daemon-reload
systemctl --user enable --now secondhand-board
```

The unit starts with your graphical session: it is `After=` and `WantedBy=` `graphical-session.target`, which systemd reaches once the session manager has imported `WAYLAND_DISPLAY`, `DISPLAY` and the rest of the session environment. The Luvus server and every pane inherit that environment, so pinentry can prompt. It has no `PartOf=`, so ending the session does not stop the unit or the agents; they keep running while your user manager does, which after the last logout needs `loginctl enable-linger`. The target is started only by a session manager that integrates with systemd, such as uwsm, GNOME or KDE Plasma. A session that does not start it, and a host without a graphical session such as a headless server, never reaches that target, so the unit never starts there; edit `WantedBy=graphical-session.target` back to `WantedBy=default.target` and delete the `After=` line, then run `systemctl --user daemon-reload` and `systemctl --user reenable secondhand-board`, since the old link in `graphical-session.target.wants` stays until you do.

The unit keeps the `PATH` of the shell you ran `hand unit board` from. systemd starts user units at boot, before your desktop session exports its `PATH`, and the board and the watchers and Luvus server it starts need it to find `claude`, `hand` and your tools. Run `hand unit board` again, and `systemctl --user daemon-reload`, after you install tools into a new folder.

Migrating from watch units: earlier versions printed a unit per fleet with `hand unit watch`, which no longer exists. The board starts the watchers again within 30 seconds, and no Luvus restart or quiet time is needed. For each installed watch unit:

```sh
systemctl --user disable --now secondhand-watch-<name>
rm ~/.config/systemd/user/secondhand-watch-<name>.service
systemctl --user daemon-reload
```

To move from one board per fleet, remove each old board unit the same way, then install the one above:

```sh
systemctl --user disable --now secondhand-board-<name>
rm ~/.config/systemd/user/secondhand-board-<name>.service
systemctl --user daemon-reload
```

### macOS

Run on a real Mac with Hand 0.11.0: the recipe loads, runs the board and respawns the watcher. Still unverified (issue #761 tracks it): `RunAtLoad` at a real login, the Background Items notice that macOS may show at login, and the real `claude` on macOS. Save this as `~/Library/LaunchAgents/dev.secondhand.board.plist`, with the absolute path of your `hand` binary, and a `PATH` that holds `claude`, `git`, `luvus` and your other tools, since launchd gives agents a minimal one:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.secondhand.board</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/hand</string>
    <string>board</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
```

Load and unload it with:

```sh
launchctl load ~/Library/LaunchAgents/dev.secondhand.board.plist
launchctl unload ~/Library/LaunchAgents/dev.secondhand.board.plist
```

The plist has no `KeepAlive`: a second board exits non-zero when it cannot bind the board's address, and `KeepAlive` would restart it in a loop. launchd therefore does not restart a crashed board; the next `hand open` starts it again.

### Windows

To run the board at logon without admin rights, put a shortcut in the Startup folder. This creates it from PowerShell:

```powershell
$link = (New-Object -ComObject WScript.Shell).CreateShortcut("$([Environment]::GetFolderPath('Startup'))\Hand board.lnk")
$link.TargetPath = "$env:LOCALAPPDATA\hand\bin\hand.exe"
$link.Arguments = "board"
$link.WindowStyle = 7
$link.Save()
```

`shell:startup` in the Run dialog opens the same folder, to move or delete the shortcut.
