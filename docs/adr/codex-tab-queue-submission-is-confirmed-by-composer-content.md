# Codex's Tab queue is confirmed by composer content, so is Enter

Date: 2026-08-28
Status: accepted
Issues: atqamz/hand#426, atqamz/hand#420 (closed by the amendment below), atqamz/hand#459,
atqamz/hand#478 (closed by the second amendment below).
Pull requests: none

## Context

atqamz/hand#426: codex's own composer refuses Enter mid-turn and queues the keystroke instead, leaving the message sitting exactly where it was - the same "Text and Enter both returned success" claim [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already reaches `submitted` on, with nothing about the RPCs themselves failing.

The same investigation also attempted a composer-content confirmation for the plain Enter path, aimed at atqamz/hand#420's silently-corrupted sends. Live testing against a real codex worker found Enter has no comparable signal: an accepted message stays visible in scrollback as a live-verified ">"-prefixed history line for as long as it sits within the read window, indistinguishable by content alone from a message that never left the composer. The check found a genuinely successful Enter send retained - and, worse, duplicated it via the retry that logic depended on - on the very next live send tried after the one that appeared to work. #420 stays open; this decision covers only what the investigation actually validated end to end.

## Decision

Once Text and the submit key both return success, `internal/steering.submissionOutcome` confirms only when the key was Tab. It reads the pane's composer back (`herdr pane read --source recent-unwrapped`, which reverses the pty's own line-wrapping - see `internal/faketool/FIDELITY.md`) and checks whether a recognizable fragment of the sent message is still there, excluding text under codex's own `codexQueuedMarker` ("Queued follow-up inputs") label: a successful queue echoes the message back verbatim there, which is confirmation, not a stuck composer. The match is chunked and whitespace-stripped rather than a whole-string or prefix/suffix match, in case the queued echo itself wraps oddly. The signal is level-triggered and bounded, mirroring `internal/runtime/launch.go`'s `confirmLaunch` precedent - act, then poll-observe with a bounded timeout, never declare success without positive evidence - with no resend on top of it: an unconfirmed Tab/queue send lands on the existing four-value `SendState` vocabulary (`not-submitted` with reason `composer-retains-message`, or `uncertain` with `composer-confirmation-read-failed`), reachable through `store.SendNeedsAttention` with no schema migration.

