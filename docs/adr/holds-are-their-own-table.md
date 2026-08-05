# Holds are independent of task rows

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#63, atqamz/secondhand#111, atqamz/secondhand#136
- PRs: none single

## Context

A question can remain open after teardown removes its task, and work can be held before a task row exists. A task column or foreign-keyed child row disappears at exactly the wrong boundary.

## Decision

A hold is a standalone row keyed by an arbitrary id, with no foreign key to a task. Teardown does not clear human-authored holds. Machine-authored usage-limit holds are projections of task scheduling state and are cleared only after their kind is checked.

The schema and read/write behavior live in [`internal/store`](../../internal/store), with command and status behavior covered by hold tests.

## Rejected alternatives

- A task column or cascading child row cannot outlive teardown.
- Keeping holds in backlog prose makes the answer depend on somebody remembering to edit it.
- A force flag that clears a hold would let the tool answer an operator's question.

## Consequences

Orphan holds are valid. Reusing an id requires an explicit clear, and every new machine-authored hold kind must define its teardown behavior without overwriting a human hold.
