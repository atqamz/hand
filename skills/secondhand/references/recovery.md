# Recovery

## The one rule under all of this

Hand never converts ambiguity into guessed success. Where it cannot observe something, it says
so - `unknown`, `needs-repair`, or an explicit unprovable condition - rather than assuming the
best case. Read that signal as exactly what it is, and do not resolve it with an assumption of
your own either.

## `hand status`'s repair fields

A task's `repair`, `repair_code`, `repair_attempt`, `repair_reason`, and `repair_observed_at`
columns (`hand status <id> --fields ...`) record when Hand itself found a condition it could not
resolve deterministically. A populated `repair_code` is Hand telling you it hit a boundary, not
a bug to route around - read `repair_reason` before deciding anything.

## `hand reconcile`

`hand reconcile [id]` converges durable task state with observed external reality: a running
Attempt whose Herdr pane is observed absent reaches a terminal lifecycle here if it can be
landed from evidence (an observed merge, a recorded delivery, or a scout report on disk);
positively observed unlanded work interrupts it instead. Where landing cannot be observed at
all, the result is `unknown` - no lifecycle value is invented.

Three flags attest to a specific unprovable fact and require an explicit task ID; read
`hand reconcile --help` in full before using any of them, since each refuses whenever anything
on record could still prove or disprove the fact it attests to:

- `--abandon-worktree` - Hand relinquishes a Treehouse lease it cannot establish ownership of. It
  never returns, prunes, or deletes the worktree itself; the operator reclaims it through
  Treehouse directly.
- `--abandon-pane` - the same attestation for a Herdr pane identity; it closes no pane, tab, or
  workspace.
- `--attempt-never-started` - attests that a running Attempt's worker took no turn at all.

wrong: treat `--abandon-*` as a general "unstick this task" hammer.

right: use exactly the flag matching the specific unprovable fact, only after confirming the
observation genuinely cannot resolve it - each flag refuses on its own if it can.

None of these flags repair a live worker's resource; an active attempt is only reached through a
recorded teardown decision. If the task is not actually stuck, `hand reconcile` without any of
these flags is the right first move, since it may resolve the state from evidence alone.

## Uncertain sends

`hand status`'s `send_state` (for example `submitted`), `send_origin`, and `send_reason` columns
record what Hand knows about the *delivery* of a message, not what the worker did with it.

```text
wrong: send_state is submitted, so the worker acknowledged or acted on the message
right: submitted only means Hand's own delivery mechanism accepted it; observe the worker's next
       report or state change before assuming it landed
```

Never blindly resend an uncertain send. A duplicate send into a worker that already acted on
the first one can compound the confusion rather than resolve it. Observe first (re-read
`hand status`, consider the pane), and only resend if the evidence genuinely shows the first one
did not land.

## Crash and restart

Hand's durable state is `state/hand.db`; a supervisor restart does not lose Task/Attempt
history. On restart, run `hand doctor` then `hand status` before acting on anything - do not
assume the fleet is in the state you last remember, since a worker's own state can have changed
while nothing was watching.

## Immutable Attempt execution snapshots

An Attempt's recorded execution details (harness, model, effort, `planned_against`, worktree)
are fixed at spawn time and describe what actually ran, not a live configuration you can edit in
place. If a task needs different execution parameters, that is `hand reopen` or `hand promote`
starting a new Attempt, not an edit to the old one's record.
