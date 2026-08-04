# The report offset is trusted only together with a digest, and is the only acknowledgement marker

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#140, atqamz/secondhand#149
- PRs: atqamz/secondhand#150

## Context

`hand watch` tails each task's `state/<id>.status` from a byte offset persisted as `report_offset`, so a restart resumes where it stopped without replaying announced lines or dropping ones written just before it.

The channel is specified as append-only, and workers do not all honor that.
A worker reporting with a truncating `>` redirect rewrites the file in place, and then a byte offset taken from the old content is pointing into the middle of a line that no longer exists.

The first fix was a newline check: every offset the reader persists sits immediately past a newline, so an offset whose preceding byte is not a newline is stale and tailing restarts from the beginning.
Without it, the fragment read from mid-line classified as a `malformed report` naming a healthy worker and quoting a mid-word slice of that worker's own well-formed report (atqamz/secondhand#140).

The newline check is necessary and not sufficient.
When the rewrite's total length happens to equal the offset, the offset sits at the end of the file with the file's own final newline behind it, which is byte-for-byte what "nothing was appended" looks like.
Reports are one line of house-style prose and consecutive ones run within a few characters of each other, so this is a matter of time rather than a contrived input (atqamz/secondhand#149).

The cost of that collision is not a missed wake.
Deferred verification is gated on the last recorded report state, so a same-length `done:` rewrite means a worker that finished is never announced as finished.

Separately, `hand status` needs to answer whether a terminal report reached anybody at all, so that a `done` with no session, no watcher and no notify hook is visible rather than merely eventual.

## Decision

`report_offset` is trusted only together with `report_digest`, a digest of exactly the bytes the offset consumed.
The pair is one value: a digest that no longer matches discards the offset with it and tailing restarts from the beginning.

The digest covers the consumed prefix only, never the unconsumed tail a worker may still be writing.
An empty digest, from a row written before the column existed or a task whose worker has not reported yet, falls back to the newline check alone, so an upgrade replays nothing.

`report_offset` is also the acknowledgement marker, and there is no second one.
Advancing it already means announced, because the poll loop persists it only after the tick's events are announced, and every announcement reaches `state/events.log` and the notify hook whether or not it reached anyone's stdout.
A terminal line past the offset reached nobody; one behind it reached at least the durable log.

Where the two readers must part company, they do so in the direction of a duplicate: an unterminated trailing terminal line is left unconsumed by the watcher and still counted as unacknowledged by `hand status`.

## Rejected alternatives

**Detect the rewrite from the file's mtime or inode.**
An mtime is granular enough to miss a rewrite inside its own resolution.
An inode is unchanged by precisely the in-place rewrite at issue.
Neither reads the thing that actually changed, which is the bytes.

**Keep the newline check alone and accept the same-length case as unlikely.**
The inputs are one-line prose reports written by the same generator, so consecutive lengths cluster rather than spread.
The failure is silent and terminal: a completed task never announced as complete.

**Digest the whole file rather than the consumed prefix.**
Then a worker appending a new line invalidates the offset that correctly describes everything before it, and every append tails from the beginning.

**Add an `acknowledged` column.**
It would store the same fact twice with a way for the two to disagree, and the derived reading is already exact.
`report_digest` is not that second marker: it records nothing about what reached anyone, only whether the offset beside it still describes the file it came from.

**Reject a stale offset by erroring rather than restarting from the beginning.**
Re-announcing a report the operator has already seen costs a wake.
A fabricated malformed report costs a wake and misrepresents the worker, and a wedged watcher costs the whole fleet.
Duplicate over silence is the standing direction here.

## Consequences

Every write of `report_offset` must write `report_digest` in the same statement, or the pair stops being one value.

A rewritten channel reads as entirely unacknowledged, which is correct: no watcher announced a line of it.

A worker mid-append is flagged `unacknowledged` for the moment its line is incomplete.
That is the safe direction, the same one taken when a watcher is denied the task lock.
