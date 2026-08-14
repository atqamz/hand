# Mechanical plans verify the acquired worktree HEAD

Date: 2026-08-14

Status: accepted

Issues: atqamz/hand#214

Pull requests: atqamz/hand#227

## Context

Hand compares a mechanical brief's `planned_against` with the registered clone's local default-branch commit while holding the project lock.
Treehouse acquisition is an external boundary and may fetch `origin` and reset a leased worktree to a newer remote default-branch tip.
The project lock does not fence Git mutations performed internally by Treehouse.

## Decision

Mechanical provisioning verifies the leased worktree's full `HEAD` against `planned_against` immediately after acquisition and before Herdr or worker launch.
On mismatch or verification failure, Hand safely returns the lease and retains provisioning evidence if cleanup fails.
The project lock remains held through the verification, worktree lock, and provisioning boundary.

## Rejected alternatives

Treehouse has no pinned/no-fetch acquisition surface in the supported dependency floor, so Hand cannot make its acquisition select a requested commit directly.
Relying only on the project-base check would allow Treehouse's own fetch/reset to launch a worker against a different revision.

## Consequences

Mechanical dispatch can briefly acquire a lease when Treehouse advances the base during acquisition, but it never launches a worker from that lease unless the exact revision is verified.
The shared fake and contract suite model and verify this external-tool behavior.
