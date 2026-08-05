# The landed-work guard reads work and fails closed

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#79, atqamz/secondhand#129
- PRs: none single

## Context

Teardown releases the only worktree holding unlanded work. Task metadata can be stale or initially misclassified, so trusting it alone either discards work or forces safe cases through an escape hatch.

## Decision

Where metadata cannot answer whether work survives, teardown inspects repository content and deliverables. Any unresolved read, parse, ref, or PR ambiguity refuses. `--force` remains an explicit authorization to discard genuinely unlanded work, not a repair for metadata.

[`cmd/teardown.go`](../../cmd/teardown.go) owns the ordered guard. Its focused tests cover content comparison, scout evidence, ambiguous PRs, and retry-safe cleanup.

## Rejected alternatives

- Trusting a dirty bit or task kind confuses recorded metadata with the work itself.
- Fetching before comparison makes a destructive guard depend on network state.
- Accepting a report file alone can discard a ship branch that still has commits.
- Falling through on ambiguity turns an unanswered question into permission to clean up.

## Consequences

The guard does more local inspection and can refuse safe work when evidence is unavailable. That false refusal is preferred to losing the only copy of work.
