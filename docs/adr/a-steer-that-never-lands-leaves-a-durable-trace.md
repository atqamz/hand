# A busy composer is waited out, and a steer that never lands leaves a durable trace

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#102
- PRs: none single

## Context

`hand send` types a message into a running worker's pane.
The pane is busy whenever the agent is mid-response, which is most of the time a supervisor has something to say.

The first implementation treated a busy composer as an error and returned immediately.
Every caller then wrote the same retry loop in shell, and the loops were all slightly different.
Worse, two of them racing the same pane lost a steer outright: both saw the composer free, both typed, and one message ended up interleaved into the other.

There is also a failure that looks like success.
The text goes into the composer and the submit keystroke fails, so the message is sitting in the pane unsent, and the process that put it there exits.
Nothing anywhere records that a steer was attempted.

## Decision

A busy composer is the normal arrival state, not an error.
`hand send` waits for it, bounded by `--wait` (default `config/send-wait`, else `2m`), because an unbounded wait is a hang and a zero wait is the shell loop coming back.

A per-task `send:<id>` lock serializes senders, so a second `hand send` waits behind the first instead of racing it on the same pane.
`hand watch`'s usage-limit resume takes that lock without waiting, since it has a whole tick to try again and must not stall behind a long steer.

Whenever the message does not demonstrably land - the wait elapses, the text fails to send, or the submit keystroke fails after the text went in - the message and a timestamp are written to the task row.
A steer that never arrived is a thing the operator has to know about, and the process that attempted it is gone.
The three cases are recorded identically because they are the same fact about the world: a steer with no evidence it landed.

They exit differently, though.
The elapsed wait is exit 6 and its own code, because it is transient and a caller can retry with a longer `--wait`.
The two delivery failures are ordinary exit 1.

The trace is cleared by any later send that reaches the pane, whatever message it carried, since a delivered steer moots an abandoned one.
Failing to clear it warns and still succeeds: the message is already in the pane, and failing there invites a retry that double-sends.

The row lock for the trace is separate from the send lock and short-lived.
A `hand send` waiting out two minutes must not block a `hand status` read or a watcher tick on the same task.

## Rejected alternatives

**Keep failing fast on a busy composer.**
It moves the retry into every caller, and unsynchronized retries against one pane were what lost a steer in the first place.

**Wait indefinitely.**
An invocation that never returns is indistinguishable from a hung one, and there is no upper bound on an agent turn.

**Hold the task-row lock for the whole wait, so the trace write needs no second lock.**
A two-minute wait would then block every reader of that task, including the watcher.
The lock is only needed for the write.

**Keep the undelivered message in memory and print it on failure.**
Printing puts it in a transcript nobody re-reads.
The operator finds out about a lost steer from `hand status`, so the trace has to be in the store.

**Give each failure mode its own exit code.**
Only the retryable one changes what a caller does next.
A distinct code for "the submit keystroke failed" is a code nobody branches on.

**Clear the trace only when the same message is re-sent successfully.**
It keeps a stale trace alive after the operator has said something better, and comparing message text to decide whether a concern is resolved is a guess about intent.

## Consequences

`hand send` is slow by default, and that is the intended trade: the caller's alternative was a loop that was slower and raced.

The undelivered trace is a second place a message lives, so it has to be cleared on the success path.
Forgetting that leaves a permanent warning on a healthy task, which is why the clear failure warns instead of failing.

`--file` exists for the same reason the wait does: a multi-paragraph steer through shell quoting is a correctness problem the caller should not have to solve.
