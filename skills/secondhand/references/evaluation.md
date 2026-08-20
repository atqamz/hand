# Evaluation

Evaluation is a supervisor reasoning procedure over evidence, not a new Hand runtime subsystem.
Hand does not run a judge or critic agent over a worker's output, and this skill does not ask
you to build one - it asks you to read the evidence Hand already gives you and reason about it
yourself, every time, before treating a task as satisfied.

## What "done" never means on its own

```text
wrong: the worker reported done:, so the task is finished
right: the worker reported done:, which is a claim - check it against the brief and the actual
       evidence before treating it as satisfied
```

A worker's own report is one input, not the verdict. Machine evidence is authoritative over
model confidence, including the confidence in a worker's own `done:` line.

## What to compare

Hold these side by side before calling anything satisfied:

```text
brief goal
brief invariants
planned_against
verification commands the brief named
worker report (state, history, any needs-decision it raised)
git diff / commits / the PR itself
tests (did they run, did they pass, do they cover what the brief asked for)
CI
the no-mistakes delivery gate, if the project uses one
Hand's own reconciliation / repair state (references/recovery.md)
```

Missing any one of these when it is available is a gap in the evaluation, not a shortcut.

## Naming the outcome

Useful outcome classes for your own reasoning - not a state Hand persists, just how to think
about what you are looking at:

- **satisfied** - every invariant holds, verification passed, and the evidence agrees with the report.
- **correctable** - a real gap exists but the fix is bounded and safe to steer directly.
- **stale-plan** - the brief's assumptions no longer hold against current reality; re-plan, do not steer around it.
- **ambiguous** - the evidence itself does not resolve cleanly; treat as `references/recovery.md`'s territory, not a guess.
- **operator-decision** - a genuine policy or irreversible choice remains; escalate.

These are guidance for your own reasoning. Do not build a table, a field, or a database around
them unless a separate, deterministic Hand contract independently requires it - if it does, this
reference is the wrong place to duplicate it; consume that contract directly instead.

## What this reference explicitly rejects

- `hand judge` or `hand critic` as commands - they do not exist, and nothing here proposes them.
- Treating "the worker says done" as sufficient without the evidence above.
- Letting model confidence override deterministic machine evidence - if `hand status` reports
  an unknown or unprovable condition, that stands even if the worker's own report sounds certain.
- Nested critic-agent swarms, or any requirement that a Secondhand evaluation involve more than
  one supervising reasoner. One supervisor, reading real evidence, is the architecture.
