# Output defaults to TOON while existing JSON remains compatible

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#45, atqamz/secondhand#100
- PRs: atqamz/secondhand#155

## Context

The primary consumer is an agent, so padded terminal tables waste context. Existing JSON callers are automation outside this repository and cannot be migrated atomically with a format change.

## Decision

Commands render their default documents through [`internal/axi`](../../internal/axi). Existing `--json` shapes remain compatible. A request combining TOON-only field selection with JSON is rejected instead of silently ignoring one flag.

Renderer tests and command tests own quoting, block shape, field selection, and JSON compatibility.

## Rejected alternatives

- Removing JSON breaks existing automation for no benefit to the new default.
- Reshaping JSON under the same flag returns valid data with incompatible meaning.
- Silently choosing one of two conflicting flags lies about the invocation that ran.

## Consequences

Commands with JSON support keep two output paths. New default output behavior belongs in the shared renderer rather than per-command formatting.
