---
source_issue: 346
source_title: "docs(architecture): lock WorkerInput/WorkerWake and canonical capability-adapter boundaries"
source_url: https://github.com/atqamz/hand/issues/346
source_body_updated_at: 2026-08-29T13:06:17Z
contract_version: v19
---

# Canonical capability and adapter contract

This repository snapshot is the immutable semantic contract captured from #346. The GitHub issue is a tracker/discussion surface; comments are not normative architecture state.

Normative for #339. Capabilities define semantic responsibility; adapters implement one capability without importing provider topology into Hand's ontology.

Owning specs:

- #343 external effects / WorkerWake / reconciliation;
- #344 exact relational schema;
- #345 lifecycle/currentness/concurrency;
- #347 read models;
- #348 cutover;
- #338 Worker Harness LaunchSpec;
- #353/#355 Supervisor runtime lifecycle/wake integration.

Canonical v19 has no `Send` capability. Semantic input is `WorkerInput`; provider notification is `WorkerWake`.

---

# Canonical vocabulary

| Term | Exact meaning |
| --- | --- |
| Fleet | one canonical orchestration tenant/home/DB |
| Project | opaque durable Git-backed body of work |
| WorkspaceBinding | immutable Project→repository-location history + physical identity evidence |
| Task | immutable operator goal |
| Plan | immutable delegated meaning/basis |
| Attempt | one execution of one Plan with immutable resolved Worker provenance |
| Supervisor Harness | ephemeral Fleet-level reasoning/runtime integration; not workflow entity |
| Worker Harness | executes one exact Attempt and returns #338 LaunchSpec |
| WorktreeBinding | exact Attempt-scoped native Git linked worktree |
| SessionBinding | exact Attempt-scoped Worker provider addressability; not Supervisor session |
| ExecutorBinding | exact Worker execution established by Launch |
| WorkerInput | immutable ordered canonical semantic instruction for one exact ExecutorBinding |
| WorkerInputAcknowledgement | immutable proof that Worker observed/drained one exact WorkerInput; not proof of action/outcome |
| WorkerWake | narrow provider mechanism allowing exact executor to observe pending WorkerInput; no semantic payload authority |
| Interrupt | exact executor-control effect whose success proves cessation |
| WorkerReport | immutable Worker Claim; not provider observation or acknowledgement |
| Observation | typed external/Git/filesystem/provider evidence |
| Attention | pure derived current actionable projection |
| SupervisorOrientation | bounded current read consumed by `hand orient` |

Never interchange SessionBinding with Supervisor runtime, paths with physical identity, WorkerInput with wake, wake acceptance with acknowledgement, WorkerReport with Observation, or Worker/Supervisor Harness roles.

No generic Result/Delivery/Isolation/ProviderResult entity.

---

# Adapter identity / common contract

`adapter_ref` exists only for genuinely replaceable provider semantics.

Core examples:

```text
builtin/git-worktree  → WorktreeCreate/Remove
herdr                 → Session / Launch / WorkerWake / Interrupt provider mechanics
```

Treehouse is not a canonical v19 Worktree/Isolation adapter. Fresh v19 has no lease/pool/generic Isolation ontology.

Common provider-facing shape is conceptually:

```text
Validate(exact canonical input)
Observe(exact subject) → found | absent | unknown + typed evidence
Prepare(exact input)  → deterministic request, no mutation
Perform(operation_key, persisted request) → typed provider disposition
```

Every adapter must document:

- exact first mutation boundary;
- strongest positive success/postcondition it can prove;
- idempotency guarantee for same operation key: yes/no;
- stable external identity/ownership evidence used for destructive actions;
- `unknown` behavior;
- typed minimal extension evidence;
- secret redaction;
- native platform behavior.

Provider acceptance never implies semantic success unless the capability's desired postcondition is exactly mechanism acceptance—and even then it cannot imply WorkerInputAcknowledgement or Worker action.

---

# WorkerInput capability boundary

Semantic input persistence belongs to Hand core/SQLite:

```text
exact current Attempt + exact ExecutorBinding
→ insert ordered immutable WorkerInput
→ semantic instruction durable
```

Provider adapters do not decide whether semantic input exists and do not own payload-delivery truth.

A Worker runtime must expose one exact provider-neutral drain path that reads all eligible pending WorkerInputs for its exact ExecutorBinding in canonical ordinal order.

No provider terminal/composer heuristic is canonical input state.

---

# WorkerWake adapter

Use exactly `WorkerWake` / operation kind `worker-wake`.

Input names exact:

```text
Attempt
SessionBinding
ExecutorBinding
operation key
bounded mechanism reason/pending boundary
```

It carries no arbitrary operator/Decision semantic prose.

Success means only the strongest mechanism postcondition the adapter can positively establish, e.g. exact host accepted/triggered the wake where that is genuinely observable. It is never equivalent to:

```text
WorkerInputAcknowledgement
Worker read all pending input
Worker obeyed instruction
WorkerReport outcome
Attempt progress
```

One wake may coalesce multiple inputs. Worker drain order remains canonical ordinal order regardless of wake completion order.

If Herdr/provider requires staged terminal bytes/keys, use one constant/bounded Hand-owned doorbell—not WorkerInput payload bytes—and expose exact residual observation/cleanup under #343.

This makes ordinary semantic input structurally safe when a provider UI is sitting on an interactive menu/prompt: semantic operator bytes never enter the terminal control stream.

Qualify separately:

```text
worker_input_drain
worker_wake
worker_wake idempotency/reconciliation
interrupt
executor observation
```

A provider may support Worker execution but degraded/unsupported automatic wake. Canonical WorkerInput remains durable/visible; do not fabricate consumption.

---

# Native Git Worktree capability

