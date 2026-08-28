# Chunked send delivery is paced by a settle pause, not chunk size alone

Date: 2026-08-29
Status: accepted
Issues: atqamz/hand#472
Pull requests: none

## Context

atqamz/hand#420 and atqamz/hand#459 established that a large `hand send` can leave the composer holding
only a prefix: `PaneSendText` stalls mid-delivery above roughly 1kB of rendered content, a hard stop rather
than a trickle, and #459 made a stalled send finalize honestly (`uncertain` or `not-submitted`) instead of
falsely claiming `submitted`. Neither fixed delivery itself - a channel that correctly reports failure on
this fleet's ordinary 1-3kB steering messages is still a broken channel. atqamz/hand#472 is that fix.

## Decision

Chunking lives in `internal/steering`, not in `internal/herdr.PaneSendText`. `PaneSendText`'s only real
production caller today is `steering.Execute` (`internal/watcher/usagelimit.go`'s `limitPane` interface
only reaches it by passing its client through `steering.Execute`, never directly), and `steering` already
owns every piece the fix needs: `composerHasTail` and the bounded poll-and-confirm machinery #459 built,
the `Client` interface, and the confirm-cadence constants. `herdr` stays what the issue itself calls it - a
one-shot wrapper with no chunking - rather than duplicating confirmation policy one layer down.

