# Submission is observed, not assumed from Text and Enter succeeding

Date: 2026-08-28
Status: accepted
Issues: atqamz/hand#420, atqamz/hand#426
Pull requests: none

## Context

[Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) reaches `submitted` once the external Text and Enter calls both return success. atqamz/hand#420's live reproduction found a `submitted` send whose composer had silently lost bytes mid-transit - two successful RPCs, and a shorter, corrupted message actually delivered - with no signal anywhere in Herdr's response shapes that this had happened: `agent_status` and `state_change_seq` were both measured unchanged across two corrupted live sends, so neither is a usable confirmation signal. atqamz/hand#426 found a second, unrelated way the same two RPCs return success without submitting anything at all: codex's own composer refuses Enter mid-turn, queuing the keystroke instead and leaving the message sitting exactly where it was, which the old rule reads as `submitted` because nothing about the RPCs themselves failed.

Both bugs share one root cause: `submitted` was a claim about two RPCs, not an observation of the terminal. [Liveness is observed, not assumed from launch](liveness-is-observed-not-assumed-from-launch.md) already established the same shape of fix for a different boundary - `hand reconcile` reads Herdr's own state instead of trusting that a launched pane stays healthy - and `internal/runtime/launch.go`'s `confirmLaunch` is the concrete precedent this decision mirrors: act, then poll-observe with a bounded timeout, refusing to declare success without positive evidence.

## Decision

Once Text and the submit key both return success, `internal/steering.composerConfirms` reads the pane's composer back (`herdr pane read --source recent-unwrapped`, chosen over the already-used `recent` because it reverses the pty's own line-wrapping - see `internal/faketool/FIDELITY.md`) and checks whether a recognizable fragment of the sent message is still there. The check is chunked and whitespace-stripped rather than a whole-string or prefix/suffix match: atqamz/hand#420's live reproduction showed a corrupted send surviving as a shifted *interior* slice with both ends missing, which only an any-intact-chunk search catches. The signal is level-triggered - "the composer no longer holds what was sent" stays true once achieved - never edge-triggered.

A send whose composer still holds the message is resent exactly once, identically, before giving up: the operator's own recovery on atqamz/hand#420 and this fix's own live reproduction both found an immediate identical retry sometimes succeeds where the first attempt silently lost bytes. Verification always precedes the retry and never substitutes for it - the retry itself is confirmed the same way, and a still-unconfirmed retry lands the send on `not-submitted`, never `submitted`.

A send hand could not confirm never reaches `submitted`. It lands on the existing four-value vocabulary from the superseded ADR - `not-submitted` when the composer is observed still holding the message, `uncertain` when the confirmation read itself fails - each carrying a `ReasonCode` distinct enough that `store.SendNeedsAttention` can route it to the supervisor, without widening the state enum or touching its schema.

For a codex-harness pane, Enter is replaced with Tab when the pane currently shows codex's own on-screen "tab to queue message" text - a codex UI fact live-verified against a real busy pane, not a general one, so the substitution is conditioned on harness identity and applied nowhere else. A successful Tab-queue echoes the message back verbatim under codex's own "Queued follow-up inputs" label, above the now-empty composer; the confirmation check excludes that label and everything after it before comparing, so a successful queue is never misread as the composer still holding the message.

## Rejected alternatives

Reading `agent_status` or `state_change_seq` as confirmation was tried against the live corrupted sends this decision reproduces and found unchanged across both - unusable as a signal, not merely unverified.

Switching wholesale to `herdr agent prompt` was tested insufficient alone by the scout report this decision implements; it remains available as a building block, not a required replacement for composer-content confirmation.

Clearing the composer before resending, rather than blindly resending the identical text, was rejected: live testing suggested repeated `pane send-text` calls do not simply compound or append, so a plain resend is already safe without an extra clearing step whose own failure mode would need separate handling.

Applying the Tab substitution to every harness, or inferring it from `agent_status` alone, was rejected: the "tab to queue message" UI fact is confirmed only for codex, and no other harness was observed to share it.

## Consequences

`hand send` gains at least one extra Herdr exec per call: the composer-confirmation read. The common case - an idle composer, confirmed on the first read - costs exactly one extra exec for every harness but codex, and exactly two for codex, which reads the pane once more up front to choose Enter or Tab before anything is sent. Only an unconfirmed first read pays for the bounded poll beyond that, and a failed confirmation pays for one retry's own confirmation on top.

A worker that consumes and finishes before hand's first read confirms within the bounded poll and is never mistaken for a failure requiring a resend.

`submitted` now certifies something [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) never could: that the composer was actually observed to let go of what was sent, not merely that two RPCs returned without error. That ADR's `pending`/`uncertain` terminality and lock-ordering decisions are unchanged and still govern; only its account of what `submitted` means is superseded.
