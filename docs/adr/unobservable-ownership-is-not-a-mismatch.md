# Unobservable ownership is not a mismatch

- Date: 2026-08-17
- Status: accepted
- Issues: atqamz/hand#245
- PRs: atqamz/hand#248

## Context

`deterministic-reconciliation-observes-before-mutating.md` already states that observation failures never become contradiction evidence.
The treehouse observation path did not implement it.
`worktree.ObserveLease` returned one flat set of states in which "the pool answered and this lease belongs to somebody else" and "the pool could not be observed at all" were the same value, and `VerifyLease` collapsed every non-exact state into `treehouse lease for <path> does not match expected lease <id>`.
Teardown then recorded `teardown_worktree_state = ambiguous` from that single unobservable answer, the store's predecessor table forbade every transition out of `ambiguous`, and reconciliation only moved a worktree resource forward from the empty state.
So one unobservable observation permanently refused teardown before `--force` was consulted, and no supported command converged the task.

The unobservability is not always transient.
Treehouse derives its pool key as `<clone directory basename>-<hash of the origin URL>`, so renaming a GitHub repository moves the key and orphans the pool the live leases were acquired from.
That pool stays unobservable for as long as the remote URL stays changed, which is why a design whose only recovery is "observe again later" recovers nothing.

## Decision

A treehouse lease observation separates answers about the recorded lease from the absence of an answer.
`LeaseExact`, `LeaseAbsent`, `LeaseMismatch` and `LeaseUnprovable` are all answers: the pool was observed and it either confirmed the identity, reported the slot available, named another identity, or offered no identity comparable with the recorded one.
`LeaseUnknown` is the absence of an answer, and it travels to every caller as its own state in `worktree.LeaseObservation` together with the `worktree.LeaseProbe` that failed: the command run, the working directory that selected the pool, and the reason.
`ObserveLease` therefore returns no error at all.
An absent executable, a non-zero exit, unparsable output, an empty pool and a pool that describes other worktrees are all `LeaseUnknown`, because none of them disproves the recorded ownership.

Unproven ownership never authorizes a destructive command, and `--force` is not a way around that.
`--force` still only chooses how a return whose ownership is already proven deals with dirty work.

No durable teardown resource state is recorded from an unprovable observation.
The Task-addressed repair marker is recorded on purpose, names the failed probe, and is cleared when a later observation proves ownership or the resource settles, so it reports an unresolved condition rather than concluding one.
Teardown that cannot observe the pool refuses and writes no resource state, so the next teardown with an observable pool proceeds normally.
`ambiguous` remains reachable from provable contradiction, and it stops being a dead end: both teardown and reconciliation may leave it, but only behind a fresh `LeaseExact` observation of the same lease identity.

For the permanently unobservable pool there is one explicit operator gesture, `hand reconcile <task> --abandon-worktree`.
It requires a task ID, applies through `reconcileHistoricalAttempt` only when a recorded teardown decision makes the attempt eligible, and refuses any observed state other than `LeaseUnknown`.
That decision is what a teardown interrupted at the worktree step leaves behind, so eligibility is not limited by the attempt's active lifecycle; an attempt without a recorded teardown decision is never abandoned.
The abandonment itself runs no treehouse command and records only the worktree resource state.
It records the terminal resource state `abandoned`, which means Hand has relinquished its claim on a lease it can no longer observe, and reconciliation then converges the task through its ordinary path.
The probe that justified the attestation is reported in the reconcile result rather than stored in a new column.

The decision table, the refusal messages, and the convergence traces belong to the tests under `internal/runtime` and `internal/worktree`.
The real command's pool-resolution contract belongs to `internal/faketool/FIDELITY.md` and is rechecked by `tests/contract`.

## Rejected alternatives

- Returning the worktree anyway, or letting `--force` stand in for ownership proof, would let an unobservable pool authorize destroying somebody else's work.
- Retry alone, whether as a `retryable` state or an operator instruction to observe later, does not recover an orphaned pool, because nothing about the pool will change while the remote URL stays changed.
- A separate `hand repair` command would add a second convergence engine beside the reconciler that already owns observe, decide, apply one action, and re-observe.
- Recording operator evidence in a new schema column would add durable state that only the run that produced it can interpret, when the resource state plus the reported probe already say what was relinquished and why.
- Changing treehouse's key derivation, or restoring the previous origin URL, would fix one fleet's accident outside Hand and leave Hand still unable to describe an unobservable pool.

## Consequences

A byte-identical lease identity is never accused of mismatching, and a diagnostic distinguishes "ownership could not be proven" from "ownership was disproven" in the structured result, not only in the rendered message.
An operator who has independently established that a pool is gone has one supported, task-scoped gesture, and it is auditable in the reconcile output.
`--abandon-worktree` adds the worktree attestation without narrowing reconciliation to a single action.
Teardown of a lease whose pool is temporarily unobservable stays retryable forever without operator involvement, because it records nothing.
Attempt lifecycle convergence after terminal worker completion stays with atqamz/hand#239; this record changes only how a worktree resource is observed and settled.
