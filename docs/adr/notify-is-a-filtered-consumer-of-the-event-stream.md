# The notify hook is a filtered consumer of the event stream, with a fixed membership

- Date: 2026-08-04
- Status: accepted
- Issues: atqamz/secondhand#127
- PRs: atqamz/secondhand#131

## Context

`hand watch --until-event`'s exit reaches a supervisory session that exists and re-arms.
It has no reach when no session is running, and an unattended fleet is the normal state overnight.

So there is a second consumer of the same classified events, `config/notify`, whose whole purpose is to reach an operator with nothing watching.
Two questions follow: how it selects events, and which events it selects.

The selection is the part that goes wrong quietly.
A hook that fires on everything wakes an operator for bookkeeping and gets muted, at which point the fleet has no unattended channel at all.
A hook that fires on too little is silent for the case it exists for.

## Decision

The notify hook is its own filtered consumer of the same stream `--event` filters for stdout, not a severity test hardcoded into `handleEvent`.
`internal/watcher.NotifyFilter` builds an `EventFilter` with its own fixed membership, using the identical `EventFilter` and `Matches` mechanism `--event` uses, and `handleEvent` checks it the same way.

The membership names the kinds worth waking someone for: `blocked`, `report-blocked`, `failed`, `report-failed`, `report-needs-decision`, `report-done`, and `usage-limit-stuck`.

`report-blocked` is in the set alongside the herdr-transition `blocked` because the two are independent signals, and a worker that reports blocked and then goes idle fires no other notifiable kind: `ClassifyStatus` suppresses `idle-unreported` precisely because the last report state is set.

`idle-unreported`, `stale`, `parked`, `pr-merged` and the `pr-record-*` kinds are out.
Each describes a transition the poll loop is already tracking toward one of the seven above, or one that resolves without a human.
`usage-limit` and `usage-limit-resumed` are out for that second reason exactly, and `usage-limit-stuck` is in because it is the one of the three that says the mechanism has run out of its own answers.

`handleEvent` calls `internal/notify.Send` in-process for every match, never by shelling out to the `hand notify` subcommand, so the wiring reaches every caller of `hand watch` with no shell wrapper.
Both modes call it, whether or not the event also reached stdout, so a transition discovered on a restart's baseline tick reaches the operator the same way a live one does.

An unconfigured `config/notify` produces no diagnostic in the hook, the same silent fallback every other `config/` default gets.
A configured template that fails, or hangs past its timeout, writes one diagnostic to the watcher's stderr and the poll loop carries on.

The `hand notify` subcommand is the opposite: an absent config, an empty one, a failed template and a timed-out template are all exit 1 there.
It used to print `notified:` and exit 0 with no config at all, which made "not configured" and "delivered" the same observable outcome on the one path meant to reach an operator with nothing watching.
An empty file is the same case in a different shape, since `sh -c ""` succeeds and would claim a delivery just as wrongly, so an empty template is unconfigured rather than a template.
All four mean nothing reached the channel, which is one fact and so one error rather than four codes.

## Rejected alternatives

**Hardcode a severity test in `handleEvent`.**
Then the notify set is a condition rather than a value, and it cannot be listed, tested against a table, or compared with what `--event` accepts.
Reusing `EventFilter` means the two consumers differ only in membership.

**Notify on every event.**
It wakes an operator for `pr-merged` and `stale`, both of which the poll loop is already carrying toward something actionable, and the hook gets muted.

**Notify on terminal report states only.**
A worker that goes unreachable fires `failed` from `ClassifyUnreachable` and never writes a report line, so a dead worker would be silent on the one channel that exists for an unattended fleet.

**Shell out to `hand notify`.**
Every caller of `hand watch` would need the wrapper, and a caller that forgot it would have a watcher that detects events and notifies nobody, with nothing distinguishing it from a quiet fleet.

**Diagnose an unconfigured `config/notify`.**
Most fleets do not configure it, so the diagnostic would be permanent noise on the watcher's stderr for a default that is working as intended.

**Let `hand notify` stay quiet about an unconfigured channel too, for consistency with the hook.**
The hook is one of many things a tick does and its silence is a default.
The subcommand does exactly one thing, so its silence is a false report of having done it.

**Warn about an unconfigured channel and still exit 0.**
Exit 0 is what a caller branches on.
A warning behind a success is a delivery claim with a footnote.

**Let a failed or hanging send end the run.**
The send runs inline in the poll loop, so an unbounded one wedges polling, `--timeout` and shutdown alike.
A hook that cannot be reached must not stop the watcher that reached it.

**Rate-limit the re-fired `failed` from an already-unreachable pane.**
Its latch is deliberately non-persisted, so a restart re-fires it once for a condition that has not changed.
That is the same duplicate-over-silence trade the poll loop already accepts, and the alternative is a signal that silently stops notifying because the process restarted.
`parked` does not make that trade, and its latch is persisted, because a done task's report file never grows again so every restart re-fires against the same frozen instant (atqamz/secondhand#127).

## Consequences

Adding an event kind means deciding its notify membership explicitly, and the default of leaving it out is the safe one.

`NotifyFilter`'s membership is contract for an operator writing a `config/notify` template, because it is the complete list of what that template can ever be called for.

The hook is one more thing the poll loop does inline, so its timeout is part of the tick's bounded work that `--until-event`'s worst-case delay is measured against.
