# Gate checks read no-mistakes output, not its database

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/hand#92, atqamz/hand#97
- PRs: none single

## Context

Secondhand needs to know whether a repository can use its configured gate and whether a PR appears in a completed gate run. Both answers exist in no-mistakes output and in its private sqlite schema.

## Decision

Secondhand invokes no-mistakes and interprets its public output. It never reads `~/.no-mistakes/state.sqlite`.

[`internal/project/gaterun.go`](../../internal/project/gaterun.go) owns the parser and distinctions between answers. Its package tests and the command-level gate tests own the observable behavior.

## Rejected alternatives

- Reading another tool's private schema couples releases without a compatibility boundary.
- Trusting process exit alone loses states that no-mistakes reports at exit zero.
- Collapsing every failure into one answer gives the operator remedies unrelated to the actual fault.

## Consequences

An upstream wording change can break the parser. The shared fake records the output Secondhand depends on, and focused tests keep an unanswered check from becoming a stronger claim.
