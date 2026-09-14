---
source_issue: 304
source_title: "feat(decision): add durable Decision/Answer authority with WorkerInput delivery"
source_url: https://github.com/atqamz/hand/issues/304
source_body_updated_at: 2026-09-14T13:27:51Z
contract_version: v19-decision-answer-v1
---

# Decision / Answer authority contract

This repository snapshot transfers the accepted Decision/Answer semantics from atqamz/hand#304 into immutable release input. The GitHub issue remains a tracker; comments are historical evidence, not normative architecture state.

Decision is a standalone canonical authority episode. Answer authority remains distinct from WorkerInput creation, WorkerWake, acknowledgement, Worker action, and lifecycle progression. DDL impact of this authority transfer is none.

The architecture challenge remains resolved in favor of standalone **Decision** as an independent canonical noun. It is not TaskHold, WorkerReport, Attention, Answer, WorkerInput, or WorkerWake.

Canonical separation:

```text
WorkerReport Claim / external evidence
!= Decision
!= TaskHold
!= operator Answer
!= WorkerInput carrying that Answer to one exact Worker execution
!= WorkerInputAcknowledgement
!= WorkerWake mechanism
!= Worker action / semantic resolution / Task progression
```

A stale Answer/WorkerInput for old Plan/Attempt/Executor never targets successor work.

---

# Why standalone Decision remains

`TaskHold` and `Decision` answer different questions.

```text
TaskHold
→ why Task-level progression is intentionally deferred/prevented
→ may exist with no operator choice

Decision
→ one exact operator-authority question/choice episode
→ may be Task-, Plan-, Attempt-, or exact-evidence-scoped
→ may exist without globally holding the Task
→ owns exact Answer identity/currentness/audit history
```

Examples:

- external dependency until tomorrow → TaskHold without Decision;
- Worker asks between two reversible implementation choices → Decision may exist without TaskHold;
- A1 asks for authority and retry A2 replaces execution generation → Decision needs exact A1/evidence currentness;
- Answer remains authoritative history even if conveying it to the Worker is delayed/uncertain.

Decision exists independently because an operator-authority question is a real durable semantic episode, not because Presentation wants a card.

---

# Decision identity / lifecycle

One Decision identifies one exact authority question under one canonical currentness context.

It carries the narrowest exact owner/evidence needed, conceptually:

```text
decision_id
Task / Plan / Attempt owner where semantically required
exact triggering evidence ID where applicable
bounded question / typed choices
created_at evidence
exact open/answered/closed evidence
```

Do not use prose/timestamp/Task ID alone for execution-scoped questions, report cursor, Supervisor/browser session, pane/provider labels, or WorkerInput/Wake state as Decision identity.

Lifecycle remains deliberately small:

```text
open
→ answered
| exact stale/cancelled closure where owning semantics justify it
```

`answered` means explicit operator authority was durably recorded. It does **not** mean Worker received, acknowledged, obeyed, or acted on it.

A terminal historical Decision never reopens; successor question gets a new identity.

---

# Decision vs TaskHold

Do not automatically create a Hold for every Decision.

Valid combinations:

```text
Decision open, no TaskHold
TaskHold open, no Decision
TaskHold + Decision linked where Task-level deferral genuinely applies
Decision answered while WorkerInput/wake/ack remains unresolved
```

Never mirror Decision with `Task.needs_decision`/waiting boolean.

Current unanswered Decisions contribute to derived Attention through #302/#347.

---

# Claim / evidence / authority separation

Worker `needs-decision:` is a WorkerReport **Claim/request signal**, not Decision identity and never Answer authority.

Git/filesystem/provider Observation, Source Observation, Attention, notification, process state, WorkerInput, or WorkerWake are not Answers.

Creating a Decision from exact evidence must be explicitly typed and idempotent so replay/orient/watch/notification/Supervisor restart cannot duplicate the same semantic question.

WorkerReport acknowledgement and WorkerInput acknowledgement are both distinct from Answer.

Reads/render/status/orient/session bootstrap/notification never answer.

---

# Answer authority

An Answer names one exact Decision and records exact actor/source authority provenance.

Initial authority is explicit operator-originated input through CLI, board/TUI/GUI/mobile Interaction, or operator interaction mediated by Supervisor Harness. Channel is provenance only.

Supervisor recommendation, Worker Claim, provider Observation, notification delivery, browser presence, or wake acceptance never silently becomes operator authority.

Future automation may answer only through an explicit scoped machine-authority policy.

---

# Exact Answer currentness

Decision/Answer currentness binds exact semantic generation.

Examples:

```text
D1 targets P1/A1/evidence R1
→ retry creates A2
→ late Answer to D1 remains A1 history
→ never authorizes A2

D1 targets Plan P1
→ replan P2
→ old Answer cannot mutate P2
```

Use #345 exact relational predicates and `BEGIN IMMEDIATE` writer semantics.

A convenience “answer current decision” action must carry exact rendered Decision identity/currentness and revalidate it; it never resolves whichever Decision happens to be current at commit time.

Concurrency:

```text
same exact Answer replay
→ converge/already-applied

conflicting second Answer
→ refuse

Answer vs retry/replan/closure
→ exact one-winner predicate; loser stale
```

No last-write-wins authority.

---

# Direct Interaction vs Supervisor reasoning

An already-explicit operator Answer does not require an LLM turn merely to relay a button/form/CLI value:

```text
Presentation renders exact Decision + choices/currentness
→ operator selects exact Answer
→ canonical Answer writer
→ revalidate
→ applied | already-applied | stale/refused
```

If operator asks for recommendation/explanation first, that reasoning flows through Supervisor Harness + fresh `hand orient`; Supervisor advice is not Answer authority unless an explicit authority policy says otherwise.

