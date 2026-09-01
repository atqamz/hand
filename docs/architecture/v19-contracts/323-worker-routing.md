---
source_issue: 323
source_title: "feat(routing): add availability and quota-aware fallback within operator-approved Profiles"
source_url: https://github.com/atqamz/hand/issues/323
source_body_updated_at: 2026-09-01T03:05:14Z
contract_version: v19
milestone: 0.8.0
---

# Worker Harness routing contract

This repository snapshot is the frozen v0.8/v19 semantic contract for #323. The GitHub issue remains a tracker surface. The former normative issue comment is incorporated below and is non-normative after this snapshot lands.

## Summary

Freeze the v1-candidate **Worker Harness routing** model around canonical **Plan intent × Plan judgment**, immutable Plan meaning, and immutable per-Attempt resolved execution provenance.

This issue answers exactly one question:

> Given one immutable Plan, which Worker execution policy / Worker Harness / model / effort should a new Attempt use?

It does **not** select or configure the Fleet-level Supervisor Harness.

The operator/runtime topology is:

```text
Operator
→ Presentation
→ Interaction
  ├─ reasoning-required → Supervisor Harness
  └─ exact typed input → canonical Hand service
→ Hand core
→ Task → Plan → Attempt
                  ↓
             Worker Harness
```

Supervisor Harness selection/runtime policy is a separate configuration/runtime concern owned by #324/#336/#353. The same provider product may serve both roles independently, but the roles and configuration paths must never collapse.

---

# Replace legacy routing vocabulary

Remove the remaining legacy/pre-canonical routing vocabulary:

```text
Task kind
scout | ship
execution_class
planned_against
routing_source
promote
ambiguous reopen
one generic harness setting meaning both supervisor and worker
```

Canonical Worker routing is:

```text
Plan intent:
  explore | execute

Plan judgment:
  mechanical | bounded | substantial

Worker Route key:
  intent × judgment

Attempt request provenance:
  profile_override?
  harness_override?
  model_override?
  effort_override?

Attempt final provenance:
  profile
  harness
  model
  effort
```

Here `Attempt.harness` always means the **Worker Harness selected for that Attempt**. It does not mean Supervisor Harness.

---

# Core semantic model

## Task does not route execution

Task is the durable operator goal. It has no mutable routing kind that changes as work moves from exploration to implementation.

A Task may contain Plans such as:

```text
Explore / bounded
→ Execute / substantial
→ Explore / mechanical review
→ Execute / bounded fix
```

Routing belongs to each immutable Plan.

## Plan owns Worker-routing semantic axes

Every Plan persists exactly one:

```text
intent   = explore | execute
judgment = mechanical | bounded | substantial
```

These fields are immutable. Changing either requires a new Plan through replan/advance semantics; never mutate the existing Plan merely to obtain another Worker Harness.

## Six canonical Worker Route classes

Configuration addresses exactly:

```text
explore.mechanical
explore.bounded
explore.substantial
execute.mechanical
execute.bounded
execute.substantial
```

Each Worker Route resolves one Worker Profile according to validated current operator policy. These six keys do not apply to Supervisor Harness selection.

Do not keep `scout/ship` or generic `harness` aliases as parallel durable route concepts after the pre-v1 reset. A removed legacy flag/key may produce an actionable migration error, not a second model.

---

# Worker Profiles

A Worker Profile is named **Attempt execution policy**, conceptually containing defaults such as:

```text
Worker Harness
model
effort
required Worker Harness capabilities
bounded availability/fallback policy references
```

Configuration owns current Worker Profile definitions. Canonical Attempt rows own immutable historical resolved provenance. Deleting/changing a Worker Profile never rewrites prior Attempts.

Do not use a Worker Profile to configure Supervisor Harness product/model/effort/wake integration/runtime lifecycle. Those belong to #324's separate Supervisor runtime family.

---

# Deterministic Attempt resolution

For every new Attempt, resolve Worker execution policy before external runtime effects.

