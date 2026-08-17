# Watcher takeover is generation-attributed

- Date: 2026-08-16
- Status: accepted
- Issues: atqamz/hand#222
- PRs: atqamz/hand#231

## Context

A single per-home watcher is authoritative, but the takeover path used to prove
replacement by signaling a pid read from `watch.pid`. Two failures followed.

A watcher crashes, a stale pid remains, a new watcher takes the kernel lock, and
before a takeover contender can even observe the new owner, it reads the stale
pid and - on Unix - sends that process SIGTERM. The pid may now belong to an
innocent unrelated process. A liveness check does not fix this: a pid that is
reused by an innocent process is as alive as the intended incumbent was.

The incumbent also cannot tell an explicit Hand takeover from an ordinary
external SIGTERM: both arrived as the same signal, so a displaced watcher could
not report whether it was replaced or just interrupted.

## Decision

The kernel-backed `watch.pid.lock` remains the only ownership authority. `watch.pid`
stays advisory pid metadata. A second routing file `watch.owner` carries a
versioned, random ownership generation that correlates takeover routing, nothing
more.

A fresh owner, while holding the authoritative lock, removes any stale routing
record, generates a fresh generation, creates the generation-bound platform
takeover endpoint, publishes the advisory pid, and only then publishes the
complete `watch.owner` record - so a valid live record implies a ready endpoint.
On release it unpublishes the record and closes the endpoint while still holding
the lock, then clears the pid, then releases the lock last.

Unix onboarding coordinates a successor through a generation-bound
Unix-domain-socket endpoint derived from fleet-home identity and generation.
Windows preserves its named-event takeover, re-keyed from home identity and
generation instead of pid. A stale generation can never reach the current
generation's endpoint, so a stale or reused pid can never become a takeover
target.

A successor becomes owner only by acquiring the kernel lock; endpoint request
success alone never grants ownership. Malformed, partial, or wrong-version
routing metadata is non-actionable - no pid fallback exists.

Replacement and generic interruption are distinct results:
`watch interrupted` (exit 8) for ctrl-C / external SIGTERM / parent cancellation,
and `watch replaced by explicit takeover` (exit 9) for a valid generation-bound
request. Event delivery stays exit 0, a quiet window exit 4, arm failure exit 5.

## Rejected alternatives

- Signaling the advisory pid (with or without a liveness pre-check): pid
  liveness cannot prove ownership and can misid an innocent reused process.
- Treating SIGTERM as proof of takeover: an external operator cannot be told
  apart from an explicit Hand request, collapsing two distinct lifecycle exits.
- A resolvable endpoint path rooted in fleet home alone: opens the takeover
  target to arbitrary-path injection and exceeds macOS socket-path limits.
- Timestamp or process-registry authority: only the kernel lock is authority.

## Consequences

No takeover operation targets a process by pid. Stale, reused, or malformed
metadata is safe by construction: routing is generation-bound and authority is
the kernel lock. A hard crash needs no manual `watch.pid` or `watch.owner`
cleanup. The exit taxonomy distinguishes replacement from interruption so a
supervisory agent re-arms only when it should.

`state/events.log` remains the fleet/worker event channel: a watcher's own
termination is neither a fleet event nor logged there. It surfaces through the
typed error, the AXI error kind, and the exit code, so a lifecycle log or fake
replaced/interrupted event is never needed.
