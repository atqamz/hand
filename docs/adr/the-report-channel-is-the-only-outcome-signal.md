# The report channel is the only source of a task's outcome

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#53, atqamz/secondhand#87
- PRs: none single

## Context

`hand` does not run workers.
herdr does, and herdr reports a pane's agent state: `working`, `idle`, `blocked`, `done`, `unknown`.

The design principle everywhere else in `hand` is that dynamic state is queried from the tool that owns it and never copied into `state/hand.db`.
`state/<id>.status` breaks that principle, and it is worth being precise about why, because on first reading it looks like exactly the duplicated-state mistake the principle forbids.

herdr's states answer "is the pane busy".
They do not answer "why did it stop" or "what happened".
`idle` and `done` in particular are the same fact for a headless fleet: whether the pane's harness printed a completion banner a human happened to be present for is not information about the task.

In production that gap meant `done`, `blocked` and `needs-decision` went unreported.
A worker finished, its pane went quiet, and nothing in the fleet could tell that quiet apart from a worker that had wedged.

## Decision

`state/<id>.status` exists as an append-only text file the worker writes and `hand` only ever reads.
Its vocabulary is fixed: `working`, `paused`, `blocked`, `needs-decision`, `done`, `failed`.

It is not a copy of herdr's agent state and does not duplicate any field herdr owns.
It is the only source of task outcome there is, and herdr's state is consulted only for whether the pane is busy.

A `done` line is a claim, not a fact.
It is cross-checked against completion evidence the worker did not produce (a ship task's merge, a scout task's `report.md`) before it is allowed to change anything, and until then it surfaces as `reported-done`.

Only a `hand send` message carries an operator decision.
A worker answering its own harness's question dialog is deciding for itself, and writes that as `working: deciding myself: <call> because <reason>` rather than attributing it to the operator (atqamz/secondhand#87).

## Rejected alternatives

**Infer the outcome from herdr's `idle`/`done` split.**
This is the alternative a future worker is most likely to reach for, because it removes a file and a vocabulary.
It does not work: for a headless fleet the split carries no outcome information at all, only whether somebody was looking.

**Have `hand` persist the outcome to `state/hand.db` and let the worker call a subcommand to set it.**
It makes reporting depend on a working `hand` binary at the exact moment a worker may be reporting that something is broken, and it makes the recovery path a database read rather than `cat`.
See `believe-the-status-file-and-ship-no-hand-dump.md`.

**Let the worker write free-form prose and classify it with a model.**
The vocabulary is six words because a classifier that is right most of the time turns every terminal report into a probability.
Malformed lines are surfaced as malformed, never guessed at and never silently dropped.

**Trust a `done` line on its own.**
Tried, and it is how a task that had not landed anything read as complete.
A worker's belief about its own completion is the least reliable claim in the system, because a worker that is wrong about being finished has no way to know it.

## Consequences

The brief a supervisory agent writes must carry the channel's absolute path and its vocabulary, or the worker never reports.
That is a real coupling between brief-writing and this design, and it is why `hand spawn`'s prompt carries it.

The channel lives and dies with the task: `hand teardown` deletes it alongside the row, so a respawned id does not inherit the previous run's log.

Because the outcome arrives as text a worker appends at will, every reader has to tolerate a partial line, a rewrite in place, and free text after a real report.
Those tolerances are contract, and the reasoning for the hardest of them is in `the-report-offset-is-trusted-only-with-a-digest.md`.

Evidence for a `done` usually lands after the line is consumed, so the verified `done` fires on a later tick than the report.
That deferral is why `done_verified` is durable rather than re-derived; see `the-watcher-persists-what-it-announces.md`.
