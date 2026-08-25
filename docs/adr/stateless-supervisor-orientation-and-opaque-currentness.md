# Stateless supervisor orientation and opaque currentness

- Date: 2026-08-22
- Status: accepted
- Issue: atqamz/hand#340

## Context

Supervisor turns may begin after a process restart, a missed watcher edge, or a moved Fleet home.
The existing status, next-action, session, and watcher paths each observe overlapping facts, but none provides one bounded orientation or an exact way to reject a stale wake.

Hand must preserve Fleet identity, report claims, evidence, watcher announcements, and supervisor acknowledgement as separate channels while avoiding a durable supervisor session, event queue, or future Attention/FleetSnapshot schema.

## Decision

`internal/orientation` owns `SupervisorOrientation`, monitor targets, opaque `CurrentnessToken` values, bounded summaries, monitor state, next actions, truncation, and uncertainty.
Tokens are provider-created values scoped to one Fleet, monitor kind, target identity, and exact current generation.
Callers may carry, compare, return, or reject them, but never parse or construct their contents.

`hand session start` reads authoritative Fleet identity, derives orientation, checks watcher ownership, performs a bounded two-pass arm when no watcher is live, then re-reads orientation.
The bounded arm reports `rearmed` or `degraded`; only a lock-proven live watcher reports `already-armed`.
The command never creates a supervisor-session row and never acknowledges or mutates work.

### Amendment - 2026-08-25

The implementation now separates runtime bootstrap from orientation. `hand session start` performs the one-time bootstrap, emits `next_command: hand orient`, and does not derive orientation or arm a watcher. `hand orient` exclusively reads and records the bounded orientation; live monitoring is established with `hand watch --until-event`.

Watcher wakes carry Fleet identity, monitor kind, opaque target identity, opaque currentness, event kind, and a bounded reason.
A wake is only a hint: a consumer must read fresh orientation and accept the wake only when Fleet, target, kind, and currentness match exactly.

The watcher keeps its per-home kernel lock, owner-generation takeover route, durable report cursor, and attempt observations.
Startup and successor ownership retain the existing observe-before-wait two-pass catch-up, so a level already true while no watcher was alive is not lost.

## Rejected alternatives

- A durable supervisor-session or conversation row would couple currentness to one process and create storage ahead of the future read models.
- A whole-Fleet hash would make an unrelated task invalidate every wake and would not provide exact target scoping.
- A detached child without a platform-tested liveness contract would claim `armed` after the session command had already lost proof of the watcher.
- Treating a wake as authoritative would let stale process output steer work without a fresh evidence read.

## Consequences

Repeated session starts converge without taking over a live watcher or clearing acknowledgement.
Moved homes retain currentness when their authoritative Fleet identity and exact durable generation are unchanged.
Colliding human-readable task IDs in different Fleets cannot share monitor IDs or tokens.
The next orientation explicitly names `hand watch --until-event` when the bounded session command cannot prove a detached watcher.

Future Task/Attempt storage and the eventual Attention or FleetSnapshot models can replace the provider adapter without changing the supervisor-facing session or wake contract.
