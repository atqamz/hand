---
source_issue: 324
source_title: "refactor(config): freeze typed v1 configuration, precedence, secrets, migration and deprecation rules"
source_url: https://github.com/atqamz/hand/issues/324
source_body_updated_at: 2026-09-01T03:05:15Z
contract_version: v19
milestone: 0.8.0
---

# Typed v1 configuration contract

This repository snapshot is the frozen v0.8/v19 semantic contract for #324. The GitHub issue remains a tracker surface.

Configuration owns **current operator policy/defaults**. Canonical Fleet persistence owns durable history and exact facts.

```text
configuration
→ current policy/defaults for future resolution

hand.db
→ Fleet → Project → Task → Plan → Attempt
→ immutable Worker execution provenance
→ exact resource/effect/input/evidence history

Supervisor runtime config
→ ephemeral supervising-agent policy
→ not Attempt provenance
→ not workflow currentness
```

Changing configuration affects future resolution/runtime behavior only through the owning contract. It never rewrites historical Plans, Attempts, resources, effects, WorkerInputs, acknowledgements, reports, or receipts.

---

# Canonical v19 resource boundary

Native Git WorktreeBinding is a **mandatory substrate invariant**, not configuration.

There is no supported fresh-v19 configuration key or policy dimension for:

```text
isolation_adapter
isolation_adapter_ref
worktree_provider
worktree_adapter
Treehouse isolation
Treehouse lease/pool selection
per-Attempt isolation override
```

Canonical resource chain is fixed:

```text
Attempt
→ WorktreeCreate / builtin/git-worktree
→ WorktreeBinding
→ SessionBinding
→ Launch
→ ExecutorBinding
```

Worker Route/Profile resolution may choose Worker Harness/model/effort and, where genuinely supported, Session-provider policy. It does **not** choose how the Attempt worktree is created.

Fresh canonical v19 config containing legacy Isolation/Treehouse-provider keys must be rejected or migrated only when their semantics are provably removable. Never reinterpret them as native Git WorktreeBinding configuration.

Treehouse may appear only as explicit v18 compatibility/reconciliation capability under #348 and the exact historical/release runtime that needs it. That is runtime/cutover capability, not normal v19 operator configuration.

---

# WorkerInput / WorkerWake are not configuration

Canonical post-launch semantic input is:

```text
WorkerInput
→ optional WorkerWake
→ WorkerInputAcknowledgement
```

None of these is mutable configuration.

Do not put current/pending WorkerInput, payload history, ordinal/cursor, acknowledgement state, WorkerWake state/residual/uncertainty, or current ExecutorBinding target into configuration.

There is no fresh-v19 semantic `Send` configuration or delivery-success state. Current v0.7 Send is transitional release behavior only; it is not a v1 policy primitive to preserve.

WorkerWake provider mechanics are typed capability/runtime implementation. Configuration may select only genuinely supported policy knobs; it never lets an operator replace WorkerWake with arbitrary terminal text/key injection.

---

# Supervisor wake configuration is distinct from WorkerWake

Supervisor host turn delivery under #353/#355 is an ephemeral runtime-integration concern:

```text
canonical Attention/actionability
→ Supervisor host wake/re-entry integration
→ future reasoning opportunity
→ hand orient
```

This is **not** canonical WorkerWake.

If configuration exposes Supervisor wake/runtime preferences, names and types must make that distinction explicit. Never use one generic `wake` or `signal` key whose meaning can target either Supervisor runtime re-entry or Worker ExecutorBinding WorkerWake.

Supervisor wake preferences cannot create/modify canonical `worker-wake` rows, WorkerInput acknowledgements, or workflow currentness.

---

# Typed configuration registry

Define one source of truth for key metadata, parsing, validation, defaults, help, redaction, deprecation, and compatibility behavior.

Cover only genuinely configurable policy families, including user/Fleet defaults, Worker Profile definitions, the six Worker Routes from #323, Worker Harness availability/fallback/model/effort policy, supported Session-provider policy, Supervisor Harness runtime policy, Supervisor host integration preferences, Presentation/notification/Source policy, automation policy where landed, update/distribution policy where configurable, and explicit environment overrides.

