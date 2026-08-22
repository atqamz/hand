---
name: secondhand
description: Use when supervising a Secondhand (hand) fleet - setup and health, profiles and routing, planning and briefs, task lifecycle, the supervision loop, evidence-based evaluation, recovery, and preparing a bug report. Do not use this from inside a worker's own worktree.
metadata:
  source: atqamz/hand
  managed-by: hand
---

# Secondhand supervisor handbook

## Project onboarding

Register an existing remote or local Git project with `hand project add <source>`.
For greenfield work, use `hand project create <name>`.
Local adoption is one-time and local-only: Hand copies committed state into its managed clone, never executes in the operator-owned source checkout, and does not synchronize the two repositories later.

This skill teaches how to drive the `hand` CLI as a Secondhand fleet supervisor. It is a
procedure layer over `hand`, not a second implementation of it: every claim here about fleet
state, dependency state, or task state is something you get by running a `hand` command and
reading its structured output, never by reconstructing it from panes, files, or memory.

```text
wrong: skill checks PATH itself and decides whether Herdr is installed
right: skill runs `hand doctor` and interprets structured output

wrong: skill edits config files directly
right: skill uses `hand config profile ...` and `hand config route ...`

wrong: skill reconstructs lifecycle state from terminal panes
right: skill uses `hand status`, `hand watch`, and hand's own reconciliation results
```

Every home has an opaque durable Fleet identity.
Use `hand fleet` to inspect user-local discovery when the current invocation context is unclear; do not invent a global active Fleet or look for a `fleet switch` command.
If Hand reports a duplicate or identity mismatch, stop runtime and mutation commands and resolve the home evidence before acting.
New workers use Fleet-scoped Herdr sessions, while legacy Attempts are observed and cleaned up only through their exact persisted Herdr identities.

`AGENTS.md` states the invariants that hold regardless of session; this skill states the
procedures for acting on them. If the two ever seem to disagree, `AGENTS.md` wins, and that is
itself worth reporting as a drift signal rather than silently resolving.

## Routing to a reference

Read the reference for the phase of work you are actually in. Do not read all of them up front
for a routine session; each is designed to be fetched only when its phase applies.

- **references/setup-doctor.md** - first session in a home, or anytime something seems wrong: interpreting `hand doctor`, dependency categories, generated-surface drift.
- **references/configuration.md** - no Profile/Route exists yet for a task you are about to dispatch, or `hand doctor` reports a routing problem.
- **references/planning-and-briefs.md** - about to dispatch: choosing scout vs ship, mechanical vs standard vs deep, writing an execution-ready brief.
- **references/task-lifecycle.md** - normal day-to-day commands: spawn, status, send, hold, deliver, merge, teardown, reopen, promote.
- **references/supervision-loop.md** - the bounded control loop that ties dispatch, observation, and decision together across a task's life.
- **references/evaluation.md** - deciding whether a worker's claim of done actually holds up against the brief.
- **references/recovery.md** - `hand status` or `hand reconcile` shows an ambiguous, ownership-unprovable, or needs-repair condition.
- **references/bug-report.md** - `hand` itself misbehaves and the operator wants to file or review a report.

## What this skill never introduces

No generic Hand plugin or runtime framework, no marketplace for third-party skills, no
automatic installation of Herdr, Treehouse, a harness, `gh`, or `no-mistakes`, no hard-coded
vendor or model policy, no cross-profile model shopping, no provider billing or quota
introspection, and no `hand judge`, `hand critic`, or generic agent-swarm pattern. Where a
procedure below needs a decision only Hand's own deterministic state can make, it says to run
a command and read the result - it does not invent a second, prose-only answer.
