# Fleet identity and user-local registry

- Date: 2026-08-22
- Status: accepted
- Issue: atqamz/hand#331
- Pull requests: none

## Context

One OS user can run several independent Hand Fleet homes.
Paths are movable and copied databases preserve durable state, so a path-derived identity or a global active-Fleet setting cannot safely identify external Herdr and Treehouse ownership.
Discovery also needs to retain missing and duplicate home evidence without becoming a second authority for Fleet state.

## Decision

Each Fleet home stores one immutable opaque Fleet identity in `state/hand.db`.
The identity is generated under the Fleet-local identity lock and is stable across restarts, moves, and registry loss.
The user-local `~/.secondhand/registry.db` stores canonical observed locators and timestamps only.
It is non-authoritative and has no Tasks, Attempts, Projects, configuration, reports, or runtime state.
Registration validates the Fleet database identity before changing the registry and transactionally retires only provably superseded locators at the same canonical home; it never changes the Fleet database.

Registration first validates the canonical Fleet identity at the supplied home.
After that positive validation, a registration transaction retires locator claims for other identities at that exact canonical home, while retaining their locators at other homes.
It removes a superseded identity row only when no locator still references it.
Unknown or unreadable canonical identity and registry evidence fail closed without changing the projection.

`hand fleet` reads registry discovery without changing the current invocation context.
There is no global active Fleet switch.
Positive duplicate or identity-mismatch evidence fails closed before runtime or mutation side effects, while registry absence or degradation is reported as a warning because it cannot prove a duplicate.
List classification is deterministic and marks only Fleets participating in an exact-home collision as ambiguous; unrelated unreadable or healthy locators retain their own states.

New Herdr resources use the Fleet identity in a named session and workspace label.
Legacy Attempts with empty or `default` sessions use the legacy session and require exact persisted workspace, tab, and pane identities for observation and cleanup.
Treehouse lease-holder text is Fleet-scoped diagnostic metadata, while persisted lease IDs and exact path observations remain the ownership proof.

Watcher takeover endpoints remain derived from canonical Fleet home plus generation rather than Fleet identity alone.
Copied homes intentionally share an identity, so they must not share an IPC route.

## Rejected alternatives

- Path-derived identity: moves and symlinks would change identity, while copied databases would be misclassified as new Fleets.
- Registry as authority: a deleted, stale, or corrupt user-local database must not change Fleet state or authorize ownership.
- Global active Fleet: it would make commands depend on ambient mutable user state and make concurrent supervisors ambiguous.
- Fleet-ID-only watcher IPC: duplicate copied homes would route takeover requests to one another.
- Label-only legacy cleanup: a project label cannot prove ownership of a particular workspace or pane.

## Consequences

Operators can discover multiple homes from outside any Fleet while each command remains explicitly bound to its resolved home.
Copied databases are visible and safe to refuse, but resolving a duplicate remains an operator decision because automatic re-keying would be irreversible and could orphan external resources.
Same-home identity replacement is the narrow exception: only a positively validated canonical database may retire the stale claim for that exact home, and the transaction is atomic, deterministic, and idempotent.
Herdr supports concurrent Fleet sessions without requiring Hand to stop or delete a named session during normal teardown.

Supervisor monitor targets and currentness tokens include the authoritative Fleet identity, while watcher ownership remains scoped by canonical home and watcher generation.
This keeps copied homes with colliding human-readable IDs from sharing wake routing or condition currentness.
