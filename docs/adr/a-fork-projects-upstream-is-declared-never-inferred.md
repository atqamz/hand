# A fork project's upstream is declared by a command of its own, never inferred

- Date: 2026-08-05
- Status: accepted
- Issues: atqamz/secondhand#134
- PRs: atqamz/secondhand#135, atqamz/secondhand#142

## Context

`hand` pushes a worker's branch to the repo it cloned.
A fork contribution's PR does not live there: it lives on the repo the work is offered to, which is the repo `hand pr` and gate-opened-PR detection have to look at.

Both compared against the clone's `origin` remote alone, so a genuine upstream PR was refused as belonging to a foreign repo, and teardown's landed-work check read landed work as unlanded.

Widening either comparison is easy and is the whole risk here.
The guard's only job is refusing a PR that belongs to somebody else's repo, so whatever tells `hand` about an upstream decides how narrow the guard stays.

## Decision

A project carries an optional `upstream` slug, and only an operator's declaration puts one there: `hand project upstream <name> <repo>`, cleared by passing an empty repo.
A project that declares nothing is guarded exactly as it was before this existed.

It is a command of its own rather than a flag on `hand project add`, because a fork project is normally already registered by the time the first upstream contribution comes up, and `hand project add` clones - it cannot be re-run against a project that already exists.

What the declared slug then does to PR matching - searched alongside the project's own repo, head refs restricted to the project's repo, every comparison case-folded - is in `an-unrecorded-pr-is-recovered-by-head-ref.md`.

## Rejected alternatives

**`hand project add --upstream <repo>`, with no separate command.**
It looks like the smaller surface and is the change a future worker is most likely to make.
It serves only a project registered after somebody already knew an upstream contribution was coming, and the recovery for every other project is `hand project remove` plus `hand project add`, which re-clones a working repo to record one string.

**Infer the upstream from GitHub's fork parent.**
It removes the command and makes what the guard accepts depend on what GitHub answers at that moment rather than on what an operator declared.
A fork of a fork, a renamed parent, or an unreachable API each move the guard without anybody deciding to.

**Accept any PR whose repo looks related to the project's own.**
There is no resemblance test that admits an upstream and refuses a stranger's repo of the same name.
The narrow version of this is the head-repo filter in `an-unrecorded-pr-is-recovered-by-head-ref.md`, which works precisely because it asks about a branch `hand` pushed rather than about a repo name.

## Consequences

The slug is projected into `data/projects.md`, whose fields are whitespace-separated, so one containing whitespace is refused at declaration time rather than read back truncated later.

An operator who forgets the declaration sees a refusal that names it - the declared upstream, or that none is declared - because "wrong upstream" and "no upstream" are different mistakes with different fixes.

Nothing reconciles the declaration with reality afterwards.
A project whose upstream is stale carries a wrong slug until somebody re-declares it, which is the cost of the guard depending on a statement rather than on a lookup.