Enter gets no composer-content confirmation. Once Text and Enter both return success, the send is `submitted` with reason `text-and-enter-accepted` - the same claim-based rule [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already used - and the pane is never read again to check.

For a codex-harness pane, Enter is replaced with Tab when the pane currently shows codex's own on-screen "tab to queue message" text - a codex UI fact live-verified against a real busy pane, not applied to any other harness or condition.

### Amendment - 2026-08-29

atqamz/hand#459 built the positive signal the Rejected alternatives section called for. `chooseSubmitKey`'s pane read is no longer codex-only: it runs unconditionally and its result becomes `submissionOutcome`'s pre-key baseline for the Enter path too, one exec serving both the key choice and the later comparison. `enterConfirms` then polls, at the same cadence `composerConfirms` uses, until a read differs from that baseline *and* holds the sent message's own tail (`composerHasTail`: the last `confirmChunkSize` runes of the message, whitespace-stripped) - never until the message goes absent, which is exactly the check the original attempt reverted. An accepted message remaining visible as history no longer matters, because the poll watches for the tail's arrival and a reaction, not for the sent text's departure.

A large `PaneSendText` can still stall mid-delivery, live-measured to leave an early fragment sitting in the composer for many seconds - the same defect atqamz/hand#420 tracked. `composerHasTail` requires the tail specifically, so a stalled prefix never confirms. Two designs preceded this one and were rejected on evidence: a pre-Enter settle-wait that polled composer growth before pressing the key could not distinguish a short message that had already fully arrived from a long one stalled at the same size, since both stop growing within roughly 150ms; and a byte-size threshold for when to worry about the stall was rejected once natural prose was measured to sustain a materially higher stall boundary than repetitive filler at the same byte count, making any fixed number both unjustified and unnecessary once the check is tail-based rather than size-gated. Chunking `PaneSendText` below the measured stall boundary was considered and filed separately as atqamz/hand#472; this amendment does not implement it.

An unconfirmed Enter send is now split by what was actually observed, not collapsed into one outcome. A composer that never differed from its pre-key baseline is `not-submitted` with reason `enter-not-confirmed`: Enter registered nothing. A composer that changed but never grew the tail is `uncertain` with the same reason: something happened, just not provably all of it, and a stalled fragment is evidence a send is not provably absent, so it cannot carry the certainty a true no-op does. `cmd`'s rendered advice was fixed to match: it now selects the uncertain warning before checking whether the composer holds a partial fragment, because every prior not-submitted-with-a-fragment case had also been not-submitted in state, and that ordering had never been exercised against an uncertain-and-partial outcome until this one existed.

Live-validated against a real codex worker: idle and mid-turn, small and 2-6KB messages, several times each, reading `send_state`/`send_reason` from the store rather than CLI output. Nine sends, zero duplicate deliveries. Two idle-large sends stalled and were correctly not confirmed. A stalled send was also observed to complete later, invisibly, as a side effect of unrelated subsequent traffic to the same pane - reproduced three times, independent of this amendment's own code: it is a property of the transport, not of the confirmation logic, and the same stale-send-completes-later behavior would occur, unobserved, against the unpatched claim-based Enter path too. Fixing that would need the pre-send busy-wait gate to recognize a composer holding an unconfirmed leftover fragment as a form of busy distinct from `AgentStatus`, which touches pre-send gating this amendment does not. It is a known, accepted limitation: neither `not-submitted` nor `uncertain` is ever retried automatically here, and the operator-facing advice for both tells the operator to inspect the pane rather than resend, specifically because of this.

atqamz/hand#420 and atqamz/hand#426 are both closed by this amendment. The Tab/queue path above is unchanged.

### Amendment - 2026-08-29 (atqamz/hand#478)

The Tab/queue path above was itself the same class of bug the first amendment fixed for Enter, for the
same underlying reason: `composerRetains`'s truncation excluded text under `codexQueuedMarker`
("Queued follow-up inputs"), and that marker is not there to exclude once its hosting turn ends and
codex dequeues the message. Caught organically during this task's own live testing, using the unfixed
binary against a real codex worker: a mid-turn `hand send` queued cleanly (marker observed, echoing the
message), then reported `not-submitted` / `composer-retains-message` after the full 10-second poll -
while the pane transcript, read back afterward, showed the shell command run and the reply delivered,
in full, exactly once, with no retry and no duplicate. Direct rapid pane reads bypassing `hand` entirely
then bracketed the mechanism to single-digit milliseconds: the instant the hosting turn ends, codex
drops the queue label and re-renders the same text as an ordinary "›"-prefixed history line -
content-identical to a message still sitting unsent - while a fresh empty composer has already opened
beneath it. A truncation keyed to the label goes blind at exactly that moment, because the label is
gone by the time the history line exists, so nothing excludes the promoted text from the search.

Two fixes were weighed, the two the issue posed. Porting Enter's `composerHasTail` shape verbatim does
not work here: Tab is chosen only while codex is busy, and a busy pane's own spinner and elapsed-time
counter redraw every tick regardless of whether anything was queued, so "differs from a pre-key
baseline" is nearly always true within one poll interval and cannot serve Enter's role of "the harness
reacted." Worse, the tail match itself cannot tell a fragment still sitting at the live composer from
the identical fragment sitting under the queue label or already promoted to history, since all three
are the same text - Tab's problem is where the fragment is, not whether it exists. Patching the
truncation to key on something sturdier than the vanishing label - the issue's other framing - had a
direct answer once the mechanism was in hand: codex always renders the live composer as the final
"›"-prefixed block, busy or idle, in every state observed (queued-and-waiting, promoted-and-processing,
genuinely stuck, and interrupted-back-to-composer). `lastComposerBlock` isolates exactly that block;
`composerRetains`'s Tab check now searches only it, rather than everything up to a label that can
disappear. A message under the queue label still confirms, for the same reason as before: it precedes
the final block, never sits inside it. `codexQueuedMarker` is removed with nothing left referencing it.

`lastComposerBlock` anchors on the *last* `"\n›"`, which raises an obvious question: what if the sent
message's own content contains a line starting with that glyph? The anchor would then fall inside the
message instead of at the composer's true start, `composerRetains` would search only the tail from
there on, and if that tail is short enough to fall under `confirmChunkSize/2`, a genuinely stuck message
could read as not retained - the false-submitted direction the issue calls strictly worse than the bug
it fixes. Checked directly against the rig rather than by reading the renderer: codex indents every line
of a composer or history entry after its first with two leading spaces, whether that line comes from a
literal newline in the sent text or from the terminal's own soft-wrap - and `recent-unwrapped` reverses
soft-wrap breaks before hand ever reads them, so a wrap-induced "›" can never surface as a fresh line at
all. Typed multi-line unsent text directly into a live, uncommitted composer - the glyph on an interior
line, on a short final line, and separated from the first line by a blank line - and every line after
the first rendered with the indent intact, never flush at column 0. Separately swept five candidate wrap
columns with the glyph placed at each and never once saw the unwrapped read produce a raw `"\n›"` from a
soft wrap. A `"\n›"` with nothing between the newline and the glyph is therefore only ever a genuine
entry boundary - the live composer's own start, or a promoted history line's - never something a
message's own content can produce, regardless of what characters it contains.

Live-validated against a real codex worker in a scratch fleet, across 270-plus sends. The confirmed-
delivered half has the organic reproduction above as its live evidence, plus a `send_test.go` case built
from text reconstructed off that reproduction's own post-transition pane read. A live capture separately
bracketed the transition itself to single-digit milliseconds, and hand's own Tab-to-first-read gap is of
a similar order, so aiming a second live `hand send` at that same gap through external timing alone did
not also land an end-to-end pass under the fixed binary within this task's testing budget - the risk
that leaves is bounded by the unit-level proof against the reconstructed real text, not by an
aspiration. The genuine-non-delivery half was reproduced end-to-end under the fixed binary:
interrupting codex's own turn a few hundred milliseconds after a real `hand send --force` pressed Tab
returns the just-queued message to the live composer, unqueued, exactly where it started, and the fixed
check ran its full bounded poll and correctly reported `not-submitted` / `composer-retains-message`
with `RetrySafe` false - confirmed against both the CLI output and a direct pane read afterward. No
poll-window or retry-behavior change of any kind is part of this amendment.

## Rejected alternatives

Confirming the Enter path the same way was tried and reverted. `composerRetains` cannot distinguish a message still sitting unsent at the live composer from the identical text remaining visible as a `>`-prefixed history line after a genuinely successful send, because Herdr's read window carries both without marking which is which the way `codexQueuedMarker` marks a queue. The failure was not rare - it reproduced on the next live Enter send tried - and its retry compounded it into an actual duplicate message delivered to the worker, not merely a wrong label. Fixing this needs either a narrower read window (whose tradeoffs against atqamz/hand#420's own interior-slice-corruption evidence are unverified) or a positive "the harness reacted" signal in place of an absence check; both are undeveloped and belong to the still-open atqamz/hand#420, not this decision.

