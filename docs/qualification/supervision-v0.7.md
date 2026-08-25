# Supervisor v0.7 qualification record

Captured: 2026-08-25.

Issue: atqamz/hand#355.

This record qualifies exact host/runtime/platform combinations, not provider brands.

## Durable evidence

Durable evidence: `docs/qualification/supervision-wake-v1.extract.json`.

Live ledger provenance: `/home/atqa/github/atqamz/hand/state/runtime/supervision-wake.json`.

Schema: `hand.supervision.wake.v1`.

At capture, the ledger contained 221 recorded episodes.

At capture, 189 episodes contained the complete `delivery_requested`, `host_accepted`, and `oriented` triple.

The committed bounded extract in `docs/qualification/supervision-wake-v1.extract.json` preserves 16 complete episode records, their identifiers, all three receipt timestamps, the source schema, and the source SHA-256.

The captured source SHA-256 is `3bedaff024e5f39597a70ff64e734225888e10bd8e737e7d4a6c4019a9be92bd`.

The source ledger is live and may contain additional episodes after this captured snapshot.

The external ledger path and hash are provenance only; review does not depend on that path because the bounded extract is committed in this repository.

Those complete episodes span 2026-08-24 and 2026-08-25 and exceed the issue's 16-episode Claude bar.

The ledger is fleet-scoped and does not store host, runtime version, or platform on each episode.

Claude attribution comes from the contemporaneous live Claude attachment and the live-dogfood record in issue #355, not from inventing per-episode host fields.

## Matrix entry

The code matrix qualifies only this exact path:

| Dimension | Evidence |
| --- | --- |
| Host | Claude Code |
| Runtime version | `2.1.238` |
| API generation | runtime version plus positive `claude.async-rewake.v1` capability evidence; no separate host API generation claimed |
| Platform | `linux/amd64` |
| Integration | `claude.stop.async-rewake.v1` |
| Addressability | `hook-session` |
| Positive capability | `claude.async-rewake.v1` |
| Live evidence | 189 complete wake receipts in the fleet ledger; 16-record extract committed in `docs/qualification/supervision-wake-v1.extract.json` |

`supported` is emitted only when the current detection matches every matrix dimension.

An unknown or changed version, platform, integration, capability, or addressability value stays `available-unqualified` or `degraded`.

## Other hosts

Codex and OpenCode are installed in this environment as Worker harnesses, which is not Supervisor qualification evidence.

Codex remains `available-unqualified` when its exact queue, hook, and thread prerequisites are present, and becomes `unsupported` or `degraded` when those prerequisites fail.

OpenCode remains `available-unqualified` or `degraded` because no live Supervisor turn with observed assistant activity is recorded.

Pi and Grok are not installed in this environment, so their current status is `unsupported` for missing executables.

Installation alone never adds a matrix entry for any host.

## Deterministic regression evidence

The existing supervision suite covers same-episode quiet behavior, coalescing, greater-than-64 subject handling, currentness re-arming, crash-before-orient recovery, restart and corruption recovery, bridge ownership, and replacement safety.

Representative tests include `TestSameEpisodeRepeatedlyObservedDoesNotStorm`, `TestOrientedEpisodeNeverRewakesUntilCurrentnessChanges`, `TestLedgerSurvivesRestartAndRecoversFromCorruption`, `TestAcceptedThenCrashRecoversBounded`, and the cross-process attachment race tests.

These tests prove Hand's deterministic semantics and do not replace live host evidence.

## Role boundary

This qualification pass ran as `HAND_ROLE=worker`.

It did not attempt to dogfood a Supervisor wake or manufacture Claude, Codex, OpenCode, Pi, or Grok episodes.

Only the already-recorded fleet ledger and existing deterministic suite were used as evidence.
