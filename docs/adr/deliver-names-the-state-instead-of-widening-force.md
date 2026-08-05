# A missing terminal state gets its own name rather than a wider `--force`

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#69, atqamz/secondhand#78, atqamz/secondhand#129
- PRs: atqamz/secondhand#135

## Context

`hand teardown` is fail-closed: it refuses to release a worktree and a pane while the work is not landed, because a teardown is what makes the work unrecoverable.

The guard asks one question, "is this landed", and answers it from `merged` and a recorded PR.
Real terminal states exist that the question cannot express.
A contribution offered to a repo the fleet does not control - a fork PR on an upstream - lands when a maintainer decides, possibly never.
A scout task's deliverable is a report, so there is nothing to land at all.
A PR opened by the gate's own `pr` step against a project the fleet does not own is the same shape (atqamz/secondhand#69).

`--force` is already there and it would work.
That is the trap: forcing records the task as `torn-down`, which is indistinguishable from work abandoned unlanded.
The record would then be wrong about the only thing anybody reads it for later.

## Decision

When the guard cannot express a terminal state, the state gets a name.

`hand deliver <id> --reason <text>` records that the work is handed off and the decision to land it belongs to someone outside the fleet.
`--reason` is required, because the record has to say what was delivered and who decides, not merely that something was.

It writes `delivered_at` and `delivered_reason` and nothing else.
It never sets `merged` or `pr_merged_observed`, which both assert the work landed.

The state is keyed off the recorded delivery, never off `kind`, so a task filed as a ship whose deliverable turned out to be a report tears down cleanly without anyone correcting the kind first (atqamz/secondhand#129).

Re-running with a new reason is a correction rather than a conflict, unlike `hand pr`'s one-task-one-PR rule, because nothing consumes the mark until teardown reads it.

## Rejected alternatives

**Widen `--force` to cover the case, or add `--force-delivered`.**
Forcing records `torn-down`, so the fleet's record of a contribution offered upstream becomes identical to its record of abandoned work.
A flag that changes what the record *means* is a state, and giving it a flag name hides that.

**Relax the guard to accept any task whose kind is `scout`.**
The kind is what somebody filed the task as, and it is routinely wrong by the time the work is done.
Keying on the recorded delivery means the correction is one command rather than a kind edit plus a teardown.

**Treat an upstream PR as merged once it is open.**
`merged` asserts the work landed.
A maintainer who closes the PR unmerged leaves the fleet claiming otherwise forever.

**Make `--reason` optional and default it.**
The whole value of the record is what it says.
A defaulted reason is `torn-down` with extra steps.

## Consequences

There is now a general pattern to follow rather than a one-off: when a fail-closed guard refuses a legitimate state, name the state.
Widening the guard is the reflex this exists to displace.

`hand status` surfaces the state in three places - a `delivered` token in the fleet view's `flags`, a `delivered` field in the single-task view, and `delivered_at` / `delivered_reason` in `--json` - because a state nothing renders is a column.

Every future guard-blocking state is a candidate for the same treatment, and each one is a new command rather than a new flag, which is the deliberate cost.
