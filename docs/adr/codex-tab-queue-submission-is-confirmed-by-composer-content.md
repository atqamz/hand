# Codex's Tab queue is confirmed by composer content, Enter is not yet

Date: 2026-08-28
Status: accepted
Issues: atqamz/hand#426. atqamz/hand#420 stays open - see Rejected alternatives.
Pull requests: none

## Context

atqamz/hand#426: codex's own composer refuses Enter mid-turn and queues the keystroke instead, leaving the message sitting exactly where it was - the same "Text and Enter both returned success" claim [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already reaches `submitted` on, with nothing about the RPCs themselves failing.

The same investigation also attempted a composer-content confirmation for the plain Enter path, aimed at atqamz/hand#420's silently-corrupted sends. Live testing against a real codex worker found Enter has no comparable signal: an accepted message stays visible in scrollback as a live-verified ">"-prefixed history line for as long as it sits within the read window, indistinguishable by content alone from a message that never left the composer. The check found a genuinely successful Enter send retained - and, worse, duplicated it via the retry that logic depended on - on the very next live send tried after the one that appeared to work. #420 stays open; this decision covers only what the investigation actually validated end to end.

## Decision

Once Text and the submit key both return success, `internal/steering.submissionOutcome` confirms only when the key was Tab. It reads the pane's composer back (`herdr pane read --source recent-unwrapped`, which reverses the pty's own line-wrapping - see `internal/faketool/FIDELITY.md`) and checks whether a recognizable fragment of the sent message is still there, excluding text under codex's own `codexQueuedMarker` ("Queued follow-up inputs") label: a successful queue echoes the message back verbatim there, which is confirmation, not a stuck composer. The match is chunked and whitespace-stripped rather than a whole-string or prefix/suffix match, in case the queued echo itself wraps oddly. The signal is level-triggered and bounded, mirroring `internal/runtime/launch.go`'s `confirmLaunch` precedent - act, then poll-observe with a bounded timeout, never declare success without positive evidence - with no resend on top of it: an unconfirmed Tab/queue send lands on the existing four-value `SendState` vocabulary (`not-submitted` with reason `composer-retains-message`, or `uncertain` with `composer-confirmation-read-failed`), reachable through `store.SendNeedsAttention` with no schema migration.

Enter gets no composer-content confirmation. Once Text and Enter both return success, the send is `submitted` with reason `text-and-enter-accepted` - the same claim-based rule [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already used - and the pane is never read again to check.

For a codex-harness pane, Enter is replaced with Tab when the pane currently shows codex's own on-screen "tab to queue message" text - a codex UI fact live-verified against a real busy pane, not applied to any other harness or condition.

## Rejected alternatives

Confirming the Enter path the same way was tried and reverted. `composerRetains` cannot distinguish a message still sitting unsent at the live composer from the identical text remaining visible as a `>`-prefixed history line after a genuinely successful send, because Herdr's read window carries both without marking which is which the way `codexQueuedMarker` marks a queue. The failure was not rare - it reproduced on the next live Enter send tried - and its retry compounded it into an actual duplicate message delivered to the worker, not merely a wrong label. Fixing this needs either a narrower read window (whose tradeoffs against atqamz/hand#420's own interior-slice-corruption evidence are unverified) or a positive "the harness reacted" signal in place of an absence check; both are undeveloped and belong to the still-open atqamz/hand#420, not this decision.

Reading `agent_status` or `state_change_seq` as a substitute confirmation signal was tried against live corrupted sends and found unchanged across them - unusable, not merely unverified.

Applying the Tab substitution to every harness, or inferring it from `agent_status` alone, was rejected: the "tab to queue message" UI fact is confirmed only for codex, and no other harness was observed to share it.

Retrying a send that failed to confirm was built, live-tested, and removed: its only justified value was recovering atqamz/hand#420's silent corruption, which is specifically the Enter path this decision does not confirm - a retry with no working confirmation on that path could only duplicate a message that had already landed, never recover one that had not.

## Consequences

`hand send`'s cost is unchanged from before this task for every non-codex harness. A codex-harness pane costs one extra exec to choose Enter or Tab; if Tab is chosen, confirming it costs one more in the common case of an immediate clear, or more within the bounded poll if it is not immediate.

atqamz/hand#426 is closed by this decision. atqamz/hand#420 is not: an Enter send still reports `submitted` on the strength of two RPCs alone, exactly as [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already described, and stays open for a signal that survives an accepted message remaining visible in history.
