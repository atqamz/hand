# A hold is its own row keyed by an arbitrary id, not a column on the task

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#63, atqamz/secondhand#111, atqamz/secondhand#136
- PRs: none single

## Context

"What needs the operator" was answered by hand in `data/backlog.md`, which means it was answered by whoever remembered to write it there.
A hold records that an id is waiting on something, so the answer is derived from the store instead.

The obvious modelling is a column on the task row, or a side table with a foreign key into it.
It fits the common case: a hold is usually about a task.

It does not fit the motivating case the issue names.
`hand teardown` deletes the task row, so a hold set on a task torn down while its question stayed open would vanish exactly when it matters most.
Work with no task row behind it at all - never dispatched, or torn down mid-question - has nowhere to hang a hold.

A second, independent reason pointed the same way at the time.
Before atqamz/secondhand#111, `Open` applied the `schema` constant with `CREATE TABLE IF NOT EXISTS` and had no schema-version mechanism.
That is a correct create against a table that does not exist and a silent no-op against one that exists and is merely missing a new column, with no error and no column added.
Adding a `blocked_on`-style column to `task` would have passed every test, since tests build fresh databases, and silently failed to apply to the one `state/hand.db` on disk.
A brand-new table sidestepped it: every existing database was missing the whole table, so the create branch ran on both a fresh and a migrated home.

## Decision

A hold is a standalone row keyed by an arbitrary id, with no foreign key into `task`.
It survives `hand teardown` of a task with the same id.

Three kinds, and no more invented without a new issue: `operator` waiting on a human, `blocked` waiting on another id named in `blocked_on`, and `limit` waiting on the harness's own quota.

Because a hold outlives its task, id reuse is a hazard, so `hand spawn` refuses a held id with exit 3 and names `hand hold clear <id>`.
Clearing is the explicit step that says the question is settled, and it is the only escape hatch.

`limit` is the one machine-set kind and it is a projection rather than a record.
`hand hold set --kind limit` is refused with exit 2, `hand hold clear` accepts it, and every machine clear checks the kind first so it never answers an operator's question on the same id.
The set direction is guarded the same way and has to be, because a `limit` hold written over an operator's would be deleted along with their question by the machine clear that follows.
`limit` is also the one kind that does not outlive its task, and `hand teardown` releases it.

A hold that cannot be read must never read as nothing waiting.
`ListHolds` and `ReadHold` surface every row as stored, inconsistent ones included, `hand status` flags an inconsistent row rather than rendering it as valid, and a failure to read holds at all is a hard error out of `hand status` rather than an empty list.

## Rejected alternatives

**A `blocked_on` column on the task row.**
Destroyed by `hand teardown` at the exact moment a hold matters, and impossible for an id with no task.
Before atqamz/secondhand#111 it would also have silently failed to apply to the one real fleet home while passing every test.

**A side table with a foreign key into `task`.**
Same teardown problem one layer out, plus a cascade delete that removes the operator's open question as a side effect of cleaning up a pane.

**Keep authoring holds in `data/backlog.md`.**
That file stays out of scope for holds entirely, and a design that finds itself parsing it has gone wrong.
A prose list is a list of what somebody remembered.

**Let `hand spawn --force` clear a held id.**
A `--force`-style flag would be the silent clear wearing a different name.
Answering an operator's question is an acknowledgement `hand` has no business making on their behalf.

**Refuse `hand hold clear` on a `limit` hold too, for symmetry with `hold set`.**
It would make the one hold set on the operator's behalf the one hold they cannot undo.

**Filter inconsistent hold rows out of `ListHolds`.**
Filtering is what lets an external write's mistake disappear from "what is held".
A `holds[0]` block and "the store could not be read" look identical unless the second is a fatal error.

## Consequences

The `hold` table has no referential integrity with `task`, so an orphan hold is a legitimate state and every reader has to treat it as one.

`hand teardown` and `hand promote` both carry kind-checked clears for `limit`, and adding a fourth kind means deciding its teardown behavior explicitly.

`hand spawn` refusing a held id is contract at exit 3, and it is what makes surviving teardown safe rather than a trap.

The schema-version reason no longer applies: an ordinary column addition is now two edits that stay in step.
See `the-schema-version-lives-in-pragma-user-version.md`.
