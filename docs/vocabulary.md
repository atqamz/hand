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

### Fleet identity

The opaque immutable identifier stored authoritatively in a Fleet home's `state/hand.db`.
It is stable across restarts and home moves and is not derived from a path, host, user, project, or timestamp.

### Fleet

The Projects, Tasks, Attempts, Workers, holds, and context coordinated from one Fleet home.

### Current invocation context

The Fleet home selected for one command by `HAND_HOME`, the current directory, or nearest-ancestor discovery.
There is no global active Fleet selection to switch.

### Fleet registry

The user-local, non-authoritative SQLite index at `~/.secondhand/registry.db` that retains observed Fleet home locators for discovery.
Registry absence or degradation does not replace the authoritative identity in a Fleet home.

### Supervisor orientation

A bounded, stateless read model returned by `hand orient`.
It contains Fleet identity, work and actionable summaries, exact monitor targets, monitor state, next actions, truncation, and uncertainty.
It is not a durable supervisor session, Task-to-Plan record, FleetSnapshot, or Attention table.

### Monitor target

The smallest exact Fleet-scoped condition a watcher can observe and a supervisor can re-check.
Its ID and currentness are provider-owned opaque values.

### Currentness token

An opaque provider-owned value for one exact monitor target generation.
Callers may carry, compare, return, or reject it, but never parse it or infer a newer generation from its bytes.

### Wake hint

A bounded watcher message naming Fleet identity, monitor kind, opaque target, opaque currentness, and reason.
A wake is not authoritative state and cannot directly mutate work; the supervisor must obtain fresh orientation first.

### Duplicate Fleet

A Fleet identity that is valid at more than one registered home.
Runtime and mutating commands refuse positive duplicate evidence rather than guessing which home owns external resources.

### Legacy Herdr namespace

The global or `default` Herdr session used by Attempts written before Fleet-scoped sessions.
Legacy cleanup requires the exact persisted workspace, tab, and pane identities and never adopts a workspace by project label alone.

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
Acquisition integrity and `HEAD` verification are documented in [the user-facing worktree guidance](../README.md#brief).
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

### Worker Harness

The local agent executable or integration Hand launches for one Attempt, such as `codex`, `claude`, `opencode`, `grok`, `pi`, or Antigravity's `agy`.
Worker Harness capability is independent from Supervisor Harness capability and is not synonymous with model provider.

### Supervisor Harness

The user-facing agent host capable of running Supervisor reasoning and, where qualified, Hand's wake/re-entry integration.
Its registry is owned independently from Worker Harness support. A product may support Worker execution without being a Supervisor Harness, or vice versa; provider identity never implies role.

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
