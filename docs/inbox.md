# Clean the inbox

Attachments pile up in `inbox/`. `hand inbox size` shows how many files Hand saved there, their total size, the age of the oldest and of the newest, and what the trash holds. `hand inbox prune --older-than 30d` moves every attachment last modified more than 30 days ago into `inbox/.trash/`, and deletes each file that has been in the trash for more than 7 days. With `--dry-run` it lists both and changes nothing. It prints how many files it moved and deleted, and the space it freed.

- **What it touches:** only regular files directly inside `inbox/` with a name Hand gives (`YYYYMMDD-HHMMSS-N.ext`, or `YYYYMMDD-HHMMSS-N-<name>`, with or without an extension). It never touches a symlink, a folder, an upload still in progress (`.upload-*`) or a file with another name. It stops when `inbox/` or `inbox/.trash` is a symlink rather than a real folder.
- **How it moves:** a rename into `inbox/.trash/` on the same disk, so nothing is copied. Each moved file gets the time of the move, so its 7 days in the trash start then. Until they end, you can move it back.
- **Failures:** a file it cannot move or delete is listed under `failed`, the others still go, and the command exits 1.
- **Repeats:** a second run moves nothing new. Two prunes of one fleet never run at once: the second exits 3.
- **Event:** a prune that moves files records `inbox.pruned` with their count and size (`3 files, 12 MiB`). Deleting expired trash records no event: the move was recorded already.

To prune every day, add this to `routing.json`:

```json
"inbox": {"keep": "30d"}
```

The watcher then runs the same prune once a UTC day. It writes the day to `inbox/.trash/.pruned`, so a restart does not run it again, and a file that failed waits for the next day's run. `keep` takes days, from `1d` to `9999d`. With `"inbox": "off"`, or no `inbox` key, nothing is pruned. A bad value makes `routing.json` invalid, as a bad `stall` does: the error names the field, and the watcher logs it once and does not prune. `hand orient` shows `inbox_folder: "612 files, 140 MiB"` once the inbox holds more than 500 files or more than 100 MiB.