Canonical precedence:

```text
1. validate immutable Plan intent/judgment
2. resolve exact Worker Route
3. choose Route Worker Profile unless profile_override supplied
4. load selected Worker Profile defaults
5. apply explicit harness/model/effort overrides field-by-field
6. apply only typed, configured availability fallback
7. validate Worker Harness/model/effort/capability combination
8. persist request overrides + final selected values on Attempt
9. only then proceed to WorktreeCreate / SessionAcquire / Launch effects
```

There is no silent ignored override. Explicit per-field override wins only for that field.

---

# No `routing_source`; exact fallback provenance

Do not persist one scalar `routing_source`. It is intrinsically lossy because final fields may have different provenance.

The explicit override fields + selected Profile + final values explain the request/final execution policy. If availability fallback changes a value, persist narrowly typed fallback provenance/evidence sufficient to explain that decision.

The exact relational closure incorporated from the former #323 normative comment is:

```text
AttemptFallbackStep
→ exact Attempt
→ ordinal within that Attempt
→ exact rejected candidate tuple
→ one typed positive rejection reason
→ bounded typed observation/evidence digest
→ observed_at
```

Baseline fallback reasons are exactly:

```text
harness-unavailable
model-unavailable
quota-exhausted
capability-unavailable
combination-unsupported
```

`unknown` is deliberately **not** a fallback reason. If availability cannot be established positively, resolution returns the owning typed unknown/refusal outcome before external effects rather than recording a candidate as unavailable.

Required invariants:

- `UNIQUE(attempt_id, ordinal)` with deterministic 1..N writer allocation;
- every fallback row is immutable historical provenance;
- each row describes a candidate actually rejected before the final selected tuple;
- no fallback rows are required when the first candidate is selected;
- final selected values remain on Attempt rather than being inferred from a last fallback row;
- configuration Route/Profile definitions are not copied into SQLite;
- no generic routing event/result/source table is introduced;
- no generic Source/Observation table is introduced solely for routing provenance.

If the chain exhausts, is cyclic/invalid, or remains unknown under policy, fail before WorktreeCreate/SessionAcquire/Launch and do not fabricate a partially routed Attempt solely to persist the failure.

---

# Supervisor Harness separation

This separation is normative.

```text
Supervisor Harness
→ hosts Fleet-level reasoning
→ hand session start once per actual Supervisor Harness runtime/session
→ hand orient before every Supervisor reasoning turn
→ may decide to create/retry/replan/advance

Worker Harness
→ executes exactly one Attempt
→ selected by Worker Route/Profile/override policy
→ stored immutably as Attempt provenance
```

Required invariants:

- selecting a Supervisor Harness does not select the Worker Harness for an Attempt;
- selecting a Worker Harness does not replace the live Supervisor Harness;
- changing Supervisor model/effort does not rewrite Worker Route/Profile or Attempt provenance;
- changing Worker Route/Profile does not restart/reconfigure Supervisor runtime;
- the same provider product may serve both roles independently;
- `HAND_ROLE=worker` remains Worker-role enforcement;
- Supervisor readiness never proves Worker capability, or vice versa;
- no generic runtime field named only `harness` may drive both roles.

---

# Retry / replan / advance routing

Retry means:

```text
same immutable Plan
→ new Attempt
→ resolve current Worker execution policy again
```

Old Attempt provenance stays immutable. The new Attempt gets its own request/final Worker provenance. Plan intent/judgment/basis/brief remain unchanged. If requested changes alter Plan meaning, require replan.

Replan/advance create successor Plans with explicit semantics and route their first Attempt independently. There is no `promote scout → ship` shortcut. Explore and Execute are peer Plan intents, not hierarchy levels.

---

# Availability / fallback

Add only deterministic, typed **Worker execution** availability fallback that can be proven before Launch/resource effects wherever feasible.

Requirements:

