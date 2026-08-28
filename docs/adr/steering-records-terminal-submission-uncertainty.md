# Steering records terminal submission uncertainty

Date: 2026-08-15
Status: superseded in part by [Codex's Tab queue is confirmed by composer content, so is Enter](codex-tab-queue-submission-is-confirmed-by-composer-content.md), whose 2026-08-29 amendment redefines what `submitted` certifies for the plain Enter path too, not only the codex Tab/queue send its original decision covered; the `pending`/`uncertain` terminality and lock-ordering decisions below still stand
Issues: atqamz/hand#197, atqamz/hand#176
Pull requests: atqamz/hand#230

## Context

Terminal text injection has a failure window between the external Text and Enter calls and durable state finalization.

An IPC or process failure cannot prove that a request was rejected before the terminal accepted it.

## Decision

Operator and watcher steering use the Attempt-bound send record in `internal/store` through `internal/steering`.

The record is `pending` before the first mutating Herdr call and reaches `submitted` only after Text and Enter both return success.

Structured Herdr pre-enqueue rejection may produce `not-submitted`; all other unresolved post-pending outcomes become `uncertain`.

Uncertain records are terminal and are never automatically resent.

The steering lock is acquired before the task lock, and reconciliation normalizes stale pending records without acquiring the steering lock.

## Rejected alternatives

An outbox, broker, worker acknowledgement, or combined Herdr protocol would either broaden the system beyond terminal submission or claim a guarantee that the PTY boundary cannot provide.

Treating every command error as non-submission would risk duplicate operator messages.

## Consequences

The operator can see the send ID, Attempt, state, and reason after restart.

A crash after terminal acceptance may leave a record that converges from `pending` to `uncertain`, requiring operator judgment before another steer.
