# The completion store is an uncapped append-only log, not a share of `events.log`

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#78
- PRs: none single

## Context

`hand teardown` removes the task's row, and after that the fleet holds no record that the task existed.
`hand status` shows the live fleet and never history, by design.
So something has to outlive the row, or "what happened to that task" has no answer at all.

`state/events.log` was already there and looked like the place for it.
Its writer reads the whole file, appends, and rewrites via a temp-file rename.
That is fine for one long-lived writer, which is what `hand watch` is.
Teardown is not: it is a short-lived process that can genuinely overlap a running watcher, and two read-modify-write cycles racing means one process's rename lands over the other's and a line is gone.

## Decision

`state/completions.jsonl` is a sibling of `events.log`, not a share of it.
It takes a dedicated lock and performs one `O_APPEND` write per record - no read, no rename, nothing a second writer can clobber.

The record is appended **before** the task's row is removed, because the record is derived from the state that removal takes out from under it.
The two sides of that ordering fail in deliberately different directions.
If the append fails, nothing was recorded and nothing was touched, so the whole command is retryable.
If the removal after it fails, the record already written is not thereby wrong: everything it claims was independently true earlier in the same run.
The row is left in place, so a retry replays the command and appends a second functionally duplicate record.
A harmless duplicate is the deliberate trade against ever silently losing a completion.

The store is uncapped.
It is the only durable record of a completion, so keeping the last N entries throws away the answer to the question it exists for.

Each line is a complete JSON object - `id`, `project`, `kind`, `outcome`, `detail`, `torndown_at` - readable without parsing prose.

`outcome` ranks `delivered` ahead of every outcome that asserts the work landed, but only while the row carries no merge.
A delivered task has to stay distinguishable from a merged one in the permanent record, or the fleet's history claims upstream merges that never happened (atqamz/secondhand#78).
A delivery an upstream maintainer then really did merge records `merged`, because that is the stronger of the two true facts and the requirement is only that the record never claim a merge that did not happen.

Teardown's own output carries the record's own `outcome` and `detail` fields, so what the command says and what the permanent record holds cannot drift.

## Rejected alternatives

**Write completions into `events.log`.**
Its read-modify-write rename loses a line whenever a short-lived writer overlaps the watcher, and teardown is exactly that.
One append-only file per writer pattern is cheaper than making the existing writer concurrent.

**Remove the row first, then append.**
The record is derived from the row.
Removing first means either holding the derived values in memory across a fallible write, or appending a record with fields missing.

**Make the duplicate impossible with a completion id and a dedup read.**
A dedup read is the read-modify-write cycle that this format exists to avoid, and it buys the removal of a harmless duplicate.

**Cap the store, or rotate it.**
Its whole purpose is answering a question about a task that is gone.
A rotated store answers it for recent tasks and silently stops answering it for the ones far enough back to be worth asking about.

**Store completions in the sqlite database instead.**
A completion is append-only, read rarely, and needs to survive anything that goes wrong with the schema.
A flat file is readable with `cat` when the database is the thing being debugged.

**Rank `merged` above `delivered` unconditionally, since merging is stronger.**
The row's `merged` flag is set by `hand merge` and by observation, and a delivered contribution is not merged by either.
Ranking merge first would record a delivery as a merge that never happened, which is the one error this outcome exists to prevent.

**Have teardown print a summary of its own and let the record be internal.**
Two renderings of one event drift, and the printed one is the one an operator quotes back.

## Consequences

There are now two durable log files in `state/` with different concurrency models, and the difference is not obvious from looking at them.
Anything that starts writing completions has to take the lock and append rather than imitating `events.log`.

The store grows without bound.
That is accepted: it is one line per torn-down task, and the failure mode of the alternative is losing history.

A retried teardown can leave duplicate records, so any future reader has to tolerate them rather than assuming one line per id.
