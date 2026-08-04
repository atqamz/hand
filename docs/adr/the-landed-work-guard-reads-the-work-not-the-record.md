# The landed-work guard reads the work, not the record, and every unresolved question fails closed

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#79, atqamz/secondhand#129
- PRs: none single

## Context

`hand teardown` releases a worktree and a pane, which is what makes a task's uncommitted work unrecoverable.
So it is fail-closed: it refuses unless the work is landed.

The guard was written against the task row, and the row is not always right about the work.

A dirty worktree refused unconditionally.
The no-mistakes gate's own review-fix round routinely leaves a file edited but uncommitted, and the edit often reproduces content the gate's merged fix already carries.
The operator's only way past it was `--force`, which discards work without looking at it.

`kind` is the one field `hand spawn` records that nothing can correct afterwards, since `hand promote` only goes scout to ship.
A scout spawned without `--scout` therefore arrives at teardown as a ship row whose shape - a report, no PR - is exactly what the guard refuses.
Again the only way out was `--force`: forcing past a work-may-not-be-landed guard to fix a metadata typo (atqamz/secondhand#129).

Both are the same failure. The guard was asking the record a question only the work can answer.

## Decision

Where the record is unreliable, the guard reads the work.

**Dirt is compared by content.**
Teardown proceeds past uncommitted changes when every one of them is a tracked modification whose current content already matches the local default branch's tip byte for byte.
The comparison is content-identical and never path-identical: a same-named file with different content, and a path that merely exists in the base, both still refuse.
Both layers a `git status --porcelain` line reports are compared, index and working tree, each where it reports a change, because an `MM` path whose working copy matches the base still holds a third differing version staged.
Untracked files are never safe - there is nothing in the base to compare them against - so their presence refuses whatever else is safe.

**A completed scout deliverable is read off disk.**
With no PR found, no merge evidence on the row, `data/<id>/report.md` present, and the worktree's branch adding no commit to the local default branch, the task is a completed scout regardless of what `kind` says.
Both halves are required and the branch check is the load-bearing one: a ship task whose PR was never opened still carries its commits, so it still refuses, where "no PR and some file exists" would accept it and discard them.
It is decided last, so it can only answer a case nothing else claims, and merge evidence excludes it outright - `hand promote` leaves the report on disk, so a promoted scout that then merged locally has every shape this case reads and it landed as a merge.

**Every unresolved question fails closed.**
A failure to resolve, read or parse a ref refuses.
Resolution is local-only with no fetch, so a stale local ref means a real safe case is missed rather than an unsafe one accepted.

**Ambiguity refuses rather than falling through.**
A branch carrying several PRs that do not resolve to one winner refuses outright rather than degrading to "no PR recorded", because that message means unlanded and this is not that.
The refusal names every PR on the head ref with its repo and state, including ones in losing tiers, since the operator has to resolve the branch rather than the pair that tripped the rule.

**A refusal shows its evidence.**
The dirty-worktree error carries the worktree's `git status --porcelain`, capped at 20 entries plus a count, so the operator is not deciding blind.

## Rejected alternatives

**Let `--force` be the answer in both cases.**
It is the answer to "discard work nobody delivered", and using it for a redundant edit or a mis-filed `kind` overloads it until it means nothing.
Its one meaning has to stay narrow enough to be scary.

**Allow a dirty worktree whose paths all exist in the base.**
Path identity says nothing about content.
It accepts exactly the case that loses work: a real edit to a file that also exists upstream.

**Compare only the working tree, since that is what the operator sees.**
An `MM` path holds staged content that is neither the working copy nor the base, and that staged content is uncommitted work.

**Fetch before resolving the base ref, for accuracy.**
A fetch makes the guard depend on the network, and its failure mode is the wrong direction: a newer remote base makes more dirt look redundant.
Local-only errs toward refusing.

**Make `kind` editable instead, so a mis-filed scout can be corrected.**
That fixes the record and leaves the guard still trusting a field a human typed once.
Reading the work fixes every future instance, including the ones nobody notices to correct.

**Accept a completed scout on the report's presence alone.**
A ship task with an unopened PR has a report often enough, and it also has commits.
Accepting it discards them.

**Decide the scout case first, since it is the cheapest check.**
Deciding it first lets it shadow a real PR or a local-only merge.
Last means it answers only what nothing else claims.

## Consequences

The guard now runs git plumbing rather than reading two columns, so it is slower and it can fail in more ways.
Every one of those ways refuses, which is the accepted cost.

`--force` keeps one meaning, and the checks that were forced past before are now questions the guard can answer.
The ordering between the checks is load-bearing rather than incidental, so a new check cannot simply be appended: it has to be placed against what may shadow it.

The refusal messages carry evidence, which makes them long.
That is deliberate for a fail-closed guard, where a terse refusal sends the operator to `--force`.
