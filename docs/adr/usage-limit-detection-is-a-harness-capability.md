# Usage-limit detection is a harness capability

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#136, atqamz/hand#81, atqamz/hand#84, atqamz/hand#85, atqamz/hand#128
- PRs: atqamz/hand#154

## Context

A stopped worker may show a harness-specific quota refusal in its pane. Generic watcher heuristics risk reading and steering unrelated harnesses, including a plain shell pane.

## Decision

Recognition and reset parsing belong to a per-harness catalogue in [`internal/harness`](../../internal/harness). The watcher asks whether the capability exists before reading a pane and treats reset times as scheduling hints, never proof that service resumed.

Only signatures observed from a real limited run are added. [`internal/watcher/usagelimit.go`](../../internal/watcher/usagelimit.go) and focused tests own scheduling, locking, holds, and retry bounds.

## Rejected alternatives

- Poll-loop text heuristics mix harness policy into orchestration and can steer the wrong pane.
- Plausible unobserved signatures trade a visible stranded worker for silent unwanted input.
- Matching a generic word such as limit mistakes warnings for stops.
- Declaring recovery at a predicted timestamp trusts an estimate instead of observing the pane.

## Consequences

Unsupported harnesses receive no pane read or automatic steer. Supporting another harness requires evidence from its real refusal surface and a catalogue entry rather than a watcher branch.
