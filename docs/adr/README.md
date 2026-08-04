# Architecture decision records

`SPECS.md` is the contract: what a caller can depend on, and what a caller can be wrong about.
This directory holds the reasoning that produced it.

Every file here answers one question `SPECS.md` deliberately does not: why is the contract shaped this way, and what was rejected to get there.
A worker who wants to change a contract clause reads the ADR behind it first, because the alternative it names is usually the change they were about to make.

## When to write one

The bar is that a future worker might undo the decision by accident.

A decision nobody could reasonably disagree with does not get an ADR.
A decision whose obvious-looking simplification is the bug it exists to prevent does.

If the reasoning is only "this is what the contract says", it is contract and belongs in `SPECS.md`.
If the reasoning is only "this is how the code happens to be written", it belongs in a code comment or nowhere.

## Format

Each record is one file, `docs/adr/<slug>.md`, with this shape:

```markdown
# <One-line statement of the decision, in the present tense>

- Date: <YYYY-MM-DD>
- Status: accepted | superseded by <slug>.md
- Issues: <fully qualified issue references, or none>
- PRs: <fully qualified PR references, or none>

## Context
## Decision
## Rejected alternatives
## Consequences
```

There is no template to install, no numbering scheme, and no index file that has to be kept correct.
The filename is the slug alone: it is an identifier a `SPECS.md` clause links to and a reader recognizes, and a date prefix on it would be metadata the linker has to remember and the reader has to ignore.
The date lives inside the file, where it belongs to the record rather than to the link.

## An ADR is never edited to match a later change

A record states what was decided on its date, and stays that way even after the decision is reversed.
Reversing one means writing a new record that states the new decision and naming the old one in its context; the old record's `Status` becomes `superseded by <new-slug>.md`, and nothing else in it changes.

Correcting a typo or a broken link is fine.
Rewriting the reasoning is not, because a record that tracks the current design is just the current design written twice, and it loses the only thing it was for.

## How `SPECS.md` points here

A `SPECS.md` section whose shape is not self-evident carries one `Why:` line at its end naming the records behind it.
One line per section, never a link per clause: the contract stays readable as a contract, and the reasoning is one hop away rather than woven back through it.