Canonical v19 Attempt worktree is native Git, `adapter_ref=builtin/git-worktree`.

WorktreeCreate success positively proves exact registered path, common repository, private gitdir, `hand:v1:<binding-id>` lock reason, exact basis HEAD, and physical filesystem identity. Git exit 0 is insufficient.

WorktreeRemove requires fresh exact ownership/registration/physical identity + no open SessionBinding + preservation safety. Unknown/mismatch blocks destruction. Clean is not sufficient to prove preservation safety. Routine `--force`/broad prune is not canonical cleanup authority.

Path/branch/task labels are locators/display only. Stale teardown cannot remove a same-path resource reallocated under a new exact identity.

---

# Session / Herdr environment boundary

SessionBinding is exact Worker provider container/addressability, not ExecutorBinding and never Supervisor session/conversation.

Session acquisition/release requires exact Worktree/currentness and positive provider postcondition evidence.

Long-lived Herdr daemon must be semantic-environment neutral:

```text
shared daemon
→ mechanism-only environment

exact child Session/Launch/drain/wake
→ Fleet/home/HAND_ROLE/Attempt/report/Harness/model values injected explicitly
```

Daemon PID/ancestry/environment is observation, never identity authority. Cross-Fleet environment contamination is a correctness failure.

---

# Worker Harness

Worker Harness executes one exact Attempt.

Input:

```text
immutable Plan meaning
immutable resolved Attempt Worker provenance
exact WorktreeBinding cwd
runtime envelope
```

Output is exact #338 structured:

```text
LaunchSpec { executable, argv[], env{}, cwd }
```

No shell-source contract. Worker Harness does not choose/create/remove Worktree, own Session topology, mutate Plan meaning, own Attention, or run supervisor bootstrap under Worker role.

Initial launch instruction is part of LaunchSpec semantics; post-launch ordinary semantic steering uses WorkerInput, not a Harness-private inbox/terminal authority.

---

# Supervisor Harness

Supervisor Harness hosts Fleet-level reasoning only:

```text
new actual runtime/session
→ hand session start once

every operator/wake reasoning turn
→ hand orient
→ fresh SupervisorOrientation
→ reason
→ exact canonical Hand operation
→ #345 writer revalidates
```

No canonical supervisor_session/conversation/current-task memory. Replacement runtime loses zero workflow truth.

Harness products may be `worker-only`, `supervisor-capable`, or both; role capability is explicit and independent.

Keep runtime health dimensions distinct:

```text
runtime identity/addressability
bootstrap integration readiness
watcher liveness
wake-delivery attachment
wake-delivery progress
orientation/reasoning progress
```

A live PID/lock/watch is never proof the control loop is healthy.

---

# Launch / Executor / Interrupt

Launch binds exact active Attempt + open WorktreeBinding + open SessionBinding + exact LaunchSpec request/digest. #343 submission precedes first provider start mutation. Success requires positive exact ExecutorBinding establishment.

Ambiguous launch creates no fabricated executor/Attempt terminality.

Interrupt targets exact ExecutorBinding. Success means positive cessation, not request acceptance. WorkerWake/Interrupt/residual cleanup share exact executor-control exclusion where mechanics interfere.

---

# Decision / Answer delivery

Standalone Decision/Answer remains #304 authority protocol.

If an Answer must reach Worker:

```text
Answer durable
→ exact Answer-origin WorkerInput durable
→ WorkerWake mechanism if needed
→ WorkerInputAcknowledgement independently proves Worker observed input
```

Provider acceptance and terminal keystrokes never become operator authority.

---

# Worker routing / configuration

#323 Route/Profile config is Worker Attempt configuration only: Worker Harness/model/effort and supported session/provider policy. It never selects a Worktree/Isolation provider.

Attempt freezes requested/resolved Worker provenance once. Supervisor Harness model/config is a separate runtime family and never rewrites Attempt history.

---

# Provider extension rule

Persist provider extension evidence only when core facts are insufficient to address exact external identity, observe exact postcondition, prove ownership, reconcile ambiguity, or explain a safety-relevant unknown.

Extensions are typed/minimal/FK-bound. No arbitrary provider JSON, duplicated lifecycle, UI state, daemon ancestry, or Supervisor conversation state.

---

# Replacement / substitutability tests

Reject #339 if:

- adding/replacing Worker Harness requires schema changes beyond immutable provider provenance;
- adding Supervisor Harness requires Task/Plan/Attempt schema;
- provider adapter invents private currentness/Attention;
- semantic WorkerInput bytes are delivered through provider interactive terminal control flow;
- WorkerWake acceptance is treated as input acknowledgement;
- one wake per message can reorder semantic inputs;
- Treehouse/generic Isolation reappears in fresh v19;
- path/PID/label becomes destructive identity;
- long-lived Herdr environment leaks Fleet/Attempt identity across children;
- Worker/Supervisor roles are inferred only from executable name.

---

# Lock conditions

- [ ] WorkerInput, WorkerInputAcknowledgement, and WorkerWake are the exact canonical nouns.
- [ ] `Send` and `ExecutorSignal` aliases are absent from canonical v19.
- [ ] WorkerWake is mechanism-only; semantic bytes stay in SQLite WorkerInput.
- [ ] Drain is exact-executor, ordered, and wake-coalescing-safe.
- [ ] Native Git Worktree ownership/cleanup uses exact identity + observation.
- [ ] Session/Executor/Supervisor runtime identities remain separate.
- [ ] Herdr daemon environment neutrality is explicit and testable.
- [ ] Worker/Supervisor Harness capabilities are role-separated.
- [ ] Adapter idempotency/unknown/destructive ownership contracts are explicit.
- [ ] #344 replacement DDL matches these boundaries before #339 starts.