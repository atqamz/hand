# Secondhand is one Go binary, not an agent distribution

- Date: 2026-08-04
- Status: accepted
- Issues: none single
- PRs: none single

## Context

Firstmate proved the fleet-supervision concept but implemented orchestration through shell scripts and a large always-loaded instruction corpus. That shape made portability, recovery, and supervisory context depend on the agent correctly replaying operational procedure.

The cross-cutting orchestration terms used here are defined in [Hand orchestration vocabulary](../vocabulary.md).

## Decision

Secondhand keeps the fleet model and moves orchestration into one Go CLI binary. The supervising agent is a client. Fleet homes contain state and prose, not executable orchestration, and ordinary commands are short-lived processes.

The package boundaries and command help are the current design; this record keeps only why the binary boundary exists.

## Rejected alternatives

- Paying down the shell distribution leaves orchestration encoded in instructions and host-specific commands.
- A scripting-language rewrite improves shell portability but retains a runtime and dependency installation surface.
- A daemon adds lifecycle and version coordination to a directory that should remain inspectable with ordinary tools.
- Porting every prior feature preserves speculative complexity instead of adding capabilities after demonstrated need.

## Consequences

One artifact owns lifecycle correctness and state transitions. AGENTS.md can remain operating guidance rather than becoming the implementation, and adding a feature requires a demonstrated fleet need.
