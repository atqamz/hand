# Completions use an uncapped append-only file

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#78
- PRs: none single

## Context

Teardown removes live task state, but a completion must remain readable afterwards. The existing watcher event log is bounded and rewritten by rename, which is unsuitable for concurrent short-lived teardown writers.

## Decision

Completions are appended to a dedicated JSONL file under their own lock before the task row is removed. The file is uncapped because it is the durable record of tasks no longer present.

[`internal/completion`](../../internal/completion) owns the format and append operation. Its tests and teardown tests own failure ordering and duplicate tolerance.

## Rejected alternatives

- Sharing the rotating event log can lose one writer's line during concurrent rewrites.
- Removing state before appending destroys the source of the record on a failed write.
- Deduplication adds a read-modify-write cycle to avoid a harmless retry duplicate.
- Keeping completion history only in sqlite removes the plain-file recovery path.

## Consequences

Retries may append equivalent records, and readers must tolerate them. The store grows by one small object per teardown rather than silently discarding old completions.
