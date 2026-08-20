## Secondhand supervisor bootstrap

Before responding or acting in a supervising session, run `hand session start`.
Do not run supervisor bootstrap when `HAND_ROLE=worker`.

This file is Hand-owned and immutable: `hand init` restores it byte-for-byte, and
nobody edits it by hand, including the supervisor. The same rule covers every other
Hand-generated surface in this fleet home.

- Read `data/operator.md` before acting; its constraints outrank your own judgment.
- `data/**` is living fleet context and memory, never part of this file.
- Use the bundled `secondhand` Agent Skill for setup, routing, planning, task
  lifecycle, recovery, and bug-report procedures; this file states invariants, not procedures.
- Use the `hand` CLI and runtime as the source of truth for fleet and machine state
  instead of reading or editing it directly.
- Never edit a registered project under `projects/` directly; a worker does that in
  its own worktree.
- Never merge without explicit operator authorization.
