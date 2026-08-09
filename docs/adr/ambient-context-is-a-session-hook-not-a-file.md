# Ambient fleet context is a session hook, not a rendered file

- Date: 2026-08-04
- Status: superseded by [Supervisor bootstrap is an AGENTS.md contract](supervisor-bootstrap-is-an-agents-md-contract.md)
- Issues: atqamz/secondhand#62, atqamz/secondhand#64
- PRs: atqamz/secondhand#122

## Context

A supervisor needs current fleet state when a session starts. A rendered dashboard can look current while silently lagging the store, and putting live state in AGENTS.md gives a generated rules file the same defect.

## Decision

Fleet context is produced by running the bare `hand` command from a `SessionStart` hook. The hook is merged into the operator's settings rather than replacing them, and is installed only for a fleet home.

The implementation and merge behavior live in [`internal/sessionhook`](../../internal/sessionhook) and its tests. The generated workflow that explains the snapshot lives in [`internal/agentsmd`](../../internal/agentsmd).

## Rejected alternatives

- A rendered dashboard duplicates live state and can be stale without saying so.
- Embedding the overview in AGENTS.md mixes perishable state with operating rules.
- Requiring the supervisor to remember an initial status command spends a turn and fails silently when forgotten.

## Consequences

Harnesses without a session-start mechanism must run `hand` themselves. The settings file remains shared with the operator, so every refresh must preserve entries `hand` does not own.
