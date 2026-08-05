# Process exit delivers watcher events to the supervisor

- Date: 2026-08-04
- Status: accepted
- Issues: none
- PRs: atqamz/secondhand#125

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
