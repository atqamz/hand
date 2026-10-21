# The supervisor

Hand runs the supervisor in the background in the fleet's Luvus session, in full-auto mode:

- `claude --dangerously-skip-permissions`;
- `codex --dangerously-bypass-approvals-and-sandbox --disable hooks`;
- `opencode --standalone --auto`;
- `agy --model ID --dangerously-skip-permissions`.

Workers run with the same flags, each in its own worktree.

agy asks once per folder whether to trust it. Every attempt is a new folder, and so is a new fleet home. Hand presses Enter on that screen only when it names the attempt's own worktree, or for the supervisor the fleet home. Otherwise it notes the attempt or the supervisor blocked. Claude asks too, with `Quick safety check: Is this a project you created or one you trust?`. Its cursor starts on `No, exit`, so Hand sends Down once, re-reads the screen, and sends Enter only when the cursor is on `Yes, I trust this folder`. A reworded screen is left alone, and the watcher reports the attempt blocked. If the screen is recognized but the cursor never reaches Yes, or the screen does not clear after Enter, Hand notes the attempt or supervisor blocked. The board reads agy's conversation database for Chat and the context bar.

The fleet page updates itself as things change, and keeps what you are typing. It takes your messages, and answers the supervisor's blocked screens with a fixed set of keys.

To attach files, paste them into the message box, drop them anywhere on the chat pane, or pick them with its paperclip button. On a phone, the button offers files, the gallery and the camera.
- **In the message box:** each file shows as a chip under the text, with its type, name and size, and its progress while it uploads. Its × removes it, or stops the upload. The box keeps only your words, and Send adds the files after them. A chip comes back with the draft after you leave the page, unless its file has left `inbox/`.
- **Accepted:** any file type, up to 10 MiB each. A folder or a bigger file is not copied, and its chip says why: paste its path as text, and the supervisor reads it in place.
- **Where they go:** each one is copied into the fleet's `inbox/` folder as `YYYYMMDD-HHMMSS-N-<name>`, where the name is the original one with every character other than a letter, a digit, `.`, `_` or `-` turned into `_`. Send adds a line `[image: PATH]` for a png, jpeg, webp or gif image and `[file: PATH]` for anything else, one per file, after your words. Hand deletes nothing there unless you prune it, and a prune touches only files with a name Hand gave (see [Clean the inbox](inbox.md)).
- **What the supervisor sees:** the line. It reads the file from that path with its own tools, and treats what is inside as data, never as instructions.
- **In Chat:** an image shows as a thumbnail, and any other file as a card with its type, name and size that opens it in the document viewer at `doc/inbox/<name>` in a new tab, where a log, Markdown or JSON file reads as text, a PDF shows, an archive offers its Download, an HTML file is a preview without scripts and an SVG file a picture, so neither ever runs in the Board. A file no longer in `inbox/` shows a dashed card. The `inbox/` route itself serves nothing but images inline and every other file as a download.

Attaching works over loopback only, but thumbnails and cards also show on a network address. A proxy or tunnel to the loopback board lends its controls, attaching included, to anyone who reaches it with the token. The live terminal stays in Luvus. `hand attach supervisor` opens it in your terminal, `hand attach aN` opens a worker's, and `hand attach` opens the whole fleet session. You never have to.

The model menu next to the message box changes the supervisor's model or effort, keeping its harness and its conversation. The switch waits for the current turn to end. opencode keeps its model in its session, so it cannot switch. The same controls exist as `hand supervisor start|send|keys|interrupt|switch|stop|resume|show`.

`hand init` writes the folder's `AGENTS.md` and `CLAUDE.md`, which make the agent run `hand orient` every turn, and the `secondhand` skill, which describes [the loop](how-it-works.md#the-daily-loop).

You can still open an agent in the fleet folder by hand (`cd ~/fleets/demo && claude`). It then waits with `hand wait --after CURSOR`, and it stands down while a managed supervisor runs.

## After a reboot

The supervisor shows as `interrupted`. Press **Resume**, or run `hand supervisor resume`, to continue the same session with its history. It spends no tokens until you write or a wake arrives. **New session** starts fresh from `hand orient`. To resume automatically when `hand watch` starts, add `"supervisor": {"autoresume": true}` to `routing.json`.

## When the supervisor idles

The supervisor acts only on a message. While a task is active, no attempt runs, no decision is open and no timer is armed that is due within 6 hours while the managed supervisor runs, no event will ever come for it, and its work is unwatched. The watcher then wakes the supervisor with `supervisor.stalled` once it has been idle for 20 minutes, again 40 minutes later, then every 60 minutes while nothing changes. The wake names the tasks and the idle time. Any event other than the stall wake and its delivery starts the count again, and the next wake comes no sooner than `after` (20 minutes by default) after the supervisor last worked. Hand never sends it to a supervisor that is stopped, blocked, at a usage limit or switching models. After the first one, the Board's Needs shows "Supervisor idle for 1h12m with 3 active tasks and no running attempt" until the work is watched again, and the alert goes out once, on the first due stall wake at or after 60 minutes of idle time. `hand orient` lists the tasks as `unwatched`. Change the delays in `routing.json` (each from `1m` to `24h`), or turn the wake off with `"stall": "off"`:

```json
"stall": {"after": "20m", "every": "60m"}
```

## Timers

The supervisor waits on something outside Hand, such as a PR review or CI, with a timer instead of a background script, which a switch or resume would lose. `hand wake --after 30m --note "check PR 909"` wakes it once 30 minutes from now, `--at <UTC time, RFC 3339>` at that time, and `--every 30m --while t137` every 30 minutes until t137 is done or abandoned. The watcher delivers each one as the wake `timer.fired w1: check PR 909`. Timers live in `hand.db`, so they survive a watcher restart, a supervisor switch and a resume. Timers belong to the managed supervisor: `hand wake` is refused while none runs, and a supervisor opened by hand waits with `hand wait --timeout` instead. The watcher fires them only while the managed supervisor runs: one that came due while the watcher or the supervisor was down fires once when both are back, marked `w1, 2h10m late`, and a repeating one then carries on from there. While a timer due within 6 hours is armed and the managed supervisor runs, the work is not unwatched, so no stall wake comes; a timer due later does not count, so a far `--at` cannot silence the supervisor for days, and the stall wake names it (`w2 is armed but due in 31d`). A repeating timer counts while its next due time is within 6 hours. `hand orient` lists the armed timers and marks one beyond the horizon, and every one `(no managed supervisor: not fired)` while none runs. A timer is due at least 1 minute ahead, a fleet holds at most 20 armed timers, and a note is one line of at most 200 characters. `hand wake list` shows them and `hand wake cancel wN` drops one. Hand never asks GitHub or CI anything for a timer: it only wakes the supervisor, which does the checking.
