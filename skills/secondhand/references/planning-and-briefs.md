# Planning and briefs

## Task kind: scout or ship

`scout` and `ship` are Task kinds, not worker roles. Use `scout` when the deliverable is an
investigation or report; use `ship` when the deliverable is a change that must be landed or
explicitly delivered. A worker is simply the agent process Hand launches to execute delegated
work, whichever kind the Task is.

## Execution class: mechanical, standard, or deep

The execution class means how much remaining judgment the executor has *after planning*, never
task size, line count, or file count.

```text
wrong: "this is a big refactor across ten files, so it must be deep"
right: "every file, symbol, and step is already decided; the executor only applies and
        verifies specified changes" -> mechanical, regardless of file count
```

- **mechanical** - a decision-complete plan; the executor applies specified changes and verifies.
- **standard** - architecture is decided; ordinary reversible implementation judgment remains.
- **deep** - substantial implementation reasoning remains.

Classify routine work yourself; do not ask the operator to classify it. Ask only when a
genuinely operator-owned tradeoff remains (see `references/configuration.md` for the Profile
side of that same judgment call).

## Investigate before you commit to a plan

Use a scout first whenever investigation is the cleanest way to remove uncertainty before
writing a ship brief - especially before a mechanical brief, since mechanical dispatch later
refuses drift against what the brief assumed.

## What a mechanical ship brief needs

Recommended headings, not syntax Hand parses - they are guidance, not a format the tooling
enforces:

- Goal
- Verified current state
- Locked decisions
- Exact files/packages/symbols touched
- Ordered implementation steps
- Invariants that must hold afterward
- Tests to add or update
- Verification commands to run
- Non-goals
- Stop/escalate conditions

## planned_against

Immediately before finalizing every new execution-class brief, resolve the registered project's
verified default branch under `<home>/projects/<project>`, investigate against that exact
revision, and record its full commit ID as `planned_against` in the brief.

Mechanical dispatch later compares the same local base and refuses on drift: if the project has
moved since planning, it will not silently execute a plan against a base that no longer exists.
Standard and deep dispatch retain `planned_against` as provenance only, without that hard
refusal, since some plan-adjustment judgment is expected of those execution classes.

```text
wrong: the project advanced past planned_against, so bump the recorded SHA and re-dispatch
right: the project advanced past planned_against, so re-check every assumption the brief made
       against the new state, and rewrite or revalidate the plan - not just its provenance line
```

## Recording and dispatching the task

- Edit `data/backlog.md` to record the task with a unique ID.
- Write the brief at `data/<id>/brief.md`, including the absolute path to `state/<id>.status`
  and the report vocabulary the worker should append to it (see
  `references/task-lifecycle.md`).
- Dispatch with `hand spawn <id> <project> [--scout] [--profile <name>] [--harness ...]
  [--model ...] [--effort ...]`. Normally omit `--model`/`--effort`: use them only for a genuine
  task-specific need, never as routine routing - routine routing belongs in a configured Route.
