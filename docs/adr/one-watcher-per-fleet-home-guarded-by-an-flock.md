# One flock owns watching for a fleet home

- Date: 2026-08-04
- Status: accepted
- Issues: none
- PRs: atqamz/secondhand#138

## Context

Two watchers duplicate notifications and race over durable report offsets. Supervisory sessions can forget an earlier watcher after compaction, so agent discipline cannot enforce singleton ownership.

## Decision

Streaming and until-event modes share one per-home `flock`. The pid stored beside it is advisory only; kernel lock ownership is authoritative, and takeover proceeds only after the incumbent releases it.

[`internal/watcher/ownership.go`](../../internal/watcher/ownership.go) and its unit and end-to-end tests own acquisition, stale pid handling, and takeover behavior.

## Rejected alternatives

- A pid liveness check can mistake reuse or permission failure for ownership.
- Separate mode locks still let two consumers advance the same report channel.
- An AGENTS.md convention fails when the session forgets the process it started.

## Consequences

There is no redundant watcher pair. A takeover that cannot prove release fails instead of risking a second consumer.
