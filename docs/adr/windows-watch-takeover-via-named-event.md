# Windows watch takeover uses a named event

- Date: 2026-08-12
- Status: accepted
- Issues: atqamz/hand#201
- PRs: none

## Context

Windows has no SIGTERM equivalent with the same graceful process-shutdown semantics.

The singleton boundary remains the per-fleet-home kernel-backed file lock described by [One flock owns watching for a fleet home](one-watcher-per-fleet-home-guarded-by-an-flock.md).

The PID file is advisory and must only identify the incumbent to a cooperative shutdown request.

## Decision

Unix takeover continues to request shutdown with SIGTERM.

Windows watchers create an initially nonsignaled auto-reset event named `Global\\hand-watch-takeover-<pid>` before publishing their PID line.

The incumbent waits on that named event and an unnamed local stop event, then closes its one-shot request channel when the named event is signaled.

A replacement opens the named event with `EVENT_MODIFY_STATE`, calls `SetEvent`, and closes its temporary handle.

Missing events are treated as a disappeared incumbent, while other event errors remain visible.

The existing lock retry loop remains the proof that takeover completed, and the incumbent exits through normal context cancellation and deferred ownership release.

## Rejected alternatives

- `TerminateProcess` is an unconditional kill and would bypass graceful watcher shutdown and deferred lock release.
- A named pipe would add a request protocol and extra connection lifecycle without improving this one-shot signal.
- PID liveness checks cannot prove ownership because the kernel lock remains the authoritative state.

## Consequences

A Windows watcher owns one lightweight named event while it owns the fleet-home lock.

The endpoint listener is joined before its handles close, so ownership release does not leak a listener goroutine or wait handle.

The replacement can signal an incumbent without becoming owner until the existing kernel lock is available.