Reading `agent_status` or `state_change_seq` as a substitute confirmation signal was tried against live corrupted sends and found unchanged across them - unusable, not merely unverified.

Applying the Tab substitution to every harness, or inferring it from `agent_status` alone, was rejected: the "tab to queue message" UI fact is confirmed only for codex, and no other harness was observed to share it.

Retrying a send that failed to confirm was built, live-tested, and removed: its only justified value was recovering atqamz/hand#420's silent corruption, which is specifically the Enter path this decision does not confirm - a retry with no working confirmation on that path could only duplicate a message that had already landed, never recover one that had not.

## Consequences

`hand send`'s cost is unchanged from before this task for every non-codex harness. A codex-harness pane costs one extra exec to choose Enter or Tab; if Tab is chosen, confirming it costs one more in the common case of an immediate clear, or more within the bounded poll if it is not immediate.

atqamz/hand#426 is closed by this decision. atqamz/hand#420 is not: an Enter send still reports `submitted` on the strength of two RPCs alone, exactly as [Steering records terminal submission uncertainty](steering-records-terminal-submission-uncertainty.md) already described, and stays open for a signal that survives an accepted message remaining visible in history. The amendment above is that signal; #420 is closed as of it.

atqamz/hand#478 is closed by the second amendment above: the Tab path's confirmation no longer depends on `codexQueuedMarker` still being on screen, so a hosting turn ending mid-poll no longer reads as a stuck composer.
