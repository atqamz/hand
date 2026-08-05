# Every harness launch template runs the harness interactively, never headless

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#152
- PRs: none single

## Context

`hand spawn` constructs a launch command from a per-harness template, `cd`s into the worktree, and sends it to a herdr pane.

Every supported harness has a headless mode that is easier to launch and easier to reason about: `claude --print`, `opencode run`.
It answers once and exits, leaving a clean transcript and no resident process.

Three things `hand` does afterwards need the process to still be there.
`hand send` writes into a running pane.
`hand watch` polls pane state to classify a worker as working, blocked or idle.
The `no-mistakes` delivery mode drives many turns as the worker responds to review, test, document and lint gates.

Headless was the original shape and it is what made the interactive first-run dialogs appear, so switching cost something visible and immediately looked like a regression.

## Decision

Every template launches its harness interactively and must stay resident for the whole task.
A one-shot invocation is not an acceptable template for any harness, present or future.

Each template sets its harness's autonomy or permission flag, so an unattended worker does not stall on a permission prompt.

The first-run dialogs interactive launch exposes are recognized by signature in `internal/harness` rather than avoided.
They are answered where answering is scoped to the work, and deliberately not answered where it is not: the managed-settings security dialog grants arbitrary code execution and prompt interception for every run on the host, so it is recognized, surfaced, and left for the operator to accept once.

A declared model or effort a harness has no flag for is warned about on stderr rather than dropped in silence (atqamz/secondhand#152).

## Rejected alternatives

**Launch headless and re-invoke per turn.**
There is nothing left to send to, classify, or drive through a gate.
Every one of `hand send`, `hand watch` and the gate mode would need its own mechanism for state a resident process already holds.

**Launch headless and keep a wrapper process resident in the pane.**
The wrapper would have to reimplement the harness's own session continuity, and pane state would then describe the wrapper rather than the worker, which is the one thing `hand watch` reads it for.

**Auto-accept every first-run dialog, including the managed-settings one.**
Accepting it is a host-wide grant with nothing to do with the checked-out repository.
`hand` has no standing to make that grant on an operator's behalf, and doing it silently on a spawn is the worst possible moment.

**Drop a declared model or effort silently when the harness has no flag for it.**
The operator declared it in a brief and would have no way to learn it was ignored.
A warning costs one stderr line and is the difference between a wrong model and a known-wrong model.

## Consequences

Every spawn is subject to first-run dialogs, and the workspace trust dialog fires on *every* spawn rather than once per host, because each treehouse worktree is a fresh path under the pool root.

Dialog signatures are matched against another tool's UI text, so they must stay case-sensitive and keep their distinguishing anchors, and they go stale when a harness changes wording.
`internal/harness` is the authority for them; `SPECS.md` describes the policy, not the catalogue.

Adding a harness means an interactive template, its autonomy flag, its model and effort capability flags, and its first-run signatures if it has any.
A template that cannot satisfy the first requirement does not get added.
