---
source_issue: 497
source_title: "a worker in a long turn never reads its queue, and hand can neither show that nor end the turn"
source_url: https://github.com/atqamz/hand/issues/497
source_body_updated_at: 2026-09-14T13:27:53Z
contract_version: v19-no-soft-turn-cancel-v1
decision: option-a
---

# No first-class soft-turn-cancel contract

This repository snapshot freezes Option A from atqamz/hand#497 as immutable v0.8 release input. The GitHub issue remains a tracker; comments and incident evidence are not normative architecture state.

v0.8 has no first-class soft-turn-cancel capability or operation. DDL impact is none.

Canonical v19 already fixes the original semantic ambiguity structurally:

```text
WorkerInput durable
!= WorkerWake
!= WorkerInputAcknowledgement
!= Worker action/outcome
```

Legacy `send submitted` / provider-composer acceptance never meant Worker consumption. In v19 the canonical proof of Worker observation is exact `WorkerInputAcknowledgement`, not provider UI queue state.

### Provider queue/UI state

A provider such as Codex may expose a UI/composer queue showing unread follow-up input. That may be captured as a typed, timestamped **Observation** when useful for diagnosis/Attention.

It is not canonical semantic-input authority and must not create, acknowledge, reorder, migrate, or settle `WorkerInput`.

Required distinction:

```text
current WorkerInput unacknowledged
!= provider UI reports queued input
!= WorkerWake degraded/unresolved
!= executor alive/dead/unknown
!= Worker ignored instruction
```

If a provider does not expose a reliable queue observation, report unknown/unsupported rather than infer queue depth from missing acknowledgement.

### Soft turn cancellation

A provider action such as Codex Escape can cancel only the current inference/tool turn while leaving the same process/session/executor alive. That is **not** canonical `Interrupt`:

```text
soft turn cancel
→ current provider turn may stop
→ exact ExecutorBinding may remain alive/current
→ pending WorkerInput may subsequently drain

Interrupt
→ targets exact ExecutorBinding
→ success requires positive cessation of that executor
```

It is also not `WorkerWake`: canceling provider computation is a mutation with different postconditions/failure semantics from making an executor notice pending input.

v0.8 therefore does **not** expose `hand send --interrupt`, `turn-cancel`, Escape injection, or another first-class Hand capability for soft-turn cancellation.

Reason: making this a supported Hand mutation would require its own exact capability/external-operation semantics, including write-ahead submission, target/currentness, strongest success postcondition, uncertainty/reconciliation, executor-control exclusion, crash behavior, and provider support. The locked #344 schema has exactly 12 external-operation kinds and no turn-cancel kind. Disguising the action as WorkerWake or Interrupt would be semantically false; adding it would require a deliberate #344 relock.

### v0.8 operator behavior

For ordinary steering:

```text
create exact WorkerInput
→ WorkerWake when supported/needed
→ observe exact WorkerInputAcknowledgement independently
```

If the operator requires **true executor cessation**, use canonical exact `Interrupt` and the owning retry/replan semantics after positive cessation evidence. Do not use provider Escape and pretend the executor was interrupted.

If an unacknowledged input remains current for a bounded policy period, #347/#302 may surface separate Attention for semantic input unacknowledged, wake degradation, provider queue Observation, and executor health. No layer may infer “Worker ignored it” from absence of acknowledgement alone.

### Release qualification

#305 must prove the chosen v0.8 semantics:

- no first-class soft-turn-cancel capability/operation is exposed;
- provider Escape/turn-cancel is not aliased to WorkerWake or Interrupt;
- `Interrupt` success still requires positive exact ExecutorBinding cessation;
- WorkerInput/Wake/Acknowledgement remain distinct;
- provider queue/UI state, when supported, is read-only typed Observation only;
- lack of queue Observation remains unknown/unsupported rather than fabricated zero;
- current unacknowledged WorkerInput can be diagnosed without treating wake/composer acceptance as acknowledgement;
- true cessation + retry never retargets old WorkerInput to the successor executor;
- fresh v19 exposes no semantic Send compatibility authority.

**DDL impact: NONE.** If v0.8 implementation later determines first-class soft-turn-cancel is mandatory, stop and reopen #343/#344/#345/#346 before implementation; do not add it as an untracked provider escape hatch.
