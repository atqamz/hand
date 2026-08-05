# A silent report channel is its own trigger, bounded by three tiers

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#127
- PRs: none single

## Context

`stale` watches herdr transitions.
A pane that registers no transition at all gives it nothing to fire on, and a worker can sit healthy and quiet indefinitely without one.
That is the shape of a wedged worker: the pane is alive, herdr has nothing new to say, and the report channel stopped growing.

So the channel's own silence has to be a trigger of its own.
The question is what bound to measure it against, since the same duration is alarming for one worker and expected for another.

A worker that reported `paused: waiting on the nightly build` has already explained its quiet.
A worker that reported `done` and still holds a pane is finished and unhurried.
A worker that reported `working: refactoring the parser` and then went quiet is the case the trigger exists for.

## Decision

`parked` fires on the report channel not growing for longer than its bound, independently of `stale`.
The bound is chosen by the last classified report line, and there are three tiers with a config key each:

- `paused`: the long bound, `config/parked-paused-bound`, default 3600s.
- `done` and `failed`: their own longer bound, `config/parked-done-bound`, default 5400s.
- everything else, including `working`, `blocked`, `needs-decision` and no report at all: the short bound, `config/parked-other-bound`, default 1200s.

`done` and `failed` are bounded rather than exempt.
What actually severs a task from steering is the status file being torn down, not the worker's own last word about being finished, so a finished worker still attached to a pane is silence like any other.

The trigger is edge-triggered like every other one: it fires once per silence episode and refires only once the report file grows past the mtime it fired for.
That instant is persisted as `parked_fired_for` rather than re-derived; see `the-watcher-persists-what-it-announces.md`.

The event carries the last report line and its age, and nothing else.
A parked worker and a crashed one are indistinguishable from the status file alone, so the process check is left to the caller.

## Rejected alternatives

**Extend `stale` to cover it, rather than adding a trigger.**
`stale` is defined over herdr transitions, and the case here is the absence of one.
Folding the two together means one threshold answering two questions, and the answer is wrong for whichever question it was not tuned for.

**One bound for every state.**
Tuned short it wakes an operator about a `paused` worker that said what it was waiting for, and about a `done` worker whose pane is simply still open.
Tuned long it is silent for the twenty minutes that matter on a `working` worker.

**Two tiers, reusing the `paused` bound for `done` and `failed`.**
It is the tempting simplification, and it makes the most expected silence in the fleet - a finished worker nobody has torn down yet - share a threshold with a worker that is actively waiting on something.
Three keys cost one config file each and let the expected case be quieter than the explained one.

**Exempt `done` and `failed` entirely.**
A `done` report is a claim, not a fact, and a worker that wrongly believes it finished is exactly the worker whose silence needs surfacing.
See `the-report-channel-is-the-only-outcome-signal.md`.

**Derive the latch from the file's current mtime on each tick instead of persisting it.**
A done task's report file never grows again, so every restart re-fires against the same frozen instant and evicts real history from the capped `state/events.log` (atqamz/secondhand#127).

**Have the event report whether the worker's process is alive.**
The status file cannot tell a parked worker from a crashed one, and a check that guesses would make the event's own claim the unreliable part.
`hand status <id>` and the pane itself answer it on demand.

## Consequences

Three config keys exist that an operator can set independently, and a fleet that wants one bound sets all three to the same value.

A worker whose report channel is growing is never parked, however long it has been busy, which is deliberate: activity is the signal, not progress.

Adding a report state means deciding its tier, and the short bound is the safe default for anything unexplained.