`sendMessage` splits the message into pieces of `sendChunkSize` (512) bytes, cut only at rune boundaries,
and sends them with `PaneSendText` one at a time. Each piece is confirmed with `chunkConfirms` before the
next is sent, using the exact tail check `composerHasTail` (#459) already uses to confirm Enter: poll until
the piece's tail appears, never until anything goes absent. The submit key is chosen and pressed only after
`sendMessage` reports every piece confirmed - a chunk that never confirms means `sendMessage` returns before
`Execute` ever reaches `chooseSubmitKey` or `PaneSendKeys`, so a failed chunk cannot become a genuine partial
submission the way an extra nudge could. A message that fits in one chunk takes the exact call sequence it
took before chunking existed: one `PaneSendText`, no confirmation read added, so a short send costs what it
costs today.

`chunkConfirms` checks the tail of the *cumulative* text sent so far, not the isolated chunk. A chunk
boundary landing mid-blank-line - routine in this fleet's Markdown briefs and reviews - strips to an empty
needle on its own, which `composerHasTail` always reports absent by construction; checking the cumulative
text lets the tail reach back into the nearest real content automatically, with no separate empty-chunk
case. Caught in review before this shipped, by inspection rather than by a failing test.

512 bytes is chosen from atqamz/hand#459's own live measurements, not tuned to a boundary already shown to
move: repetitive filler stalled near 1kB, and prose that separately cleared 1965 and 1667 bytes stalled once
concatenated to 3634 bytes. 512 sits under half of the smallest failure seen.

Live validation (real codex 0.147.0, scratch fleet home in a temp directory) found chunk size alone is not
the whole story. A payload built from many near-identical templated sentences - the same "repetitive filler"
shape atqamz/hand#459 already measured as the worst case - reliably stalls once enough of it has accumulated
in the composer, reproduced on three separate fresh panes with no prior scrollback. At 512-byte chunks with
no pause between them the ceiling was about 4.1kB; at 256-byte chunks, about 8.1-11.6kB. Halving the chunk
size roughly doubled the byte ceiling while quadrupling the chunk count, which rules out both a fixed byte
ceiling (it moved) and a fixed chunk-count ceiling (it would have stayed put) - the remaining candidate is a
rate the harness accepts per unit time, and smaller chunks bought headroom only as a side effect of the
extra confirmation round trips slowing delivery down.

`interChunkSettle` (300ms, reusing `sendConfirmPolling`'s existing interval rather than a new number) pauses
before every chunk after the first. Keeping the chunk size at 512 bytes and adding the pause let the exact
12077-byte payload that failed at both 512-byte and 256-byte chunks with no pause deliver whole and correct;
pushed further, a fresh 20159-byte purely-repetitive payload also delivered clean. That is roughly five
times the original chunk-size-only ceiling, confirmed against the real shipped binary, not only the
experimental one the hypothesis was first tested against. The pause is skipped for a single-chunk message,
so it never touches the short-send cost the issue's own acceptance bar protects.

Every realistic payload tried - 1kB and 3kB idle sends (several each), one genuine mid-turn 3kB send under
`--force` while the worker was actively `Working`, and a dedicated 4.5kB payload of varied, non-repetitive
review-style prose - delivered whole, correct, and exactly once, with zero failures, at 512 bytes with the
settle pause. The residual ceiling is specific to adversarially repetitive, mechanically templated text
accumulating past several kB in one message, not to the natural-language traffic this fleet actually sends.

## Rejected alternatives

**Chunking inside `herdr.PaneSendText`.** Every real caller already goes through `steering.Execute`, and the
confirmation apparatus chunking needs (`composerHasTail`, the bounded poll, the `Client` interface) already
lives in `steering`. Moving chunking to `herdr` would mean duplicating that machinery one layer down, or
promoting it out of `steering` for a second caller that does not exist, for no caller `herdr` does not
already serve through `steering`.

**Shrinking chunk size further instead of pacing.** Tried live: 256-byte chunks with no pause pushed the
repetitive-content ceiling from about 4.1kB to about 8.1-11.6kB, a real improvement but not proportional to
the size cut, and it doubles round-trip cost on every ordinary multi-chunk send whether or not that send
would ever have needed the extra margin. The chunk-count-vs-byte-ceiling comparison is what identified the
constraint as a rate rather than a size, and a rate is not a dial chunk size alone can turn - see the
Decision above.

**Confirming per-chunk against the isolated chunk text.** `composerHasTail` reports a message absent when
its whitespace-stripped form is empty, which a chunk landing entirely inside a blank-line run - a routine
shape in Markdown - produces on its own. Confirming against the cumulative sent-so-far text instead makes
the tail reach back into the nearest real content with no special case, and needs no new confirmation
primitive beyond what #459 already built.

**Treating any single sub-chunk-size stall as fully disqualifying (closing this only with a stop-and-escalate
report).** The issue's own verification bar - a 3kB send arriving whole, submitting once, confirmed - was
met repeatedly, including on realistic prose past that bound. An adversarial repetitive-filler payload
failing safely (refused, not corrupted, honestly reported) is a documented residual limitation, not evidence
that chunking fails to solve what the issue is about.

## Consequences

A short send (fits in one chunk) costs exactly what it cost before this change: one `PaneSendText` call, no
added confirmation read, no settle pause. A send needing chunking now costs one `PaneSendText` plus one
confirmation read per chunk, plus a 300ms pause before each chunk after the first - live-measured at about
2.18s for a realistic 3kB send, against about 10.06s for the same shape of send before this change (which
spent that time on a confirmation poll that timed out without ever seeing the full text arrive, and finalized
`uncertain` rather than delivering).

The residual limitation: a message built from long runs of near-identical, highly repetitive text can still
exceed what the harness accepts in one continuous exchange, even chunked and paced. Such a send fails safely
- the submit key is never chosen or sent, nothing is duplicated, and the composer is left holding exactly the
confirmed prefix, with `state.SendReasonTextChunkNotConfirmed` (`not-submitted`, not retry-safe) recorded
honestly. Natural-language steering messages in the sizes this fleet actually sends were never observed to
trigger it.

A failed chunked send still leaves the pre-existing gap atqamz/hand#472's own brief named and explicitly did
not ask this change to close: `internal/steering/send.go`'s busy-composer gate waits on `AgentStatus ==
working`, and a pane holding an unconfirmed leftover fragment reports `idle` for the whole time it sits
there. A subsequent `hand send` to that pane proceeds immediately rather than refusing, at the same cost as
an ordinary idle send - it does not inherit any protection from the failed chunked send that came before it.
