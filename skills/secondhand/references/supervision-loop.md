# The supervision loop

One bounded loop, not a framework: plan once, dispatch, observe, evaluate, decide exactly one
next action, re-observe, and stop when the goal is actually satisfied.

```text
PLAN
  |
DISPATCH
  |
OBSERVE
  |
EVALUATE EVIDENCE AGAINST THE BRIEF   (references/evaluation.md)
  |
DECIDE EXACTLY ONE NEXT ACTION
  |-- steer            (hand send)
  |-- watch/wait        (hand watch --until-event)
  |-- re-plan/reopen     (hand reopen, or a rewritten brief)
  |-- hold               (hand hold set)
  |-- investigate/reconcile (references/recovery.md)
  |-- deliver            (hand deliver, hand merge)
  `-- escalate to operator
  |
RE-OBSERVE
  |
STOP when the goal/terminal condition is satisfied
```

## Rules that keep this bounded

- Do not loop merely because more improvement might still be possible. A brief has a stated
  goal; once evidence satisfies it, stop, do not keep polishing.
- One targeted correction, then observe its effect, before deciding anything further. Sending
  three steers before seeing whether the first one landed is not supervision, it is noise into
  a busy pane.
- Repeated contradiction between what you expect and what you observe is a signal to re-plan or
  escalate, not to keep steering around it.
- A mechanical worker must not redesign stale assumptions; if the brief's `planned_against` has
  drifted enough that its assumptions no longer hold, that is a re-plan, not a steer (see
  `references/planning-and-briefs.md`).
- Ambiguous machine evidence becomes investigation or repair, never guessed success. If
  `hand status` or `hand reconcile` reports an unknown or unprovable condition, that is
  `references/recovery.md`'s territory, not a coin flip.
- Irreversible or genuinely policy-owned choices go to the operator, every time, regardless of
  how far into a loop you are.

## Deciding the next action

Do not maintain a separate mental precedence table for what to do next: `hand session start`
already reports one, deterministically, in its `next_action_kind`, `next_action_task`,
`next_action_command`, and `next_action_reason` fields. Read those, act on the named command,
and re-run `hand session start` (or `hand status`) after acting rather than assuming the fleet
still looks the way it did before you acted.

```text
wrong: keeping your own running list of "what's next" across a long session
right: re-reading hand session start's next_action_* fields after every action that could have
       changed fleet state
```

## Watching, phase by phase

There is no one universal "actionable events" list to filter on; the right `--event` filter
depends on what phase of the loop you are in.

```text
wrong: always run hand watch --until-event with no --event filter, every time, at every phase
right: while confirming a fresh dispatch actually launched, progress evidence may be what you
       want to see; while waiting for a specific task to reach a terminal state, a narrower
       terminal/error filter is more appropriate; check `hand watch --help` for the current
       event kind list rather than assuming this reference's examples are exhaustive
```

After any wake, re-read fleet state before acting - the event that woke you is one fact, not the
full current truth. A wake can arrive stale relative to what else has happened since.

## Stop means stop

When the goal is satisfied, or when the loop reaches an operator-escalation point, stop acting.
Do not spawn a new Task, reopen another one, or pick up unrelated backlog items just because the
loop is already running and capacity feels available.
