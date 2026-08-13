# Tasks are durable and Attempts own execution

- Date: 2026-08-13
- Status: accepted
- Issues: atqamz/hand#193
- PRs: none single

## Context

A task's logical identity and history must survive the disposable worker process, worktree, pane, and harness invocation that execute it.
The earlier [completion-store decision](the-completion-store-is-an-uncapped-append-only-sibling.md) treated teardown as removing the task row, which made the completion file the only durable record after cleanup.

## Decision

SQLite stores logical work in `task` and each execution incarnation in `attempt`.
A task has one lifecycle, an authoritative active-attempt relation, and a report cursor that remains continuous across attempts.
An attempt has its own ordinal, lifecycle, execution identity, resources, pane-scoped bookkeeping, and usage-limit state.
The database partial unique index permits at most one provisioning or running attempt for a task.
Terminal attempts are immutable with respect to activation.
Spawn creates a task and Attempt 1, promotion preserves the scout attempt and creates the next attempt, teardown terminalizes both rows without deleting them, and `hand reopen` explicitly creates a new attempt for a terminal task.

## Rejected alternatives

- Keeping execution columns on `task` makes promotion overwrite history and makes a terminal row look runnable.
- Deleting task rows at teardown loses logical lineage and makes ID reuse ambiguous.
- Reusing a terminal attempt resurrects a disposable execution identity and its pane-scoped latches.

## Consequences

Single-task status can inspect terminal tasks and bounded attempt history, while fleet status remains focused on open tasks.
Completion JSONL remains useful as an independent audit and recovery channel, but it is no longer the sole durable record after teardown.
Conditional transitions, crash evidence, and orchestration extraction remain later work in #194 and #195; profile and route snapshots remain later work in #215.
