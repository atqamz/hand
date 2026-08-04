# Ambient fleet context is a `SessionStart` hook, not a rendered file

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#62, atqamz/secondhand#64
- PRs: atqamz/secondhand#122

## Context

A supervising agent that has to ask for the fleet before it can reason about it spends a turn on what the session could have opened with.

`data/dashboard.md` was the file-based answer.
`hand` rendered the fleet into it, the agent read it as part of its context, and the file was there whether or not it was current.
It was removed with atqamz/secondhand#62, and the accuracy defects it produced are the same family as atqamz/secondhand#53: a rendering read back in as evidence.

The replacement has to arrive at the start of a session without anyone asking for it, which means something outside `hand` has to run `hand`.

## Decision

`hand init` and `hand update` install `hand` as a Claude Code `SessionStart` hook in the fleet home's `.claude/settings.json`, so every conversation opens with the bare command's overview already in context: identity, home, counts, and the task table.

The output is generated at the moment it is read, which no file can be.

`settings.json` is merged, never overwritten.
An operator's permissions, other events and other `SessionStart` entries are carried through untouched, and a file `hand` cannot parse is an error rather than a clobber.

`hand` owns at most one entry: the first whose command runs this binary or any binary named `hand`.
Refreshing repoints that entry's path and leaves any arguments the operator added alone.

Installing is confined to a fleet home.
A directory with no `state/hand.db` gets no `.claude/` directory at all.

## Rejected alternatives

**Keep `data/dashboard.md` and re-render it more often.**
More often is still not "at the moment it is read", and the failure is silent: a stale dashboard looks exactly like a current one.
It is also durable state derived from a rendering, which nothing in `hand` does any more.

**Put the overview in `AGENTS.md`, which the agent reads anyway.**
`AGENTS.md` is a generated template refreshed by `hand update`, so the same staleness applies, and the fleet state would be interleaved with the operating rules it is meant to be read against.

**Rely on the agent running `hand` first, per a rule in `AGENTS.md`.**
That is the "remember to check" pattern, and it costs a turn every time it works.

**Overwrite `settings.json` on install, since `hand` owns the fleet home.**
It does not own the operator's permissions or their other hooks.
Clobbering them is a data loss whose blast radius is outside `hand` entirely.

**Own every entry whose command mentions `hand`.**
Then an operator's own wrapper script named differently but invoking `hand` is either adopted or duplicated.
Matching this binary or a binary named `hand`, first match only, is the narrowest rule that still finds the entry after an install has moved.

**Install into any directory, so a checkout of the tool gets the hook too.**
A directory with no `state/hand.db` runs no supervising session, so the hook would fire `hand` where there is no fleet to report.

## Consequences

The mechanism is Claude Code specific.
A supervisory harness with no session-start hook gets no ambient context and has to run `hand` itself, and nothing in `hand` papers over that.

`.claude/settings.json` in a fleet home is a file two parties write, so every `hand update` is a merge that has to survive whatever the operator did since.

`hand` with no arguments is now load-bearing as the session opener rather than only as a convenience, so its output shape is contract.
