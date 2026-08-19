# Every diagnosis names a reachable treatment

- Date: 2026-08-19
- Status: accepted
- Issues: atqamz/hand#254
- PRs: none

## Context

`hand reconcile` answers a Task it cannot converge with `needs-repair` and a `repair_code`.
The codes accumulated one contradiction at a time, and nothing checked that a supported command could leave each one.
Three of them could not be left at all.

A historical Attempt whose Herdr identity is incomplete, or whose observed pane belongs to another identity, latched `teardown_herdr_state = ambiguous` during teardown and then answered `teardown-resource-ambiguous` on every later reconcile.
`herdrCleanupSettled` accepted only `released`, teardown's release path returned early on `ambiguous`, and `SetAttemptTeardownResourceState` refused `abandoned` for any resource other than the worktree.
So the pane resource had no terminal state reachable by any command, and the Task stayed diagnosed forever.
One fleet Task sat in that state for five days after its work had already merged.

A historical worktree with no comparable lease identity answered `legacy-worktree-ownership-unprovable`, which named no command either, because `--abandon-worktree` accepted `LeaseUnknown` alone.
A provisioning Attempt that submitted a launch and persisted nothing answered `provisioning-pane-missing`, whose only exit was `hand teardown --force`, a flag that means "skip the landed-work checks" on an Attempt with no work and no resource to check.

A diagnosis with no reachable treatment is worse than an unhandled error.
It reports a real contradiction, refuses correctly, and offers the operator nothing, which is the pressure that produces a hand-edited database.
atqamz/hand#239 fixed the converse for lifecycles: an Attempt may not become terminal in a way that makes its own cleanup unreachable.

## Decision

Every `repair_code` the runtime can emit has an entry in `repairTreatments` in `internal/runtime/repair.go` naming the supported commands that leave it, and the persisted `repair_reason` carries that treatment text with the Task ID substituted.
What `hand status` and `hand reconcile` show is the text the Task row holds, so the refusal and its way out cannot drift apart.

A treatment falls in exactly one of three classes.
`supported-command`: an ordinary `hand` command leaves the state, attesting to nothing and changing nothing outside Hand first.
`explicit-supported-attestation`: ownership is neither provable nor disprovable, so the only exit is an operator attestation scoped to that one resource, which relinquishes Hand's claim and destroys nothing.
`retryable-after-external-fix`: the contradiction is about the world outside Hand, so reconciling again ends it once that world is fixed.

The classes are named for what the operator must supply, not for how Hand implements the exit, because that is the distinction an operator reading a refusal has to make: run a command, assert a fact only they can establish, or change something outside Hand first.

The pane attestation is `hand reconcile <id> --abandon-pane`.
It takes the shape `--abandon-worktree` already established: an explicit Task ID is required, ownership states an observation can prove or disprove are refused on every route into it, no Herdr command runs, and only the durable resource state is recorded.
`abandoned` becomes a terminal state for the Herdr resource as it already is for the worktree, and `herdrCleanupSettled` accepts it, so reconciliation converges the Task through its ordinary path afterwards.
No pane, tab or workspace is closed, so a pane that answers in place of the recorded one stays exactly where it is.

This extends one sentence of `unobservable-ownership-is-not-a-mismatch.md`, which recorded that `--abandon-worktree` refuses any observed state other than `LeaseUnknown`.
It now also accepts `LeaseUnprovable`.
That is the same category of unsettleable ownership rather than a weakening: a recorded worktree with no comparable lease identity offers nothing for a future observation to compare, so no later reconcile can prove or disprove the claim.
`LeaseExact`, `LeaseAbsent` and `LeaseMismatch` stay refused, and the boundary that record exists to defend is unchanged, because the attestation still returns, prunes and deletes nothing.

Where the facts prove no resource is at stake, reconciliation performs the repair itself rather than naming a command.
`unwindableProvisioning` is that proof for a failed launch: the Attempt is still provisioning, no Herdr identity was ever persisted, no worktree lease is recorded, and no teardown has already decided the Attempt's terminal lifecycle.
Reconciliation then converges the Attempt to `interrupted` under the disposition `provisioning-unwound`, releasing nothing, claiming no ownership and inventing no pane identity.
The Attempt and its history stay durable and readable, and `hand reopen` spawns the task again through the ordinary path.
A provisioning failure that did persist any of those facts is refused by name, so the unwind cannot reach an Attempt that owns something.

A path Treehouse provably reports under a different lease is relinquished the same way, without an attestation, because a disproven claim was never this Attempt's to return.

The enumeration is a test, not a convention.
`internal/runtime/repair_test.go` parses the package's own source for `repairCode` constants, so a new code declared without a treatment, without one of the three classes, or without a case that drives a Task into it and back out through the commands its treatment names, fails the suite.

## Rejected alternatives

- A generic `--force` or `--repair-anything` flag on reconcile would make one gesture answer every diagnosis, which is exactly the unproven-ownership-authorizes-destruction shape atqamz/hand#245 removed.
- Reusing `hand teardown --force` as the exit for a launch that persisted nothing would record "landed-work checks skipped" about an Attempt that produced no work, so the completion record would misdescribe what happened.
- Letting reconcile release a pane whose ownership cannot be proven would close a pane Hand does not own; only Hand's claim is relinquished, never the resource.
- Auto-unwinding any provisioning failure, rather than only one proven to hold no resource, would orphan a lease or a pane whenever the failure happened after acquisition.
- Documenting the treatments in prose alone would leave the invariant unenforced, and the codes drifted for exactly that reason.
- A single `unresolvable` class covering everything that is not a plain command would merge "assert a fact I alone can establish" with "fix something outside Hand and retry", which are different acts by the operator.

## Consequences

No state exists in which Hand answers `needs-repair` with a code no supported command can leave, and adding one fails the tests rather than a fleet.
An operator who reads a refusal is told which command ends it, in the durable Task row rather than only in one run's rendered output.
Refusals did not become weaker: unknown and unproven ownership still authorize no destructive cleanup, and the two attestations relinquish claims without touching a worktree or a pane.
The Herdr resource gains a terminal state it lacked, so a Task whose pane ownership can never be settled converges instead of accumulating.
