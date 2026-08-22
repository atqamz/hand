# Arming a watch observes before it waits

- Date: 2026-08-19
- Status: accepted
- Issues: atqamz/hand#252
- PRs: atqamz/hand#262

## Context

[Process exit delivers watcher events to the supervisor](the-until-event-exit-is-the-delivery.md) decided that until-event mode takes a silent baseline and that startup state is not an event.
That made every wake edge-triggered.
A condition already true when the watcher armed, and a condition that arrived while no watcher was alive, then had no transition left to fire on: a worker whose pane stopped before the arm, or a report written between two watchers, woke nobody.
Supervising sessions found such state only when the operator asked for it.

## Decision

The supervisor-facing wake contract is the fleet's current actionable condition **or** the next transition, not the next transition only.

Arming performs a real observation whose events are delivered.
[`internal/watcher`](../../internal/watcher) seeds each task from durable state on the first arming tick and classifies it on the second, and `RunUntilEvent` returns whatever that pass found.
`ClassifyCatchUp` asks the level question `ClassifyStatus` can only ask as an edge, and the attempt's durable `status_changed_for` is what says whether some watcher already announced the episode.
`CatchUpFilter` bounds the delivery to kinds whose announcement durable state records, so a re-arm against an already-announced condition is silent.

Announcement, not operator acknowledgement, is the idempotence unit here. The acknowledgement model is defined in [the attention record](attention-is-one-derivation-over-three-channels.md).

`hand session start` may run this same two-pass observation as a bounded arm.
Because that command exits after the bounded pass, it reports `rearmed` rather than claiming a live watcher and names `hand watch --until-event` as the next re-arm action.

## Rejected alternatives

- Keeping the silent baseline and letting supervisors poll `hand status` between arms restates the missed condition as the supervisor's problem and reintroduces the ad-hoc `sleep; poll` loop the issue rejects.
- A daemon, an event bus, or a persistent subscription registry buys nothing the durable state already in `state/hand.db` and `state/<id>.status` cannot answer at arm time, and is atqamz/hand#244's design space rather than this defect's.
- Delivering the whole arming pass unfiltered re-fires every dwell latch that is re-derived rather than persisted; `stale` alone is satisfied by any task whose herdr status has simply not changed lately, so every re-arm would return at once.
- A new durable "announced" column per condition duplicates evidence the attempt row already carries.

## Consequences

An arm can return before it ever reaches its poll loop, and `--timeout` bounds only the waiting that follows.
Events the arming pass delivers are the same events a transition would have delivered, so consumers need no new vocabulary.
A watcher process that dies between announcing and persisting re-announces on the next arm, which is the duplicate this package already prefers over a suppressed announcement.
`stale` remains recorded in `state/events.log` and on the streaming `hand watch` path, but never wakes an arm.
