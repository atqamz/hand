# Hand orchestration vocabulary

This glossary owns the canonical cross-cutting orchestration terms for Hand 0.5.0.
It is a terminology reference, not a centralized product specification.
Command contracts remain owned by command help, implementation, and focused tests, while durable rationale remains in the relevant architecture decision records.

## Authority and runtime

### Operator

The human authority.
The Operator owns intent and irreversible decisions.

### Supervisor

The user-facing agent session.
The Supervisor plans work, writes briefs, coordinates the Fleet, invokes Hand, and may persist configuration after talking to the Operator.
The Supervisor is the normal interface, so routine setup should not require the Operator to hand-edit configuration files.

### Hand

The first-party CLI/runtime.
Hand owns lifecycle, durable state, isolation, process supervision, routing resolution, and reconciliation.

### Fleet home

The directory containing one Hand Fleet's durable context and state.

### Fleet

The Projects, Tasks, Attempts, Workers, holds, and context coordinated from one Fleet home.

## Work model

### Project

A durable Git-backed body of work registered with the Fleet.
A Project may enter through a remote clone, one-time adoption of a local Git worktree, or greenfield creation.
Every Project has one canonical repository under the Fleet and workers never execute directly in an operator-owned source checkout.
A Project has a delivery mode such as `direct-pr`, `local-only`, or `no-mistakes`.

### Task

A durable delegated unit of logical work and history.
A Task survives individual executor runs.

### Task kind

For Hand 0.5.0, there are exactly two Task kinds:

```text
scout
ship
```

`scout` means the intended deliverable is an investigation or report rather than landed code.
`ship` means the intended deliverable is a change that must be landed or explicitly delivered.

`scout` and `ship` are Task kinds, not Worker roles.
A Worker is the generic agent process executing delegated work, regardless of Task kind.

### Brief

The durable task instruction written by the Supervisor for a Worker.
The brief may contain optional `execution_class` and `planned_against` front matter.
The recognized machine-readable contract is limited to those fields and the existing `model` and `effort` fields; the Markdown body has no required schema.

For a mechanical brief, `planned_against` is the full commit ID of the registered project's verified local default branch.
Hand compares it with exact equality before provisioning and refuses a stale plan.
For standard and deep briefs, it remains provenance without the mechanical exact-match refusal.
Hand also verifies the acquired worktree `HEAD` before Herdr or worker launch because Treehouse may refresh a lease during acquisition.
Mechanical dispatch requires a harness capable of carrying the shared mechanical worker guidance; unsupported harnesses fail before lifecycle mutation.
If the project advances, the Supervisor must re-check the plan rather than merely replacing the commit ID.

### Execution class

How much executor judgment remains after planning.
It is not a measure of task size, lines of code, or file count.

For Hand 0.5.0, there are exactly three execution classes:

```text
mechanical
standard
deep
```

A large exact migration can be `mechanical`, while a five-line concurrency fix can be `deep`.
`mechanical` plans should be execution-ready: they should state verified current state, locked decisions, exact change locations, ordered steps, invariants, tests, verification, non-goals, and stop conditions.
Those headings are recommendations, not syntax Hand parses.
Execution classes are not model, effort, profile, or cost routing; concrete routing is deferred to atqamz/hand#215.

### Attempt

One execution incarnation of a Task.
A Task may have historical Attempts but at most one active Attempt.
Concrete execution identity belongs to the Attempt.
Task, Attempt, and Worker are distinct: the Task is durable history, the Attempt is one execution incarnation, and the Worker is the process executing it.

### Worker

The agent process Hand launches for execution, regardless of Task kind or model.
`scout` and `ship` do not name Worker roles.
Compatibility names such as `HAND_ROLE=worker` and `WorkerRole` remain unchanged.

### Harness

The local agent executable or integration Hand launches, such as `codex`, `claude`, `opencode`, `grok`, or `pi`.
Harness is not synonymous with model provider.

## Configuration and delivery

### Profile

An Operator-defined named execution configuration.
Profile names are arbitrary.
Hand does not reserve names such as `cheap`, `normal`, `frontier`, or `premium` as semantic tiers.
This glossary defines the concept only and does not add Profile persistence, configuration, or routing.

### Route

A deterministic mapping from Task kind plus execution class to a Profile, and eventually to one concrete Attempt execution snapshot.
This glossary defines the term only and does not add routing behavior.

### Delivery mode / gate

Delivery mode describes how Project work lands.
`no-mistakes` is an optional gate, add-on, or integration boundary, not a source of Hand core Task kinds.
Its internal stage names and configuration belong to no-mistakes itself.

See the canonical [no-mistakes upstream](https://github.com/kunchenguid/no-mistakes).
