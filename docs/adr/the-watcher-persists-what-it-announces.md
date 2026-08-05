# The watcher persists what it announces, and forgets what the pane anchored

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#75, atqamz/secondhand#127, atqamz/secondhand#128
- PRs: atqamz/secondhand#125, atqamz/secondhand#138

## Context

`hand watch` holds an in-memory `TaskState` per task and is restarted constantly.
`--until-event` restarts on every delivered event by design, so on a busy fleet the process lifetime is shorter than most of the intervals it measures.

Every fact in that struct therefore needs an answer to "what happens to it on restart", and there are only two: persist it, or re-derive it.
Getting that wrong produced a family of defects that all look different and share one cause.
Re-deriving `status_changed_at` from the resume time erased a real dwell on every restart, silencing `stale` for exactly the fleet it exists to watch (atqamz/secondhand#75).
Re-deriving the `parked` latch re-fired against a frozen instant and evicted real history from the capped `state/events.log` (atqamz/secondhand#127).

`hand promote` is the second axis, and it is sharper.
It rewrites the task row in place, keeps `id` and `created_at`, and gives the task a **new herdr pane**.
A `created_at` identity check therefore passes untouched, and a running watcher writes its cached copy of the scout's facts straight back onto the freshly-rewritten ship row on the next tick.
A promoted ship inherited the scout's last report as if it were its own.

## Decision

**Anything the watcher announces is persisted at the moment it announces it, never re-derived on restart.**
`report_offset` and `report_digest`, `pr_merged_observed`, `done_verified`, `last_report_state` and `last_report_note`, `parked_fired_for`, and `usage_limit_retry_at` with `usage_limit_attempts` are all written after their line goes out.

A fact may be re-derived only when re-deriving it costs at most one duplicate announcement against a clock that keeps moving.
The `stale` latch and the first-sighting outage latch qualify; the dwells they are measured against do not, and are persisted.

Where duplicate and silence are the only two options, the choice is the duplicate.
The single exception is the usage-limit schedule, where the duplicate is a steer typed into a live pane rather than a line on stdout, so an unparseable stamp resumes unlimited.

**For a cached fact the governing question is not "is it durable" but "was it anchored to the pane."**
Both halves are handled: `hand promote` clears the durable fields itself, because no watcher may be running, and a running watcher explicitly forgets its in-memory copies, because the identity check cannot see a promote.

A dwell timestamp is trusted only alongside the status it describes, persisted as `status_changed_for`, since a status observed in a different pane is a new dwell even when it spells the same word.

Anything added to `TaskState` is classified in `SPECS.md`'s "What survives a `hand watch` restart" table before it ships.

## Rejected alternatives

**Re-derive everything on restart and keep no watcher bookkeeping in the store.**
It is the smaller schema and it is how the defects above happened.
Evidence that lands while the watcher is down makes the restarted process conclude the line already went out, so the announcement is skipped silently.

**Persist every latch, so nothing can ever duplicate.**
Each persisted latch is a column that must be cleared on teardown and on promote, and a latch never cleared is a signal that never fires again.
Persisting a latch whose clock keeps moving buys nothing: the condition has to genuinely re-mature before it can fire twice.

**Use `created_at` alone as task identity and let it cover promote too.**
Promote deliberately keeps `created_at`, because the task is the same task to an operator.
So identity cannot be the mechanism, and pane anchoring has to be asked about field by field.

**Let `hand watch` clear the promoted task's stale facts rather than having promote do it.**
A watcher may not be running when a promote happens, and then nothing clears them at all.

**Rate-limit duplicate notifications instead of persisting the `parked` latch.**
Rate limiting hides the symptom and leaves the frozen-instant re-fire in place.
A done or failed task's report file never grows again, so there is no later edge to rate-limit toward.

## Consequences

The restart table in `SPECS.md` is contract for anyone extending the watcher, and a new `TaskState` field that is not in it is an unreviewed decision.

`hand promote` and `hand teardown` both carry clearing logic that has to grow with the watcher's durable fields.
A new durable field means an edit in three places: the store schema, promote's clear, and the watcher's in-memory forget.

`report_offset` surviving a restart is what makes `--until-event`'s second baseline tick necessary, since those unconsumed lines are new to the poll loop but not to the file.
