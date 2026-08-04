# One watcher per fleet home, and ownership is an flock rather than the pid

- Date: 2026-08-04
- Status: accepted
- Issues: none
- PRs: atqamz/secondhand#138

## Context

Two `hand watch` processes on one fleet home are not a redundant pair.
Each polls herdr independently, each classifies the same transition, and each fires the notify hook, so the fleet's news arrives twice and the hook whose whole purpose is to reach an unattended operator becomes the loudest duplicate of all.

They also fight over the report channels: `report_offset` is durable and shared, so each watcher consumes lines the other has not seen.

The way a second watcher actually gets started is a supervisory session that lost the memory of having started the first one.
Compaction drops that memory, and a convention written in `AGENTS.md` is a convention compaction can drop with it.

## Decision

`hand watch` acquires ownership of the fleet home before it polls anything, and refuses with exit 3 when another watcher holds it, naming the incumbent's pid and `--takeover` as the remedy.
Validation is at the point of acquisition, in the tool.

**Ownership is an `flock` on `state/watch.pid`, never the pid the file contains.**
The pid inside is advisory: it names the incumbent in the refusal and lets `--takeover` signal it, and it is trusted only when it arrives newline-terminated, so a read that races the incumbent's own write degrades to `unknown` rather than to some other process's pid.

`--takeover` sends SIGTERM, which `hand watch` already handles as a clean shutdown, and waits up to 5s.
If the lock does not come free the takeover fails rather than proceeding.

Ownership is per fleet home and shared by both modes, so a streaming watcher also blocks `--until-event` against the same home.

## Rejected alternatives

**Check whether the recorded pid is alive.**
A lock that a crash can leave held would lock a fleet home out of watching itself, which is worse than having no lock.
Any liveness check is a heuristic that can decide wrongly: the pid may have been recycled, or the signal may be refused for reasons unrelated to liveness.
The kernel releases an `flock` when its holder dies however it died, so there is nothing stale to clear and no heuristic to get wrong.

**Write the rule in `AGENTS.md` and let the supervisory agent honor it.**
The failure mode is a session that forgot it had started a watcher.
A rule that depends on that session remembering is a rule that fails in exactly the case it exists for.

**Let a takeover proceed after the 5s wait whether or not the lock came free.**
Two watchers is the condition being prevented, so a takeover that cannot confirm the incumbent is gone must not become one.

**Give the two modes separate locks, since `--until-event` is short-lived.**
An arming watcher consumes report lines out from under a streaming one.
They contend correctly, and a caller that wants the window says so with `--takeover`.

## Consequences

Exit 3 from `hand watch` means another watcher owns the home, and that is contract a caller can branch on.

`state/watch.pid` is not authoritative for anything.
Reading it to decide whether a watcher is running gives an answer the lock may already have invalidated; taking the lock is the only way to know.

A test that runs the watcher in-process must skip the takeover signal when the recorded pid is its own process, or it SIGTERMs the test runner.
