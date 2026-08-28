# Two fleet homes in play is a refusal, not a silent choice

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#460
- PRs: none

## Context

`internal/runtime/provision.go` sets `HAND_HOME` in every managed worker's environment,
deliberately, so the worker knows the home its report channel belongs to. `internal/home.Resolve()`
checked `HAND_HOME` before ever looking at the working directory, and stopped there the moment it
found HAND_HOME to name a real fleet home.

A worker that builds a scratch fleet home to verify a change to provisioning, teardown, holds, or
steering end to end, and `cd`s into it, does not thereby isolate anything: `HAND_HOME` is still set
to the live supervising home, and every bare `hand` command reads that variable first. This was
caught read-only, during final verification for atqamz/hand#420: a worker ran `hand status` from
inside its scratch directory, expecting it to act on the scratch home, and it listed the live
fleet's seven real tasks instead. The worker then checked the live fleet's task and project lists by
hand and confirmed nothing had changed - but `hand spawn`, `hand teardown`, `hand merge` or
`hand hold` would not have stopped to ask.

Scratch fleet homes are not a rare shape. They are how this class of change gets verified, and
supervisor briefs actively ask for them.

"Always pass `HAND_HOME` explicitly" is not a fix, for the same reason atqamz/hand#413 rejected it
for the Secondhand root: an override a caller can forget to set is the bug, not the remedy for it.
The worker in the incident above did not forget in the sense of being careless - they held the
scratch home in mind and reasoned about it as the target, and `HAND_HOME` still won because nothing
made the two disagree loudly.

## Decision

When `HAND_HOME` is set, `Resolve()` now also asks whether the working directory sits inside a
*different* valid fleet home. If it does, `Resolve()` refuses with `ErrAmbiguousHome`, naming both
paths and the two ways to proceed: unset `HAND_HOME` to act on the working directory's home, or run
the command from outside that directory to act on `HAND_HOME`'s. Nothing picks a winner. The
refusal renders through the ordinary `error`/`kind`/`exit`/`help` document every `hand` command
already produces for a precondition failure, with a help line naming both remedies rather than the
generic precondition filler text.

**cwd never wins.** Making the working directory outrank `HAND_HOME` when the two disagree would
reproduce the identical hazard in the other direction: a worker whose shell happens to be sitting
inside some fleet home, running a command meant for the one named in its environment, would act on
the wrong one just as silently. `HAND_HOME` is set on purpose by provisioning specifically so a
worker's commands are not at the mercy of what directory a shell happens to be in; a resolver that
overrides it based on cwd defeats the reason it exists.

**The two unambiguous shapes stay exactly as fast as before.** A worker's cwd is a Treehouse
worktree pool slot, never a fleet home: [The worktree pool lives outside every fleet
home](the-worktree-pool-lives-outside-every-fleet-home.md) keeps a pool disjoint from every home in
one direction, and `refuseManagedTreeHome` (atqamz/hand#413) keeps a fleet home from ever being
created inside a pool in the other. Together they let `Resolve()` rule out "cwd is a different home"
with a single path comparison against `worktree.PoolsRoot()` - no stat call - before it ever reaches
for the ancestor walk. A cwd already inside `HAND_HOME` is ruled out the same cheap way. Only a
working directory outside both pays for the walk, and it pays exactly the walk `Resolve()` always
performed when `HAND_HOME` was unset; nothing about the "every managed worker" shape got slower.

**`hand init` is unaffected, on purpose.** Its target is always its explicit argument or the working
directory, resolved directly in `resolveInitHome`, never through `home.Resolve()`. A worker building
a scratch home, or refreshing one it already built, while its inherited `HAND_HOME` still names the
live fleet, keeps working exactly as before: init reconciles the directory it was told to, and its
own pre-existing `warnHandHomeMismatch` warning (unrelated to this change) still says that every
*other* command will read `HAND_HOME` instead. Root's `PersistentPreRunE` does call
`home.Resolve()` ahead of every command including `init`, but every step gated behind that call
already excludes `cmd.Name() == "init"` - a fact this decision leans on rather than duplicates - so
an ambiguous result from that call changes nothing about what `init` does.

## Rejected alternatives

- **cwd wins when the two disagree.** Rejected above: it is the same failure mode pointed the other
  way, and `HAND_HOME`'s entire purpose in a worker's environment is to not depend on cwd.
- **A `--home` flag, or "always pass `HAND_HOME` explicitly."** Discipline is not the fix; an
  instruction a caller can forget to follow reproduces the bug it was meant to prevent, which is
  exactly what atqamz/hand#413 already concluded for the Secondhand root.
- **Warn instead of refuse.** A warning a script's stdout-consuming caller never reads is silence
  with extra steps. The near-miss this fixes was read-only specifically because a human was watching
  the output; `hand spawn` or `hand teardown` invoked from automation would not have had one.
- **Refuse whenever `HAND_HOME` is set and cwd is inside *any* home, even the same one.** This would
  turn the ordinary case of a worker or supervisor invoking `hand` from inside the very home
  `HAND_HOME` already names into a refusal, which is not ambiguous and has nothing to diagnose.

## Consequences

No `hand` command can act on a fleet home other than the one its invocation unambiguously names. An
invocation that already was unambiguous - `HAND_HOME` unset, `HAND_HOME` matching cwd's home, or cwd
outside every home entirely (every managed worker) - behaves exactly as it did before this change,
at the same cost. An invocation naming two different homes now fails loudly, before touching either
one, and names the two ways out. `hand init`'s own target resolution and its existing
`HAND_HOME`-mismatch warning are untouched.
