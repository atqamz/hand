# AGENTS.md is fully Hand-owned and immutable

- Date: 2026-08-20
- Status: accepted
- Issues: atqamz/hand#218
- PRs: none

## Context

[Supervisor bootstrap is an AGENTS.md contract](supervisor-bootstrap-is-an-agents-md-contract.md) let a fleet home keep operator or supervisor content around one marked span hand owned. That mixed ownership grew its own costs as the fleet matured: merge logic to preserve the surrounding content, drift heuristics for perishable prose outside the span, and no settled answer for whether a given sentence belonged in `AGENTS.md`, `data/operator.md`, or `data/learnings.md`.

Now that Task/Attempt semantics, execution-ready briefs, Profiles/routes, reconciliation, and distribution ownership are stable, `AGENTS.md` no longer needs to carry anything beyond the invariants a supervisor must load regardless of harness.

## Decision

`AGENTS.md` is generated content end to end. `hand init` restores it byte-for-byte against the running binary's canonical body; nothing outside it is preserved across a refresh, and the supervisor never edits it by hand. Detailed procedure moves to the bundled `secondhand` Agent Skill; living fleet context stays in `data/**`.

The first time `hand init` finds a fleet home's `AGENTS.md` predating this model, it archives that file's non-generated content verbatim under `data/agents-md-legacy-migration.md` before overwriting it, reports the archive path, and leaves relocating anything useful to a human. `internal/agentsmd`'s `legacyContent` recognizes the previous marked-span format to know what to archive; ambiguous or malformed markers fail the refresh instead of guessing, so a human resolves them by hand. Once that one-time migration has run, any further drift, including a hand edit, is a plain restore rather than another migration.

`hand doctor`'s drift check now compares the whole file against the canonical body byte-for-byte instead of scanning for perishable content or marker damage, since there is no longer a span within the file that legitimately differs.

## Rejected alternatives

- Keeping the marked-span model and only shrinking its content leaves every cost this decision removes: merge logic, drift heuristics for content the span cannot own, and an unsettled boundary between `AGENTS.md` and the fleet home's own notes.
- Reclassifying a pre-migration file's non-generated content into `operator.md` or `learnings.md` automatically requires guessing intent Hand cannot verify; the issue this decision closes explicitly rules it out.
- Discarding a pre-migration file's non-generated content instead of archiving it risks real operator or supervisor knowledge with no recovery path.

## Consequences

A fleet home's `AGENTS.md` can no longer coexist with hand-authored operator prose; anything an operator wants to keep must live under `data/**` or in the bundled skill's references instead. The one-time migration is a single archive-and-replace rather than an ongoing merge, so `internal/agentsmd` sheds the perishable-content scanner, the marker-drift line-by-line diagnostics, and `mergeGenerated` entirely. This repository's own top-level `AGENTS.md` keeps the legacy `<!-- hand:generated:start/end -->` markers purely as a human-facing delimiter around a copy of the canonical body, verified current by `internal/agentsmd`'s dogfood test; no runtime code reads them there.
