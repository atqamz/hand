# The comment rule is two mechanical checks, not a judgement about whether a WHY is real

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#98
- PRs: atqamz/secondhand#160

## Context

`CONTRIBUTING` and the operating rules both said zero comments by default, only a WHY the code cannot show or a functional pragma.
The rule did not bind, and the reason is that it has no checkable form.
A worker can construct a WHY for any comment, and the reviewer is then arguing taste.

The measurement is what settled it.
Worker-authored added Go comment lines, read in file order: 145 on atqamz/secondhand#75 with 141 holding on review, 78 on #77 with all but two holding, 24 on #65, 91 on #79 all carrying a real WHY.

So the bodies were mostly defensible.
That is the finding: the rule was passing every case it was applied to while the volume kept climbing, which means it was not the thing doing the work.

The concentration was in test comments, and they shared one shape.
The first clause restates the test function name, then the real content follows.
The restating line is pure duplication, and it is also the line that makes the block long enough to feel like documentation.

## Decision

Two rules replace the unfalsifiable one, both checkable without judgement:

1. A comment may not open with the identifier it documents.
2. A comment block may not exceed three lines.

Both are enforced by the lint step rather than by a reviewer reading a diff, and both are stated in `CONTRIBUTING` in their checkable form.

Rule 1 applies wherever Go's own doc convention does not: unexported declarations, everything in `_test.go`, and comments inside function bodies.
An exported declaration's doc comment is required by convention to open with its name, so it is exempt from rule 1 and not from rule 2.
Exempt from both: the package doc comment, directives, and files carrying the generated-code header.

Consecutive `//` lines are one block for rule 2, and neither a bare `//` line inside a run nor a blank line above a doc comment breaks it.
Both are ways of writing six lines of prose in front of one declaration while satisfying a three-line rule, and the blank-line form also drops the first half out of godoc.

Rule 2 will occasionally be wrong: a genuinely subtle invariant sometimes needs four lines.
That is accepted.
A rule that is right most of the time and mechanically enforced binds harder than one that is right always and enforced never.
An escape hatch is added only if a real case appears, not in advance.

## Rejected alternatives

**Keep the WHY rule and enforce it harder in review.**
The measurement says the WHY rule was already passing nearly every comment it was applied to.
Enforcing it harder means reviewers rejecting comments that satisfy the stated rule, which is arbitrary rather than strict.

**Cap the total comment count per file or per diff.**
It penalizes the one long file with three real invariants and permits the short file full of restatement.
The shape being removed is per comment, so the check is per comment.

**Ban comments in test files outright, since that is where the volume is.**
A test's non-obvious setup is exactly the kind of WHY the code cannot show.
The problem was the restating first line, not the presence of comments in tests.

**Ship an escape-hatch pragma with rule 2.**
An escape hatch available from the start is the judgement call coming back through a directive, and every four-line block will claim it.
Add it when a real case appears.

**Make the checks warnings rather than lint failures.**
A warning is the previous state with more output.
The whole point is that the rule binds without anyone adjudicating it.

**Apply rule 1 to exported doc comments too, for uniformity.**
Go's doc convention requires an exported declaration's comment to open with its name, and godoc renders it that way.
A rule that fights the toolchain gets an exemption written into the checker eventually, so it is written in from the start and scoped instead: rule 1 covers unexported declarations, `_test.go` files and in-function comments, which is where the measured restatement was.

**Count only a run of `//` lines uninterrupted by anything.**
A bare `//` inside a run, and a blank line above a doc comment, are each a way to write six lines in front of one declaration while satisfying a three-line rule.
The blank-line form also drops the first half out of godoc, so it is worse than the violation it evades.

## Consequences

Both rules are syntactic, so they are wrong sometimes in both directions.
A comment that opens with a word that happens to be the identifier is rejected even where the sentence is fine, and a three-line block of pure restatement passes.
That is the trade: mechanical and imperfect over correct and unenforced.

Every existing violation has to be fixed at once, because a checker with a grandfathered baseline is a checker that never fails.
That was 727 violations across 95 files.

Prose that outgrows three lines has to go somewhere, and `CONTRIBUTING` sends it to `SPECS.md`.
That is right only for prose a caller can depend on.
Reasoning that outgrows three lines belongs in `docs/adr/`, or `SPECS.md` regrows exactly the way that made it 2831 lines.
See `README.md` in this directory for which is which.

`tools/commentlint` is the authority for the exemptions, not this record.
A rule this mechanical will accumulate edge cases in the checker, and a record that tried to track them would be the checker written twice.