---

# Answer delivery to Worker = WorkerInput

If an answered Decision must reach a live Worker, create one exact canonical `WorkerInput` whose typed origin references the exact Answer/Decision as required.

```text
Decision answered
→ BEGIN IMMEDIATE
→ require exact still-current intended Attempt + SessionBinding + ExecutorBinding
→ insert immutable ordered Answer-origin WorkerInput
→ COMMIT
```

This is a new semantic input identity. It is **not** a provider Send operation.

Required distinctions:

```text
Decision answered
!= Answer-origin WorkerInput created
!= WorkerWake prepared/submitted/accepted
!= WorkerInputAcknowledgement
!= Worker acted
!= Plan/Task progressed
```

If the target execution is already stale/replaced before WorkerInput creation, fail stale; never retarget the Answer to successor A2/E2 automatically. If delivery to successor is semantically intended, create the explicit successor authority/input under the owning currentness rule.

If crash occurs after WorkerInput commit but before wake, the Answer-delivery semantic input is still durable and must not be duplicated.

---

# WorkerWake is mechanism only

After exact Answer-origin WorkerInput exists, a provider `WorkerWake` may be prepared/submitted/reconciled under #343.

WorkerWake contains only exact execution addressing + bounded Hand-owned mechanism reason/doorbell. It does **not** carry the Answer semantic bytes as terminal control input.

Provider acceptance/triggering never means Answer delivered/acknowledged.

One wake may coalesce this Answer-origin input with other pending WorkerInputs; Worker drain order remains exact canonical ordinal order.

---

# Interactive prompt safety

The old hazard—typing arbitrary Answer/steer bytes into a terminal while provider UI is showing a menu and accidentally choosing an option—is structurally removed for canonical v19 semantic input:

```text
semantic Answer bytes
→ SQLite WorkerInput only

terminal/provider wake path
→ constant/bounded Hand-owned doorbell only
```

If the wake mechanism itself can leave staged residual external state, #343 exact write-ahead/residual cleanup applies to that mechanism. Residual uncertainty may block competing executor control, but it never decides whether the Answer/WorkerInput exists.

Do not infer WorkerInput acknowledgement from pane text remaining/disappearing.

---

# Attention / FleetSnapshot / SupervisorOrientation

Current unanswered Decisions appear through canonical #302/#347 Attention and #303 FleetSnapshot.

An answered Decision may remain visible with a separate unresolved delivery obligation:

```text
Answer-origin WorkerInput current + unacknowledged
WorkerWake degraded/unresolved
provider residual blocking control
```

These are separate evidence families/codes, not `Decision pending` mutation.

Repeated reads/wakes never duplicate Decision, Answer, or WorkerInput.

Supervisor sees current obligations through fresh `hand orient`; Supervisor runtime/session memory is never Decision currentness or authority.

---

# Migration / archive

Canonical v19+ history preserves exact Decision/Answer and WorkerInput/Acknowledgement identity.

Legacy v18 cutover must not fabricate Decision/Answer/WorkerInput/Acknowledgement history from report prose, prompt text, send-like state, terminal bytes, or cursors that cannot prove exact canonical authority/execution identity.

Archive classification never deletes authority history merely because Task/Attempt is terminal.

---

# Required tests

- Decision without TaskHold;
- TaskHold without Decision;
- exact Decision+Hold relationship where both genuinely apply;
- repeated triggering evidence replay does not duplicate Decision;
- reads/orient/watch/notify never answer;
- explicit Answer persists exact authority;
- old A1/P1 Decision/Answer cannot affect retry A2/replan P2;
- same Answer replay idempotent; conflicting Answer refuses;
- Answer vs retry/replan/closure race fails stale, never retargets;
- answered Decision creates at most one intended exact Answer-origin WorkerInput identity under idempotent retry contract;
- target becomes stale before WorkerInput creation → no successor retarget;
- crash after WorkerInput commit before WorkerWake → semantic input remains durable/no duplicate;
- WorkerWake accepted but no WorkerInputAcknowledgement → still unacknowledged;
- interactive provider menu receives no Answer semantic bytes through wake mechanism;
- Supervisor replacement loses zero Decision/Answer/input truth;
- Presentation exact Answer and Supervisor-mediated operator Answer converge on same writer semantics;
- v18 cutover fabricates none of Decision/Answer/WorkerInput/ack from unprovable legacy evidence.

---

# Acceptance criteria

- [ ] Standalone Decision remains independently justified from TaskHold/evidence/UI.
- [ ] Answer is exact authority tied to one Decision.
- [ ] Decision answered != WorkerInput created != wake != acknowledgement != action/progression.
- [ ] Execution-scoped authority binds exact Plan/Attempt/evidence and never retargets successor work.
- [ ] Claim/Observation/report ack/input ack/Attention never becomes Answer authority.
- [ ] Reads/notifications/bootstrap/orientation never answer implicitly.
- [ ] Direct exact Interaction may persist explicit Answer without unnecessary Supervisor LLM turn.
- [ ] Answer delivery uses immutable exact WorkerInput, not semantic Send/terminal bytes.
- [ ] WorkerWake is mechanism-only and provider acceptance never equals acknowledgement.
- [ ] Interactive prompt ambiguity cannot reinterpret Answer semantic payload.
- [ ] Decision/Answer/WorkerInput history survives restart/archive/v19+ migration.
- [ ] v18 cutover fabricates no authority/input history from unprovable prose/cursors/send-like state.
- [ ] No Task-level needs-decision flag, report/input cursor, generic Result/Delivery, or Presentation inbox becomes canonical authority.