Do not force canonical/runtime facts into config: Fleet identity; Task/Plan/Attempt current IDs/lifecycle; Workspace/Worktree/Session/Executor bindings; physical identity; external-operation state; WorkerInput/Acknowledgement/Wake/Interrupt; WorkerReports; terminal/qualification/integration evidence; Hold/Backoff/Repair; Decision/Answer; observations; ActionIntent; watcher/runtime ownership/progress; or live Supervisor runtime identity.

---

# Worker routing and immutable Attempt provenance

For a new Attempt:

```text
immutable Plan intent × judgment
→ configured Route
→ selected Worker Profile
→ Profile defaults
→ explicit harness/model/effort overrides
→ capability/fallback validation
→ immutable final Attempt provenance
```

Exactly six Route keys:

```text
explore.mechanical
explore.bounded
explore.substantial
execute.mechanical
execute.bounded
execute.substantial
```

Persist exact requested/resolved Worker execution provenance required by #323/#344/#345. There is no Attempt isolation-adapter provenance.

Config change after Attempt creation never re-resolves that Attempt. Retry creates a new Attempt and may consume current routing policy. Replan/advance creates a new Plan when Plan semantics change.

Do not restore Task kind, `scout/ship`, `execution_class`, `planned_against`, or `routing_source` as canonical config-derived history.

---

# Supervisor Harness configuration is separate

Do not route Fleet Supervisor selection through Plan intent×judgment Worker Routes.

Conceptually separate Supervisor runtime policy may include only where supported:

```text
supervisor.harness
supervisor.model
supervisor.effort
supervisor runtime/wake-integration preferences
```

Required invariants:

- choosing a Supervisor provider does not choose that provider for Worker Attempts;
- changing Supervisor model/effort does not rewrite Attempt provenance;
- changing Worker Route/Profile does not silently restart/replace Supervisor runtime;
- the same product may serve both roles independently;
- unsupported Supervisor capability is diagnosed independently from Worker capability;
- live Supervisor process/session/thread identity is never config authority;
- runtime identity/readiness/wake attachment/progress are observations, not config truth.

Lifecycle remains:

```text
new Supervisor runtime
→ hand session start once

every reasoning/wake-handling turn
→ hand orient
```

Configuration may influence runtime selection/integration but never makes either command workflow authority.

---

# Scope and precedence

Every key documents an explicit semantic owner/scope. Do not invent one universal precedence stack; freeze precedence per semantic family.

Worker Attempt resolution is Plan intent/judgment → Route → Profile → explicit Attempt overrides → typed capability/fallback → immutable final provenance.

Supervisor runtime resolution is explicit invocation override where supported → user/Fleet Supervisor policy → supported default/detection → capability validation → ephemeral Supervisor runtime.

Presentation settings affect rendering/local UI only. No precedence rule may override an already-persisted exact Attempt/Executor/WorkerInput/currentness identity.

---

# Environment variables and daemon inheritance

Environment overrides are narrow, explicit, typed, documented with scope/precedence/inheritance.

`HAND_HOME` is a Fleet locator/invocation context, not Fleet identity. `HAND_ROLE=worker` is runtime role enforcement, not ordinary mutable policy.

Worker environment is the structured #338 LaunchSpec envelope, not an untyped config escape hatch.

Long-lived Herdr daemon ancestry is **not** a configuration source for semantic child identity. Fleet/home/HAND_ROLE/Attempt/report/Harness/model inputs needed by a child are injected explicitly from the current Session/Launch/input-drain/wake contract.

A stale daemon environment cannot override current config or canonical identity.

Exact Fleet-level Herdr named-session identity is derived from canonical `fleet_id`, not a mutable config key or ambient `HERDR_SESSION` authority.

---

# Secrets

Credentials/tokens do not become plain Fleet config merely because an integration needs them. Secret ownership/reference is explicit; diagnostics redact deterministically; absence may be diagnosed without echoing value; Attempt provenance persists only non-secret historical identity; WorkerInput is not a secret-management channel; Supervisor session/account tokens are never workflow state.

