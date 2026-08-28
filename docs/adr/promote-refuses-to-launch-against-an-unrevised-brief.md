# Promote refuses to launch against an unrevised brief

Date: 2026-08-28

Status: accepted

Issues: atqamz/hand#448

Pull requests: none

## Context

`hand promote` turns a completed scout into a ship task and launches the ship attempt against the
same path, `data/<id>/brief.md`, that the scout attempt read. A scout brief says what a scout brief
has to say - investigate, do not change behavior, no commit, no push, no PR - and promotion reuses
whichever text sits at that path without checking whether anyone rewrote it for the ship attempt.

This was observed in this fleet: the scout for atqamz/hand#410 was promoted, and attempt 2 launched
against the scout brief before the supervisor could rewrite it. The worker was not blocked and
reported nothing unusual, because it read a coherent brief and obeyed it. The failure is quiet -
recovered only because the supervisor happened to check the attempt state within the minute.

## Decision

Hand records a digest of the brief at attempt launch, alongside `planned_against` on the `attempt`
row, which already records commit provenance for the same reason: to answer "what was this attempt
launched against" without re-deriving it from mutable state.

`hand promote` compares the scout attempt's recorded digest against the current digest of
`data/<id>/brief.md`. If they match, promote refuses, names `data/<id>/brief.md`, and says to
rewrite it as a ship brief - launching nothing. If they differ, promote launches exactly as it does
today, and the new ship attempt records its own digest of the brief it launched against.

An attempt recorded before this change carries no digest. An empty digest is read as "no evidence
either way", not as "unchanged" - refusing it would refuse every task promoted before this feature
existed, which is a worse failure than the one being fixed.

A digest proves the supervisor touched the brief deliberately. It cannot judge whether the new
brief is a *good* ship brief, and does not try to - that limit is deliberate, not an oversight.

## Rejected alternatives

**Promote without launching.** Splits one command into two for every promotion, including the ones
where the supervisor already rewrote the brief correctly. It solves the problem by removing the
convenience of dispatching from the same command, rather than by checking the precondition that was
actually missing.

**A separate `data/<id>/ship-brief.md`.** Changes the path convention the bundled skill documents in
`references/planning-and-briefs.md` for every ship task, not only promoted ones. Widest blast radius
for the same guarantee a digest check gives at the cost of one column.

## Consequences

Every attempt launch - `hand spawn`, `hand reopen`, and `hand promote` - now records a digest of the
brief it launched against, on the same `attempt` row that already records the harness, model, and
`planned_against` for that launch. Nothing reads this digest except promote's own comparison; it is
not surfaced as routing provenance.

A supervisor that promotes with an unrevised brief gets a refusal naming the file to rewrite, not a
worker that silently investigates instead of shipping. A supervisor that already rewrote the brief
sees no change in behavior.
