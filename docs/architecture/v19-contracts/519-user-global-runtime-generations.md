---
source_issue: 519
source_title: "two fleet homes share one user-global runtime at ~/.secondhand, and one can destroy the other's runtime and worktrees mid-flight"
source_url: https://github.com/atqamz/hand/issues/519
source_body_updated_at: 2026-09-14T13:27:54Z
contract_version: v19-user-global-runtime-generation-v1
---

# User-global runtime generation ownership contract

This repository snapshot freezes the accepted cross-Fleet runtime ownership model from atqamz/hand#519 as immutable v0.8 release input. The GitHub issue remains a tracker; comments and incident evidence are not normative architecture state.

This is user-global runtime mechanism state under `SECONDHAND_HOME`, not canonical Fleet-DB ontology. DDL impact is none.

Hard invariant:

```text
No Fleet may destroy or invalidate runtime/integration state still required by another Fleet.
Unknown ownership/liveness blocks destructive cleanup.
```

Path existence, PID existence, process ancestry, `current.json`, process cwd, or a Fleet-home locator alone is never durable ownership authority.

### Runtime generation identity

Runtime generations are immutable and content-addressed/deterministic for one exact target/runtime contract. A generation identity is derived from the exact locked target/component content and verified installed manifest, not from wall-clock time or a random/timestamp suffix.

Materialization is:

```text
exact target contract
→ deterministic generation identity/path
→ stage privately
→ verify complete artifact/component digests + manifest
→ atomically publish if absent
→ if exact generation already exists and validates, adopt it
```

`hand runtime ensure` MUST converge on an already-valid generation. It must not mint a parallel timestamp generation merely because a selection pointer is missing/stale.

A selected/current pointer is a convenience projection for resolution. It is not ownership and cannot authorize deletion.

### Cross-Fleet live references / leases

Before a Hand-managed long-lived consumer executes from or otherwise requires a shared generation, acquire a **durable user-global generation reference/lease** under `SECONDHAND_HOME` that names at least:

```text
generation identity
stable lease/reference identity
canonical Fleet ID when a Fleet owns the consumer
consumer kind / exact Hand runtime role
created ownership evidence
```

Fleet-home path is diagnostic/locator data only; it is not the lease identity.

The live holder must also participate in a process-held kernel coordination primitive scoped to that durable lease/generation (for example the existing cross-platform file-lock substrate). Durable lease metadata proves claimed ownership; the held kernel lock is liveness/coordination evidence. PID/path/ancestry may be additional Observation only.

A crash may leave durable lease metadata behind. Recovery must reconcile it under the same user-global coordination protocol. Missing PID/path or elapsed time alone never clears a lease. If the implementation cannot positively establish that no participating holder remains, ownership is `unknown` and destructive cleanup refuses.

### Ensure / adoption

`runtime ensure`:

- takes one user-global materialization/selection lock;
- revalidates the exact target contract after lock acquisition;
- discovers the deterministic generation by identity, not by `current.json` alone;
- validates the complete existing generation before adoption;
- adopts a valid existing generation idempotently;
- materializes only when that exact generation is absent/invalid in a way safe to replace without touching another generation;
- never prunes generations, worktrees/pools, or integration payloads as a side effect.

A live process executing generation G is not by itself proof that G is valid for a new caller; adoption still validates the exact manifest/content contract. Conversely, a missing selection pointer is not evidence that G is unused.

### Prune / deletion

Normal v0.8 operation must not recursively delete the user-global Secondhand root.

Destructive runtime cleanup, if exposed, is exact-object cleanup only and requires all of:

```text
exact generation identity
user-global prune lock
zero durable live references after reconciliation
exclusive acquisition of the generation's live-holder coordination scope
no foreign Fleet reference
no unknown holder/process/integration observation
exact object still matches the generation being evaluated
```

Any foreign/live/unknown reference => refuse with typed diagnostics naming the generation and known Fleet/reference identities.

Do not use broad force/prune as ownership authority. `--force` cannot convert unknown ownership into permission.

For v0.8 the safe fallback is retention: if stale-reference recovery cannot prove deletion safe, leak disk space and report Repair/diagnostic state rather than delete a possibly-live generation.

### Integration payload lifetime

User-global integration payloads use the same principle: immutable/versioned payload identity plus exact durable references from consumers that require them. Runtime or release maintenance may remove only a specific payload after proving no foreign/live/unknown reference. Never couple integration deletion to replacement of `current.json` or a Fleet-local teardown.

### Treehouse / pools

Fresh canonical v19 Attempt ownership remains native Git `WorktreeBinding`; this issue does **not** reintroduce Treehouse/generic Isolation into v19.

Any remaining v18/v0.7 Treehouse pool/lease capability is legacy runtime/cutover mechanism state. Until that compatibility path is removed, its destructive cleanup must remain exact-lease scoped and cross-Fleet safe. No runtime cleanup may remove a pool merely because another Fleet cannot see its lease locally.

### Crash / restart / observation

After crash/restart:

- canonical Fleet DB continues to own workflow truth;
- user-global runtime lease/reference metadata owns claimed shared-runtime ownership;
- kernel coordination + typed process/provider observations establish current liveness where possible;
- stale/missing `current.json` never invalidates an otherwise valid referenced generation;
- unknown reference/liveness prevents destructive cleanup;
- recovery may converge selection/materialization without changing canonical v19 workflow state.

### Qualification requirements

#305 must prove on the exact release candidate:

- two concurrent Fleet homes can execute from one valid generation without either invalidating it;
- `runtime ensure` converges/adopts the exact valid generation rather than minting a parallel timestamp generation;
- concurrent ensure is idempotent and leaves one valid deterministic generation;
- replacing/losing the selection pointer does not make a referenced generation deletable;
- prune/delete refuses on foreign, live, or unknown references;
- crash with durable lease metadata reconciles fail-closed;
- a stale PID, reused PID, path alias, deleted cwd, or process ancestry never authorizes cleanup;
- integration payload remains while any Fleet/runtime reference needs it;
- no normal runtime operation recursively removes `SECONDHAND_HOME`;
- legacy Treehouse compatibility cleanup cannot destroy a foreign Fleet's live resource;
- runtime support/artifact identity remains coherent with exact source qualification.
