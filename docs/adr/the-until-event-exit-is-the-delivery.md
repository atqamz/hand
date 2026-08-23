# Process exit delivers watcher events to the supervisor

- Date: 2026-08-04
- Status: accepted, with its silent baseline superseded by [Arming a watch observes before it waits](arming-a-watch-observes-before-it-waits.md), and its delivery claim narrowed by [Supervisor turn delivery is host-specific](supervisor-turn-delivery-is-host-specific.md)
- Issues: none
- PRs: atqamz/hand#125

## Context

A streaming watcher detects events but does not wake a supervising agent. Background task runners resume the agent when a process exits, and shell wrappers around a never-ending stream cannot reliably distinguish startup state, timeouts, and probe failures.

## Decision

Until-event mode takes a baseline, exits after the first new matching event, and uses distinct exits for delivery, an empty window, and an unprobeable task. Startup state is not an event.

[`internal/watcher`](../../internal/watcher) owns the state machine. Unit and end-to-end watcher tests own filtering, arming, timeout, signal, and exit behavior. The generated fleet workflow tells supervisors to re-arm it.

## Rejected alternatives

- A permanent stream still requires the agent to remember to read it.
- `tee` and first-match wrappers confuse existing state with a transition and lose diagnosis outside their pattern.
- Returning success on timeout makes an empty window indistinguishable from delivered news.

## Consequences

One invocation delivers one wake and must be re-armed. Events consumed during baseline remain in durable channels even though they do not cause that invocation to exit.

Each delivered event can also carry a structured wake hint with Fleet identity, exact monitor target, opaque currentness, and a bounded reason.
Consumers re-orient before acting and discard a wake whose target or currentness is stale.

Narrowed by [Supervisor turn delivery is host-specific](supervisor-turn-delivery-is-host-specific.md): a process exit is a usable delivery primitive only where an owning host is guaranteed to convert it into another Supervisor reasoning opportunity.
Once the outer Supervisor turn has ended, an exit code alone proves nothing followed; supported hosts get their turn through their own bridge (`hand supervision wait --host <harness>` plus the per-host mechanism), and the resulting reasoning turn begins with `hand orient`.