- fallback chain explicit/configured, finite, cycle-free;
- capability incompatibility != quota exhaustion;
- provider/model `unknown` != unavailable;
- no unbounded probe/retry loop;
- every candidate validated before selection;
- final Attempt provenance explains actual Worker execution choice;
- existing Attempts never mutate;
- fallback happens before WorktreeCreate / SessionAcquire / Launch when mechanically knowable;
- inability to observe availability positively returns typed unknown/refusal, not fabricated certainty;
- Supervisor liveness/availability is never part of Worker fallback.

`Outcome` in routing discussion means a routing-resolution disposition, not a generic canonical Result entity.

---

# Capability and configuration boundary

#324 owns the complete typed config contract. #346 owns role/capability boundaries.

Current mutable Worker routing policy remains configuration authority, not Fleet DB truth. Fleet DB persists historical facts necessary to explain work: Plan intent/judgment, Attempt explicit overrides, selected Profile, final Worker Harness/model/effort, and narrow fallback provenance.

It does not persist a copy of current Route/Profile definitions. Supervisor Harness configuration remains a separate namespace/semantic family.

If legacy config had one generic `harness/model/effort` meaning both Supervisor and Worker defaults, migration must not guess. Map only where meaning is provable; otherwise require explicit operator choice per #324.

---

# Validation before effects

Before Worker resource/Launch effects, reject typed cases such as missing Route/Profile, unsupported Worker Harness/capability, invalid model/effort combination, unsupported override, deterministic availability failure without fallback, unknown availability where positive proof is required, exhausted/cyclic fallback, Plan basis/currentness requiring replan, or role-confused configuration.

Do not begin WorktreeCreate, SessionAcquire, or Launch before discovering a mechanically knowable configuration error.

---

# Required test matrix

Cover all six Worker Routes; route/profile/field override combinations; typed fallback and unknown; retry with changed Worker policy; replan/advance; Supervisor/Worker role collisions including the same product independently configured for both; and invariant that historical Attempts never mutate.

Assert final Attempt state fully explains request/final Worker execution provenance without `routing_source`, and that no Supervisor availability signal influences Worker fallback.

---

# Acceptance criteria

- [ ] This is explicitly **Worker Harness routing**, not generic/Supervisor Harness selection.
- [ ] Canonical routing key is Plan intent × Plan judgment with exactly six Route classes.
- [ ] Task kind/scout/ship/execution_class/planned_against/promote are absent from canonical routing semantics.
- [ ] Plan intent/judgment are immutable.
- [ ] Retry creates a new Attempt and may re-resolve current Worker policy without mutating old history.
- [ ] Replan/advance create new Plans with explicit semantic axes.
- [ ] Attempt stores explicit profile/harness/model/effort overrides and exact final provenance.
- [ ] `Attempt.harness` unambiguously means Worker Harness.
- [ ] `routing_source` is absent.
- [ ] Fallback provenance is exact typed append-only rejected-candidate evidence; `unknown` is not unavailable.
- [ ] Worker Route/Profile config changes never rewrite historical Attempts.
- [ ] Supervisor config changes never rewrite Worker routing/Attempt provenance, and Worker routing changes never restart Supervisor implicitly.
- [ ] Invalid combinations fail before external effects when knowable.
- [ ] Current Route/Profile config stays config authority rather than duplicated Fleet DB truth.
- [ ] Ambiguous legacy generic Harness config is never guessed across roles.
- [ ] Routing APIs expose exact selected provenance or typed outcome; no generic Result/RoutingResult entity exists.

## Architectural outcome

```text
Task goal
  ↓
immutable Plan(intent, judgment, basis, brief)
  ↓
Worker Route/Profile + explicit Attempt overrides
  ↓
Attempt(final immutable Worker Harness/model/effort provenance)
  ↓
Worker Harness execution
```

Supervisor reasoning remains independently configured and ephemeral. A v0.8 Attempt must explain what the Plan meant, which Worker policy was requested, what was explicitly overridden, what fallback occurred, and which Worker Harness actually executed it—without describing the Supervisor Harness.