# External tools have one shared stateful fake each

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#40
- PRs: atqamz/hand#158

## Context

Per-test scripts can return expected text without representing the state changed by the previous command. They also let stream placement and response shapes drift independently from the real tools.

## Decision

[`internal/faketool`](../../internal/faketool) provides one stateful fake for each external CLI used by the suites. [`internal/faketool/FIDELITY.md`](../../internal/faketool/FIDELITY.md) records observed behavior, [`tests/contract`](../../tests/contract) checks it hermetically through shared fixtures, and `make contract-live` checks reversible calls against installed real tools.

## Rejected alternatives

- Per-test scripts test isolated responses rather than command sequences.
- Transcript replay hides the state machine in call ordering and requires rerecording for harmless sequence changes.
- Mandatory contract tests would make the ordinary suite depend on external binaries and network access.

## Consequences

A new external call requires a fake behavior, an observed fidelity entry, and a contract check where the call is safely reversible. Tests declare isolated fake state rather than relying on suite order.
