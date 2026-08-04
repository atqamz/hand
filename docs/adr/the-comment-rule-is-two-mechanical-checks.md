# The comment rule is two mechanical checks, not a judgement about whether a WHY is real

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#98
- PRs: none at time of writing

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

## Consequences

Both rules are syntactic, so they are wrong sometimes in both directions.
A comment that opens with a word that happens to be the identifier is rejected even where the sentence is fine, and a three-line block of pure restatement passes.
That is the trade: mechanical and imperfect over correct and unenforced.

Every existing violation has to be fixed at once, because a checker with a grandfathered baseline is a checker that never fails.

Doc comments on exported identifiers idiomatically open with the identifier's name.
This repo's rule takes precedence over that convention, which is a real departure from `gofmt`-adjacent Go style and is why the rule is stated in `CONTRIBUTING` rather than left implicit.
