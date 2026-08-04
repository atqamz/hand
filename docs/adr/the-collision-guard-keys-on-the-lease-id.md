# The worktree collision guard keys on the treehouse lease id, not the path

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#48
- PRs: atqamz/secondhand#132

## Context

`hand spawn` and `hand promote` both acquire a worktree and then cross-check it against every other task row before committing to it.
The guard originally compared worktree paths, which is the field a reader reaches for first because it is the one a human recognizes.

treehouse recycles paths.
A pool slot returned to its pool keeps its directory and is handed straight back out to the next task, under a brand-new `lease_id` that treehouse regenerates on every acquisition, including a same-holder reacquisition of the same slot.
So the path is the one part of a lease that is reused and the identity is the one part that never is.

Keying on the path produced a false positive rather than a missed collision, and the sequence is entirely ordinary.
`hand teardown` returns the worktree before it removes the task's row, deliberately, so a fault in the later step leaves the whole command retryable.
If that removal fails, the row survives naming a path treehouse has already freed.
The next spawn or promote legitimately acquires that path, matches the stale row on path equality, force-returns its own exclusive lease and fails over a collision that never existed concurrently.

## Decision

The guard compares the lease identity treehouse mints per acquisition, `lease_id` from `treehouse get --lease --json`, recorded on the task row.

Path comparison remains the fallback whenever either side has no identity: a task row written before the `lease_id` column existed, or a treehouse older than v2.1.0, which is the version floor for the field.
Existing rows keep being guarded through the migration and gain a real identity as each task is torn down and respawned, so nothing is rewritten in place.

Every task row is compared, done and failed ones included.

The guard is defense-in-depth over `hand`'s own bookkeeping, not the thing preventing two tasks from sharing a worktree.
`worktree.Get` always passes `--lease`, and treehouse's pool lock refuses to hand out a currently-leased slot, so two tasks cannot concurrently hold one path in the first place.

## Rejected alternatives

**Keep comparing paths.**
The path is recycled by design, so path equality answers "has this directory ever been used by another task" rather than "is another task holding it now".

**Filter the comparison to active tasks so stale done rows stop matching.**
Status says nothing about whether a worktree is still held: a task keeps its lease until teardown returns it.
Filtering by status would drop rows that genuinely still hold the slot, turning a false positive into a missed collision.

**Remove the task row before returning the worktree, so no stale row can exist.**
Then a fault in the return leaves a row-less lease nothing will ever release.
The current order is the one that keeps `hand teardown` retryable as a whole.

**Drop the guard entirely, since treehouse's pool lock already prevents the real collision.**
It stays as a check on `hand`'s bookkeeping rather than on treehouse's, and it is cheap.
Note that the older justification for it - that it prevented a stale-lease-after-crash bug, firstmate #947 - was wrong: that bug was pid-based ownership, which `hand` has never used.

## Consequences

`lease_id` is a nullable column with a real fallback path, so both branches need tests and both are exercised by the `internal/faketool` treehouse fake.

A treehouse older than v2.1.0 stays usable and silently degrades to path comparison, which reintroduces the false positive above for that operator.
The version floor is documented rather than enforced.

The retracted firstmate #947 claim is recorded here on purpose.
It read as the guard's justification for long enough that removing the guard looked like removing a crash fix, and it was neither.
