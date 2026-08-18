# Deterministic reconciliation observes before mutating

- Date: 2026-08-15
- Status: accepted
- Issues: atqamz/hand#196
- PRs: atqamz/hand#229

## Context

Hand crosses SQLite, Git, treehouse, herdr, and worker-process boundaries without a distributed transaction.
A crash can leave durable lifecycle evidence ahead of, behind, or contradictory to external reality.
Re-running an external command blindly can launch duplicate workers, return a reused worktree lease, or close an unrelated herdr resource.

## Decision

SQLite is durable intent and history.
Git, treehouse, herdr, and processes are observed evidence.
Reconciliation compares those independently owned facts through a bounded observe, decide, apply-one-action, and re-observe loop.
The decision table is deterministic, and repeated reconciliation against unchanged reality converges.

Repair metadata is Task-addressed and orthogonal to Attempt lifecycle.
It stores a stable machine-readable code, bounded reason, relevant Attempt ID, and observation timestamp.
The marker remains until the same contradiction is proven gone or a safe lifecycle transition resolves it.
Observation failures never become contradiction evidence and do not clear an existing marker.
Unknown Herdr failures follow the same rule, including when a running Attempt's worktree is dirty.

An existing Attempt is never rerouted during reconciliation.
Its persisted harness, model, effort, execution class, planned-against commit, requested profile, and routing source remain the only execution identity.
There is no automatic worker, harness, model, or profile fallback.

Automatic resource cleanup requires exact ownership proof and a clean worktree.
Dirty worktrees, incomplete or reused identities, and ambiguous launch evidence become `needs-repair`.
A running Attempt whose Herdr pane is observed absent instead converges to a terminal lifecycle from observed landing evidence, without an operator running `hand teardown`; it becomes `needs-repair` only where that landing itself cannot be observed.
Released historical Herdr resources remain attributed anomalies when matching live identities are observed; reconciliation reports them without closing the resource.
The reconciler does not run as a daemon and does not implement the send protocol.

The detailed recovery matrix belongs in the reconciliation decision and observation tests under `internal/runtime`.
Command rendering stays in Cobra, and status remains read-only.

## Rejected alternatives

- Treating SQLite as a complete model of external reality would make stale rows authorize destructive actions.
- Treating every external error as absence would turn service outages into false repair decisions.
- Re-routing or relaunching an existing Attempt would create a second execution identity and duplicate side effects.
- A daemon or generic controller would add lifecycle ownership and scheduling scope that this explicit CLI use-case does not need.

## Consequences

`hand reconcile [id]` is the explicit mutation boundary for recovery.
The command can safely progress through several durable checkpoints in one invocation while the iteration guard prevents a logic loop.
Ambiguous ownership is resolved only by a fresh exact observation or an operator action explicitly supported by the owning resource decision, and dirty work remains manual.
A worker that disappears while running is not silently replaced.
`unobservable-ownership-is-not-a-mismatch.md` adds the one supported gesture for a treehouse pool that can no longer be observed at all.