---

# Migration / deprecation

Define explicit representation/version behavior for additive keys/defaults, renamed/removed values, unsupported newer representations, warnings/deprecation, ambiguous old generic harness/model/effort settings, legacy Isolation/Treehouse-provider settings, legacy Send/provider steering, and ambiguous generic wake/signal settings.

If an old generic `harness` setting could mean Supervisor or Worker policy, do not guess.

If an old Isolation/Treehouse setting has no canonical v19 equivalent, remove/refuse with migration guidance rather than manufacturing a Worktree provider abstraction.

If an old Send/terminal-steering setting would imply semantic bytes transported through provider terminal input, do not map it to WorkerInput/WorkerWake automatically.

If an old generic wake key cannot distinguish Supervisor re-entry from WorkerWake, refuse/rename rather than infer a target role.

---

# Read-only surfaces

`hand config`, read-only doctor/status, FleetSnapshot, `hand orient`, presentation refresh, and `hand session start` never silently rewrite/migrate config merely by inspection.

Invalid/newer/ambiguous config is reported before external effects. Read-only diagnosis never mutates historical canonical state, creates WorkerInput/WorkerWake, acknowledges anything, or starts provider resources merely to normalize config.

---

# Structured effective-config output

Expose bounded machine-readable effective config with schema/version, redacted values, scope, semantic family, source layer/provenance, deprecated/invalid state, Worker Route/Profile validation, Supervisor capability/readiness where appropriate, and fallback summary.

Current config provenance is not historical Attempt provenance. Live Supervisor runtime identity, exact Worker ExecutorBinding, WorkerInput state, and wake-progress state are not durable config values.

---

# Required tests

Test all six Worker routes, Profile inheritance/overrides, immutable active/old Attempts under config edits, no worktree-provider selection, no WorkerInput/Ack/Wake state in config, no semantic terminal-Send mapping, distinct Supervisor-vs-Worker wake namespaces, role-specific Supervisor selection, same provider independently configured for both roles, ambiguous legacy Harness/Isolation/Send/wake refusal, unsupported newer config refusal before effect, secret redaction, environment precedence/inheritance, and read-only non-mutation.

---

# Acceptance criteria

- [ ] One typed config registry/schema owns validation/default/help/compatibility metadata or equivalent centralized contract.
- [ ] Configuration and canonical Fleet DB authority are separate.
- [ ] Native Git WorktreeBinding is fixed core substrate with no selectable config/provider key.
- [ ] No canonical `isolation_adapter_ref` or Treehouse isolation config survives fresh v19.
- [ ] Worker Route/Profile resolves only Worker execution policy plus supported Session-provider policy.
- [ ] Six Plan intent×judgment Routes are the only canonical Worker routing keys.
- [ ] Supervisor Harness config is explicitly separate from Worker Route/Profile config.
- [ ] WorkerInput/Acknowledgement/WorkerWake state is never configuration.
- [ ] No fresh-v19 semantic Send config/transport authority exists.
- [ ] Supervisor wake/re-entry config and canonical WorkerWake are distinct semantic families.
- [ ] Existing Plans/Attempts/resources/effects/inputs remain immutable under config edits.
- [ ] Retry may consume new Worker config only through a new Attempt.
- [ ] Same provider may serve Supervisor and Worker roles without config collision.
- [ ] Every key has explicit scope/authority/precedence.
- [ ] Secrets remain external/redacted.
- [ ] Environment overrides are allowlisted and role-aware.
- [ ] Long-lived Herdr environment cannot become stale semantic config source.
- [ ] Ambiguous legacy Harness/wake settings are never guessed across roles.
- [ ] Legacy Isolation/Treehouse/terminal-Send settings never manufacture new canonical abstractions.
- [ ] Unsupported newer config refuses safely before external effects.
- [ ] Read-only diagnostics do not silently rewrite policy/canonical history or create input/effects.

## Architectural outcome

Configuration is v1-ready only when no mutable knob can silently redefine historical work, semantic input, role boundaries, runtime identity, or Attempt resource ownership.