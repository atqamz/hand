# Completions use an uncapped JSONL file

- Date: 2026-08-04
- Status: accepted, superseded in part by [Tasks are durable and Attempts own execution](tasks-are-durable-and-attempts-own-execution.md) and by [A project is identified by a surrogate id, not its name](a-project-is-identified-by-a-surrogate-id-not-its-name.md), which removes the project-rename exception below
- Issues: atqamz/hand#78
- PRs: none single

## Context

Teardown removes live task state, but a completion must remain readable afterwards. The existing watcher event log is bounded and rewritten by rename, which is unsuitable for concurrent short-lived teardown writers.

## Decision

Completions are appended to a dedicated JSONL file under their own lock before the task row is removed. Project rename is the one controlled exception: it may atomically rewrite only records belonging to the renamed project, under the same lock, so rollback cannot overwrite unrelated records. The file is uncapped because it is the durable record of tasks no longer present.

[`internal/completion`](../../internal/completion) owns the format and append operation. Its tests and teardown tests own failure ordering and duplicate tolerance.

## Rejected alternatives

- Sharing the rotating event log can lose one writer's line during concurrent rewrites.
- Removing state before appending destroys the source of the record on a failed write.
- Deduplication adds a read-modify-write cycle to avoid a harmless retry duplicate.
- Keeping completion history only in sqlite removes the plain-file recovery path.

## Consequences

Retries may append equivalent records, and readers must tolerate them. The store grows by one small object per teardown rather than silently discarding old completions.
