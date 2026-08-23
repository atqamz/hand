# Task lifecycle

## The commands, in normal order

```sh
hand spawn <id> <project> [--scout] [--profile <name>]     # launch a worker
hand status [id]                                            # fleet overview, or one task's detail
hand orient                                                 # first Hand read of every reasoning turn
hand watch --until-event [--event <kinds>] [--timeout <d>]  # wait for the fleet's next actionable event
hand send <id> <message>                                    # steer a running worker
hand hold set <id> --kind operator --reason <text>           # waiting on the operator
hand hold set <id> --kind blocked --reason <text> --blocked-on <other-id>
hand hold clear <id>                                          # resolved; resume normal tracking
hand deliver <id> --reason <text>                            # handed off; landing is someone else's call
hand merge <id> [--squash|--merge|--rebase|--local]           # land a task's completed work
hand teardown <id> [--force]                                  # clean up after landing (or --force if abandoned)
hand reopen <id>                                              # start a new attempt on a terminal task
hand promote <id>                                             # turn a completed scout into a ship task
```

## Reading `hand status`

`hand status` with no ID shows the fleet overview; `hand status <id>` shows one task's full
detail, including its report history. Use `--fields` to pick columns and `--full` for the
untruncated single-task report line. A worker reports its own state on the `state` column via
the vocabulary below; `reported` tracks the last report keyword seen; `flags` surfaces anything
needing attention (for example `gate-absent`, `parked`, `merged-external`).

## The report vocabulary a worker uses

Workers report with `working:`, `paused:`, `blocked:`, `needs-decision:`, `done:`, or `failed:`.
`needs-decision:` is reserved for what a worker cannot take back; anything reversible, it
decides itself and says so in first person - `working: deciding myself: <the call> because
<reason>`.

wrong: recording your own harness's answer to its own confirmation dialog as an operator
decision.

right: only a `hand send` message you actually sent counts as an operator decision; if you
answered your own harness's prompt, that was you deciding, and it is reported that way.

## Watching the fleet

`hand watch --until-event` observes the fleet's already-actionable state first - so a condition
that arrived while nothing was watching still wakes it - then waits for the next event and
exits 0. Exit 8 means interruption and exit 9 means takeover replacement; neither is a fleet
event. Re-arm it after acting on an event, or when intentionally resuming monitoring. See
`references/supervision-loop.md` for which `--event` filter fits a given phase - there is no one
universal filter.

## Holds, delivery, and teardown

- Waiting on the operator or on another task is a hold, not a note in a file you maintain
  yourself: `hand hold set` records it, `hand hold clear` resolves it.
- `hand deliver <id> --reason <text>` records that work is handed off and landing it is someone
  else's call - use it before `hand teardown --force`, which is otherwise for work nobody
  delivered.
- Never force-teardown without explicit authorization, and never merge without explicit
  authorization either; both are `AGENTS.md` invariants this skill does not relax.
- Run `hand teardown <id>` once work has actually landed. `hand reconcile` (see
  `references/recovery.md`) is the tool for ambiguous or interrupted lifecycle state; teardown
  itself does not resolve ambiguity.

## Never edit files under `projects/`

Workers make their changes in their own isolated worktrees. Editing a registered project's
mirror directly bypasses that isolation and is one of `AGENTS.md`'s standing invariants.
