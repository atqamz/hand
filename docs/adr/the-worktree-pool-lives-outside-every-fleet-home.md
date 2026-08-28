# The worktree pool lives outside every fleet home

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#427
- PRs: none

## Context

atqamz/hand#404 scoped Treehouse pools to the registered clone, closing the cross-fleet worktree
bleed in atqamz/hand#403 and atqamz/hand#412 where a shared `~/.treehouse` let one fleet's slots
alias another's. It did that by passing `--root <clonePath>`, which put every pool at
`<fleet-home>/projects/<project>/.treehouse/<pool>/<n>/<project>`.

That made a worker's worktree a descendant of the fleet home, and a fleet home always carries a
Hand-owned `CLAUDE.md`. The consequence was already written down as something that must not happen,
in `internal/agentsmd/agentsmd.go`:

> a worktree is never under the fleet home, so it never loads the home's AGENTS.md

After #404 it did. Claude Code collects `CLAUDE.md` by walking up from its working directory, finds
the home's, and the `@AGENTS.md` import it contains resolves outside the worker's project root, so
it draws its external-import dialog and waits. `hand spawn` has no signature for that dialog, so
every Claude worker in every fleet failed its 60s confirmation window and left a provisioning
attempt for `hand reconcile` to unwind. The answer is never recorded either, because the pane dies
before it is answered, so the block is deterministic rather than first-run.

The blocked launch is the visible half. The other half is that a worker was being handed the
supervisor's own operating contract, which `AGENTS.md`'s `HAND_ROLE=worker` guard limits without
making intended.

## Decision

The pool root is a per-clone directory under the user-local infrastructure root, never the clone
itself:

```
<secondhand home>/pools/<clone basename>-<12 hex of sha256(clone path)>
```

It is resolved in `internal/worktree` at the point `--root` is built, from the clone path alone.
`#404`'s property is preserved exactly: the digest is taken over the absolute clone path, which
contains both the fleet home and the project, so two clones can never share a pool and no fleet's
slots can alias another's.

**A worktree recorded under its clone keeps the clone as its root.** A lease acquired before this
change is still observed and returned through the pool it was acquired from. Without that rule the
first observation after upgrading would report `LeaseUnknown`, which correctly refuses destructive
cleanup ([Unobservable ownership is not a mismatch](unobservable-ownership-is-not-a-mismatch.md))
and would therefore strand the worktree rather than release it.

The digest keys the pool rather than the fleet id, so this layer reads no fleet state and no root is
threaded through the provisioning dependency graph. The fleet id would read better in a path a human
browses; it is not worth `internal/worktree` depending on fleet identity resolution to get it.

## Rejected alternatives

- **Making the Hand-owned `CLAUDE.md` self-contained instead of an `@AGENTS.md` pointer.** It
  removes the dialog, and makes the second failure strictly worse: the supervisor's contract would
  then be reliably read by every worker instead of failing to load.
- **Teaching `hand spawn` to answer the dialog.** It is a security prompt about trusting file
  imports. A tool clicking through it on the operator's behalf is not a fix, and it would leave the
  contract leak in place. Recognising the dialog and refusing with a reachable treatment is worth
  doing on its own, and is not a substitute for removing the cause.
- **Launching the harness with its project-context discovery disabled.** For Claude Code that is
  `--safe-mode`, which also disables the project's own conventions, or `--bare`, which additionally
  forces `ANTHROPIC_API_KEY` and never reads OAuth. Both trade away things a worker needs, and both
  are per-harness where the cause is not.
- **Reverting to a single shared `~/.treehouse`.** That is exactly the aliasing atqamz/hand#404
  fixed.
- **Keying the pool by fleet id rather than a digest of the clone path.** More readable, and it
  makes `internal/worktree` depend on fleet identity resolution or on deriving the home from the
  clone path by convention - the same class of assumption that produced this issue.

## Consequences

A worker worktree is no longer inside the fleet home, so the invariant `internal/agentsmd` states is
true again, and no harness picks up the supervisor's contract by directory ancestry. The fix is not
per-harness: Codex, Grok and Pi never see the home's files either.

A fleet home is no longer self-contained. Copying or deleting one no longer takes its worktrees with
it, and a home removed by hand leaves its pool behind under `<secondhand home>/pools/`. That is a
real loss and the reason this record exists rather than a commit message. It is judged acceptable
because `<secondhand home>` already holds the private pinned runtime and the user registry, so
per-fleet state outside the home is established, and because a pool holds no durable intent: it is
reconstructible working space, and the durable record of what a worktree was for lives in
`state/hand.db`.

Leases taken before this change keep working, and there is no migration step. The compatibility rule
is not a temporary shim to remove later: a recorded path under its clone means that lease's pool is
the clone, which stays true for as long as any such lease exists.
