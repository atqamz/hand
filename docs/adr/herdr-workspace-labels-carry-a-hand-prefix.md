# A project's herdr workspace label carries a `hand:` prefix, and a bare-label workspace is not adopted

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#118
- PRs: none single

## Context

`hand` keeps one herdr workspace per project and finds it by label.
The obvious label is the project name, and that is what shipped.

Herdr derives a workspace's label from its root directory's basename when none is given.
So the label space is neither unique nor owned by `hand`: any directory on the machine whose basename matches a project name produces a workspace with the identical bare label.
The fleet home itself is one such directory, and so is any other tool's workspace rooted at a same-named path.

`FindWorkspaceByLabel` returns whichever match `herdr workspace list` happens to return first.
Under a collision that is a silent dispatch of a worker into a workspace `hand` never created.

## Decision

The label is `hand:<project-name>`, mirroring the existing `hand:<task-id>` convention for treehouse worktree ownership.

This does not make the label unique, and it is not claimed to.
It changes what a collision requires: another `hand`-managed project coincidentally sharing the same name, rather than any directory on the system.

A workspace already created under the bare label before this change is **not** adopted.
`hand spawn` creates a new `hand:<project-name>` workspace alongside it and the old one is orphaned - still functional, just no longer found by lookup.

## Rejected alternatives

**Keep the bare project name.**
It puts `hand`'s lookup in a namespace every directory on the machine can write to, and the failure is silent rather than an error.

**Adopt an existing bare-label workspace on first lookup, then rename it.**
Adoption means deciding that a workspace `hand` did not create is `hand`'s, which is the exact assumption the prefix exists to stop making.
The one case adoption helps is a fleet mid-upgrade; the case it breaks is a same-named workspace belonging to something else.

**Match on the workspace's root directory instead of its label.**
The root is the worktree's cwd for the first task, and worktrees are recycled by treehouse, so the root a workspace was created at is not stable across the project's life.

**Refuse to spawn when a bare-label workspace exists, so the operator migrates deliberately.**
It blocks work on a condition `hand` can route around, and the orphaned workspace costs nothing but a stale entry in `workspace list`.

**Make the label globally unique with a hash or an id.**
The label is what an operator reads in herdr to find their fleet.
A unique label nobody can recognize trades a rare collision for a permanent usability cost.

## Consequences

Upgrading a fleet leaves one orphaned workspace per project, which the operator closes by hand or ignores.

Two `hand`-managed projects with the same name still collide, and nothing detects it.
That is the accepted residue: the prefix narrows the blast radius rather than removing it.

Anything else `hand` names in another tool's namespace gets the same treatment - `hand:` prefixed and never adopted - because both halves of this decision came from the same mistake.
