# A contended refusal names its recorded holder

- Date: 2026-08-28
- Status: accepted
- Issues: atqamz/hand#410
- PRs: none

## Context

`contend` refused a busy `state/watch.pid.lock` with "a watcher is already attached to this fleet
home", unconditionally, regardless of what actually held it. That was often wrong: `waitOwned`
(`internal/supervision/wait.go`), reached from `hand supervision wait`, `claude-stop`, and
`codex-stop`, acquires the exact same kernel lock in-process whenever no watcher currently owns the
fleet home, to poll for actionable episodes on behalf of a Supervisor Harness's Stop hook. Its
argv never contains "watch", so `pgrep -af 'hand watch'` finds nothing, and an operator who trusted
the message went looking for a watcher process that did not exist.

The refusal's remedy compounded the misdiagnosis. "Stop it through its owning session" named a
session an in-process bridge cycle does not have, and "use --takeover for cooperative replacement"
promised a protocol `waitOwned`'s holder cannot honor: `hand watch` wires `Ownership.
TakeoverRequested()` into its own cancellation (`cmd/watch.go:110`), but `waitOwned` never reads
that channel at all, so a takeover request against it is accepted by the endpoint and then ignored
by the process that should act on it. A `--takeover` contender would wait out the full grace period
and fail, having changed nothing.

The information needed to fix this was already durable by the time contention is possible:
`publishNewOwner` writes `state/watch.pid` and `state/watch.owner` while holding the authoritative
lock, before any contender can observe it busy. `contend` simply never read them. Confirmed live on
atqamz/hand#410 (issuecomment-5450929728): a real, alive `hand supervision claude-stop` process held
the lock, and `state/watch.owner` named its pid and generation the entire time.

Naming the pid was not previously done because an earlier decision
([Watcher takeover is generation-attributed](watcher-takeover-is-generation-attributed.md)) removed
pid as a *takeover target* - a stale or reused pid must never be signaled. That constraint is about
the takeover protocol's mechanics, not about diagnostic text: reporting a pid does not signal it.

## Decision

`contend` reads the owner record before composing a refusal, and the message it builds depends on
what that record shows:

- A readable record naming a bridge holder (see below) says so, states that the hold is transient,
  and does not offer `--takeover` - neither in the immediate refusal nor as a suggestion, because
  it cannot succeed.
- A readable record naming anything else keeps the existing remedy: stop it through its owning
  session, or use `--takeover`.
- An absent or unreadable record says exactly that - "the lock is held, but its ownership record
  could not be read to identify the holder" - and offers no treatment it cannot back with evidence.
  This satisfies [Every diagnosis names a reachable treatment](every-diagnosis-names-a-reachable-treatment.md):
  an unnamed holder gets no unreachable promise either.

A `--takeover` contender fails immediately against a recorded bridge holder rather than waiting out
`takeoverGrace` first: since the holder is known in advance not to observe a takeover request,
waiting out the grace period would only delay the same honest failure.

`OwnerRecord` gains a `Kind` field (`internal/watcher/owner_record.go`), populated at acquisition:
`OwnerKindWatch` for `AcquireContext` (interactive `hand watch`, wired to honor takeover) and
`OwnerKindBridge` for the new `AcquireBridgeContext` (in-process supervision bridge cycles, which
`internal/supervision/wait.go`'s `waitOwned` now calls instead of `AcquireContext`). Empty or any
value other than the two known kinds is treated the same as "not provably a bridge" for the
treatment offered - the message never claims a kind the record does not affirmatively carry.

This changes nothing about who may hold the lock, how many may hold it, how release works, or how
the generation-bound takeover endpoint itself is contacted. `Kind` is diagnostic metadata riding
alongside the existing pid and generation; it grants no authority and answers no ownership question
the kernel lock does not already settle.

## Rejected alternatives

- **Exec a real `hand watch` child from every `waitOwned` caller**, so `pgrep -af 'hand watch'`
  would find it. Rejected: this reintroduces child-process lifecycle management (start, supervise,
  reap, propagate cancellation) that the in-process design deliberately avoids, and it does not fix
  the underlying assumption that "the watcher" is always literally that one command - a future fifth
  caller would reintroduce the same misattribution.
- **Separate lock files for an interactive watch and a bridge cycle**, so an operator's manual arm
  never contends with a background cycle. Rejected here: it redesigns what "the fleet-home watcher"
  means, trading one singleton role for two, which
  [One watcher per fleet home, guarded by an flock](one-watcher-per-fleet-home-guarded-by-an-flock.md)
  does not anticipate. It remains open as a larger, separately-weighed change; this decision only
  fixes what the refusal says about the single lock that exists today.
- **Infer the holder's kind from `/proc/<pid>/cmdline`** instead of recording it. Rejected: it is
  Unix-only in a package that already carries a Windows takeover path
  (`internal/watcher/takeover_windows.go`), and it would read a fact the acquiring process already
  knows about itself at the moment it publishes ownership, for no benefit over recording it.
- **Treat every unreadable record as a bridge holder, or every readable-but-unrecognized `Kind` as
  a watch holder.** Both guess an identity from an absence. The record's own honesty standard -
  "an unreadable or absent owner record is its own answer" - applies the same way to a partially
  informative one: naming a fact not in evidence is not better than naming none.

## Consequences

A contended refusal never asserts an identity - "a watcher" - the code did not observe. Every
treatment it offers (stop the owning session, or `--takeover`) is one the named holder can actually
carry out; a bridge holder is told its hold is transient and given no dead-end remedy, and an
unidentifiable holder is told plainly that it is unidentifiable rather than handed the old universal
wording. `--takeover` against a known bridge holder fails at once instead of after a wasted grace
period. The lock model, its single-holder invariant, release ordering, and the generation-bound
takeover protocol are unchanged; `Kind` is additive routing metadata with no bearing on any of them.
