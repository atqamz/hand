# hand init adopts its own source tree

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#436
- PRs: none

## Context

`hand init` refuses a non-empty directory that is not already a recognized fleet home, so that adopting an operator's unrelated working directory is impossible by construction (see [hand init is the canonical fleet reconciler](hand-init-is-the-canonical-fleet-reconciler.md)). That guard also refuses the one directory where Hand's own development wants a fleet home: a checkout of Hand itself.

That configuration was never speculative: CONTRIBUTING.md already instructs a contributor to run `./hand init` in the checkout, and states that every directory it creates there is gitignored so the fleet home lives alongside the source without being committed. Every surface `hand init` writes into a fleet home - `state/`, `data/`, `projects/`, `config/`, and the per-harness bridge directories - is in fact ignored, and the canonical `AGENTS.md` and its `CLAUDE.md` reference are committed byte-for-byte identical to what `agentsmd.Refresh` generates, so a reconcile writes nothing and leaves no drift. The guard was refusing a documented, harmless configuration rather than preventing a hazard, and the documentation was the older of the two.

## Decision

Hand's own source tree is the single exception to the non-empty refusal. `hand init` adopts a non-empty directory when, and only when, that directory's `go.mod` declares the module path `github.com/atqamz/hand`. Every other non-empty directory that is not a recognized fleet home is still refused, unchanged.

Identity is the module path rather than a pathname or a Git remote:

- A clone or a fork under any directory name qualifies, so the exception is not tied to one operator's layout.
- A subdirectory of the tree carries no `go.mod` of its own, so `hand init internal` is still refused without a second guard.
- A fork that renames its module has opted out, which is the correct reading: it is no longer Hand's source tree.
- The check reads one file and executes nothing, so preflight stays free of a Git dependency.

`hand init` reports `hand_source_tree: true` in its output for such a home, on the adopting run and on every later reconcile, and its help names registering Hand as a project so the supervisor does not edit the checkout it supervises.

## Rejected alternatives

- An explicit `--adopt` or `--force` flag makes the exception general: it re-opens adoption of any non-empty directory, which is exactly what the refusal exists to prevent, and it contradicts `hand init` asking no questions.
- Matching on the Git remote URL requires executing Git during preflight, breaks for a tarball or a mirror, and still says nothing a renamed module path would not say better.
- Matching on a directory name such as `hand` or `hand-dev` adopts any unrelated directory that happens to carry the name, and refuses a clone that does not.
- Dropping the non-empty refusal entirely, and letting the gitignored layout make adoption harmless everywhere, removes the guard for repositories that ignore none of those paths.
- Keeping Hand's fleet home outside the checkout and registering Hand as a project from there works, and remains available, but it never exercises a fleet home that is also a working repository - the case Hand's own development is best placed to keep honest. It also contradicts CONTRIBUTING.md, which would then need rewriting to describe a workflow nobody uses.

## Consequences

Hand can be developed with Hand from its own checkout: `hand init` in the tree, then `hand project add https://github.com/atqamz/hand`, which registers a separate Fleet-managed clone under `projects/` for workers to worktree from. The checkout remains the supervisor's workspace and is never a registered project, so the invariant that nobody edits a registered project directly is untouched.

The exception is narrow enough to stay honest: `cmd/init_test.go` asserts adoption of a tree whose module path matches, continued refusal of a non-empty tree whose module path does not, and continued refusal of a subdirectory of the tree. A change that widened the exception to any non-empty directory fails those tests rather than surfacing as an adopted working directory.
