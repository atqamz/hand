# Invariant map

The rules hand's tests are meant to hold it to, written as claims about intended behavior rather
than descriptions of current behavior. Every entry comes from an ADR or from the code that ADR
names, never from reading a test: a test derived from the implementation asserts what the code
does, which is exactly the assertion that cannot catch the code being wrong.

This is a living document. It is not an ADR, records no decision, and carries no date. The decision
that produced it is [Tests state invariants first, examples second](adr/tests-state-invariants-first-examples-second.md);
the staged work that fills it in is atqamz/hand#442.

## How to read an entry

Each invariant has a stable id, a statement, and the source that owns it. The statement is what a
test must be able to falsify. If you cannot imagine an input that would break it, it is a
description, not an invariant, and it does not belong here.

`layer` names where the invariant is meant to be checked:

- **property** - stated over generated inputs; the rule is the assertion.
- **model** - stated over generated *sequences* of operations against a reference model.
- **unit** - a specific case worth naming, underneath a property rather than instead of one.
- **manual** - checkable only against real external state; recorded here so it is not mistaken for
  something the suite covers.

`coverage` is deliberately `unaudited` throughout on first writing. Phase 0 of atqamz/hand#442
fills it in per invariant, and a phase that adds tests updates only the lines it actually touched.
An unaudited line is a question, not a claim that coverage is missing.

## Task and Attempt lifecycle

Source: [Tasks are durable and Attempts own execution](adr/tasks-are-durable-and-attempts-own-execution.md),
`internal/store`, `internal/runtime`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-TASK-1 | At most one attempt of a task is provisioning or running, at any point in any sequence of operations. | model | unaudited |
| INV-TASK-2 | Attempt ordinals within a task are contiguous and start at 1, and an ordinal is never reused. | model | unaudited |
| INV-TASK-3 | A terminal attempt is immutable with respect to activation: no operation reactivates it. | model | unaudited |
| INV-TASK-4 | The report cursor is continuous across attempts: promotion and reopen do not reset or rewind it. | model | unaudited |
| INV-TASK-5 | Teardown terminalizes the task and attempt rows and deletes neither. | property | unaudited |
| INV-TASK-6 | `hand reopen` on a terminal task creates a new attempt and mutates no existing one. | model | unaudited |
| INV-TASK-7 | Promotion preserves the scout attempt and creates the next one. | model | unaudited |
| INV-TASK-8 | An existing attempt's execution identity - harness, model, effort, execution class, planned-against commit, requested profile, routing source - is write-once. Nothing reroutes it after creation. | model | unaudited |

## Reconciliation

Source: [Deterministic reconciliation observes before mutating](adr/deterministic-reconciliation-observes-before-mutating.md),
[Liveness is observed, not assumed from launch](adr/liveness-is-observed-not-assumed-from-launch.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REC-1 | Reconciliation converges: applied twice to unchanged observed reality, the second run changes no durable state. | model | unaudited |
| INV-REC-2 | The decision table is a function of (durable intent, observed evidence). Same pair, same decision, for any pair. | property | unaudited |
| INV-REC-3 | An observation *failure* never becomes contradiction evidence, and never clears an existing repair marker. | property | unaudited |
| INV-REC-4 | A repair marker survives until the same contradiction is proven gone or a safe lifecycle transition resolves it. | model | unaudited |
| INV-REC-5 | Reconciliation applies at most one action per loop iteration, then re-observes. | model | unaudited |
| INV-REC-6 | Automatic resource cleanup requires exact ownership proof, a clean worktree, and positive proof that returning the worktree discards no commit held nowhere else. All three, never two. | property | unaudited |
| INV-REC-7 | Classifying an attempt idle-unreported changes no lifecycle, releases no lease, returns no worktree, and touches no Herdr resource. | property | unaudited |
| INV-REC-8 | The usage-limit probe fires at most once per stop. | model | unaudited |

## Init and generated surfaces

Source: [hand init is the canonical fleet reconciler](adr/hand-init-is-the-canonical-fleet-reconciler.md),
[AGENTS.md is fully Hand-owned and immutable](adr/agents-md-is-fully-hand-owned-and-immutable.md),
[hand init adopts its own source tree](adr/hand-init-adopts-its-own-source-tree.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-INIT-1 | Init is idempotent: from any home state, running it twice leaves the same state as running it once. | property | unaudited |
| INV-INIT-2 | Init resets nothing it did not create: arbitrary `data/**` content, `config/**` values, project rows, and task history all survive it. | property | unaudited |
| INV-INIT-3 | `AGENTS.md` and its `CLAUDE.md` reference are restored to canonical bytes from any drifted content, and pre-immutable content is archived rather than discarded. | property | unaudited |
| INV-INIT-4 | A skeleton file is written only when absent. Its content, once written by anyone, is never rewritten by init. | property | unaudited |
| INV-INIT-5 | A non-empty directory is adopted if and only if its `go.mod` declares hand's own module path. | property | phase 0 of atqamz/hand#436 landed unit tests; property form unaudited |
| INV-INIT-6 | Preflight refusal is total: a refused target has no byte changed, including the private runtime root. | property | unaudited |

## Registry and fleet identity

Source: [Fleet identity and user registry](adr/fleet-identity-and-user-registry.md), `internal/registry`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REG-1 | `Register` is idempotent per (home, fleet id): repeating it yields one row, not many. | property | unaudited |
| INV-REG-2 | A home's fleet identity is never reissued or rewritten by any registry operation. | property | unaudited |
| INV-REG-3 | Entry classification is a function of (stored rows, observed filesystem), and reading it mutates nothing. | property | unaudited |
| INV-REG-4 | Losing or deleting the registry changes no fleet identity; re-registering a home restores the identity its own `state/hand.db` carries. | property | unaudited |
| INV-REG-5 | Pruning removes only entries already classified `missing`, and never the current home. | property | not yet implemented - atqamz/hand#413 |
| INV-REG-6 | A full test run adds zero entries to the operator's real user registry. Reaching it from a test is impossible, not merely discouraged. | property | violated today - atqamz/hand#413 |
| INV-REG-7 | A fleet home inside a Treehouse worktree, or inside another fleet's managed tree, is refused rather than registered. | property | violated today - atqamz/hand#413 |

## Project identity and the completion store

Source: [A project is identified by a surrogate id, not its name](adr/a-project-is-identified-by-a-surrogate-id-not-its-name.md),
[Completions use an uncapped append-only sibling](adr/the-completion-store-is-an-uncapped-append-only-sibling.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-PROJ-1 | A project's `p_`-prefixed id is minted once and never reissued, for any sequence of add, rename, and remove. | model | unaudited |
| INV-PROJ-2 | Rename writes exactly one row, found by id, and nothing else. | property | unaudited |
| INV-PROJ-3 | A name is unique among registered projects and reusable after removal. | model | unaudited |
| INV-PROJ-4 | A completion record keeps the label it was written with; it is an audit line, not a view. | property | unaudited |
| INV-PROJ-5 | The completion file is append-only. The single exception - project rename rewriting only that project's records under the same lock - never touches an unrelated record, including on rollback. | property | unaudited |
| INV-PROJ-6 | Unplaceable lineage is marked, never guessed: an unresolvable project id is written as the explicit unknown value rather than empty. | property | unaudited |
| INV-PROJ-7 | A completion is appended before its task row is removed, never after. | model | unaudited |

## The report channel and attention

Source: [The report channel is the only outcome signal](adr/the-report-channel-is-the-only-outcome-signal.md),
[Attention is one derivation over three channels](adr/attention-is-one-derivation-over-three-channels.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REP-1 | `state/<id>.status` is authoritative for what a worker said. Where a stored projection disagrees with the file, the file wins. | property | unaudited |
| INV-REP-2 | Parsing the report file is total: arbitrary bytes yield a determinate result, never a panic and never a silently dropped report. | property | unaudited |
| INV-REP-3 | Evidence and claims are stored apart and rendered apart. No field carries a value from one channel in the shape the other channel's fields use. | property | unaudited |
| INV-REP-4 | Where the two channels disagree, the disagreement is what gets surfaced. Neither is silently resolved in favour of the other. | property | unaudited |
| INV-REP-5 | Acknowledgement is independent of both: reading status never clears it. | property | unaudited |

## Orientation and currentness

Source: [Stateless supervisor orientation and opaque currentness](adr/stateless-supervisor-orientation-and-opaque-currentness.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-ORI-1 | Orientation is a function of persisted state: same state, same orientation. | property | unaudited |
| INV-ORI-2 | Orientation surfaces no actionable item not backed by one of the three channels. | property | unaudited |
| INV-ORI-3 | A currentness token is opaque to every consumer: it is carried, compared, returned, or rejected, never parsed or constructed. | property | unaudited |
| INV-ORI-4 | A token is scoped to exactly one (fleet, monitor kind, target identity, generation). A token from any other tuple is rejected. | property | unaudited |
| INV-ORI-5 | `hand session start` creates no supervisor-session row and acknowledges or mutates no work. | property | unaudited |
| INV-ORI-6 | A wake is a hint only. It is accepted only when fleet, target, kind, and currentness all match exactly. | property | unaudited |
| INV-ORI-7 | Truncated orientation says it was truncated and how much it omitted; a bounded summary never silently drops an item. | property | unaudited |

## Watcher ownership and delivery

Source: [One watcher per fleet home, guarded by an flock](adr/one-watcher-per-fleet-home-guarded-by-an-flock.md),
[Watcher takeover is generation-attributed](adr/watcher-takeover-is-generation-attributed.md),
[The until-event exit is the delivery](adr/the-until-event-exit-is-the-delivery.md),
[Arming a watch observes before it waits](adr/arming-a-watch-observes-before-it-waits.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-WATCH-1 | At most one watcher per fleet home holds the kernel lock, under any interleaving of starts and takeovers. | model | unaudited |
| INV-WATCH-2 | The stored pid is advisory. No decision treats it as ownership. | property | unaudited |
| INV-WATCH-3 | Takeover proceeds only after the incumbent releases the kernel lock. | model | unaudited |
| INV-WATCH-4 | Arming observes already-actionable state before waiting, so a condition that arrived while nothing watched still wakes it. | property | unaudited |
| INV-WATCH-5 | Startup state is not an event: until-event mode takes a baseline and exits on the first *new* matching event. | property | unaudited |
| INV-WATCH-6 | Delivery, an empty window, and an unprobeable task have distinct exit codes, and each is reachable. | property | unaudited |

## Locks and schema

Source: [Lock pathnames are permanent rendezvous points](adr/lock-pathnames-are-permanent-rendezvous-points.md),
[The schema version lives in PRAGMA user_version](adr/the-schema-version-lives-in-pragma-user-version.md),
[Holds are their own table](adr/holds-are-their-own-table.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-LOCK-1 | A logical lock key maps to one permanent pathname for the life of the fleet home, created on first acquisition and never unlinked by any caller. | property | unaudited |
| INV-LOCK-2 | Neither zero size nor an old mtime is read as evidence about whether a lock is held. | property | unaudited |
| INV-SCHEMA-1 | A fresh database is created directly at the latest version; an existing one applies pending steps transactionally. | property | unaudited |
| INV-SCHEMA-2 | A database newer than the binary is refused before any other statement runs. | property | unaudited |
| INV-SCHEMA-3 | Migration is all-or-nothing: an interrupted migration leaves the recorded version and the schema agreeing with each other. | model | unaudited |
| INV-HOLD-1 | A hold is a standalone row with no foreign key to a task, and survives the task's teardown when human-authored. | property | unaudited |
| INV-HOLD-2 | A machine-authored hold is cleared only after its kind is checked, never by kind-blind clearing. | property | unaudited |

## Worktree lease and ownership proof

Source: [Unobservable ownership is not a mismatch](adr/unobservable-ownership-is-not-a-mismatch.md),
[Mechanical plans verify acquired HEAD](adr/mechanical-plans-verify-acquired-head.md),
[The landed-work guard reads the work, not the record](adr/the-landed-work-guard-reads-the-work-not-the-record.md),
[The worktree pool lives outside every fleet home](adr/the-worktree-pool-lives-outside-every-fleet-home.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-LEASE-1 | `ObserveLease` returns no error: every outcome, including the absence of an answer, is a determinate state. | property | unaudited |
| INV-LEASE-2 | Absent executable, non-zero exit, unparsable output, empty pool, and a pool describing other worktrees all classify as unknown, never as mismatch. | property | unaudited |
| INV-LEASE-3 | Unknown carries the probe that failed: the command, the working directory that selected the pool, and the reason. | property | unaudited |
| INV-LEASE-4 | Unproven ownership never authorizes a destructive command, and `--force` does not change that. | property | unaudited |
| INV-LEASE-5 | Mechanical provisioning verifies the leased worktree's full HEAD against `planned_against` before Herdr or worker launch, never after. | model | unaudited |
| INV-LEASE-6 | On mismatch or verification failure the lease is returned safely, and provisioning evidence is retained if cleanup fails. | model | unaudited |
| INV-LEASE-7 | The project lock is held continuously across verification, worktree lock, and the provisioning boundary. | model | unaudited |
| INV-LEASE-8 | Any unresolved read, parse, ref, or PR ambiguity in the landed-work guard refuses. Ambiguity never resolves to "landed". | property | unaudited |
| INV-POOL-1 | A worktree pool never sits inside any fleet home, so no harness picks up the supervisor's context by directory ancestry. | property | `TestGetAcquiresFromAPoolOutsideEveryFleetHome`, `TestReturnUsesThePoolOutsideEveryFleetHome` |
| INV-POOL-2 | Two clones never share a pool root, so one fleet's slots cannot alias another's. | property | `TestPoolRootDiffersPerClone` |
| INV-POOL-3 | A worktree recorded under its clone keeps the clone as its pool root, so a lease taken before atqamz/hand#427 stays observable and returnable. | property | `TestPoolRootKeepsALegacyWorktreeOnItsOwnClone`, `TestReturnKeepsALegacyWorktreeOnItsOwnClone` |
| INV-POOL-4 | Pool resolution never follows a foreign recorded worktree path to that path's own pool. | property | `TestReturnNeverFollowsAForeignRecordedPathToItsOwnPool` |

## Output rendering

Source: [Output is TOON by default and JSON is retained](adr/output-is-toon-by-default-and-json-is-retained.md),
`internal/axi`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-OUT-1 | A rendered block's `[N]` always equals the number of rows that follow it. | property | unaudited |
| INV-OUT-2 | Rendering is total: arbitrary field values, including quotes, newlines, commas, and non-ASCII, render to a document that parses back to the same values. | property | unaudited |
| INV-OUT-3 | An empty result renders its header with a count of `0` rather than nothing. | property | unaudited |
| INV-OUT-4 | Every failure writes exactly one document carrying `error`, `kind`, and `exit`, and a command that already produced stdout keeps it. | property | unaudited |
| INV-OUT-5 | Field selection is a projection: it changes which fields appear and never their values or their order relative to each other. | property | unaudited |
| INV-OUT-6 | A request combining TOON-only field selection with JSON is rejected, never silently honoured in part. | property | unaudited |

## Diagnosis and treatment

Source: [Every diagnosis names a reachable treatment](adr/every-diagnosis-names-a-reachable-treatment.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-DIAG-1 | Every reachable stuck state has an entry naming the supported commands that leave it, or is explicitly marked undiagnosed. | property | unaudited |
| INV-DIAG-2 | A persisted repair reason carries its treatment text with the task id substituted, so a refusal and the way out cannot drift apart. | property | unaudited |
| INV-DIAG-3 | Every treatment falls in exactly one of the three classes. | property | unaudited |

## Launch-level delivery authorization

Source: `internal/harness/harness.go` (`briefPrompt`), `internal/runtime` (spawn, reopen, promote,
reconcile), `cmd/doctor.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-AUTH-1 | Given a kind, a ship task's rendered launch prompt always states the worker is authorized to commit, push its branch, and open the pull request, regardless of what the brief itself says. | unit | `TestBuildShipCarriesDeliveryAuthorization` |
| INV-AUTH-2 | Given a kind, a scout task's rendered launch prompt always states its deliverable is a report and that it must not commit, push, or open a pull request, and never carries the ship grant. | unit | `TestBuildScoutCarriesNoDeliveryGrant` |
| INV-AUTH-4 | Every launch path supplies the task's kind to `harness.Build`: spawn, reopen, promote (always ship), and reconcile's confirm-launch arm all carry it through, so INV-AUTH-1/2 hold for a real launch and not only for a prompt built by hand. | unit | `TestSpawnBuildsHarnessCarryingTaskKind`, `TestReopenBuildsHarnessCarryingTaskKind`, `TestPromoteBuildsHarnessCarryingShipKind`, `TestReconcileConfirmLaunchCarriesTaskKind` |
| INV-AUTH-3 | `hand doctor` names every open ship task whose *last* reported state is `done` with no pull request recorded, and the finding names the command that shows the way out. This does not catch a ship worker that is functionally finished but still reporting `working:` - that case is not mechanically detectable from the report channel alone. | unit | `TestDoctorWarnsForOpenShipTaskReportedDoneWithNoPR` |

## Pure helpers

Source: `internal/shellquote`, `internal/age`, `cmd/statusview.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-PURE-1 | For any string, a real shell parses `shellquote.Quote(s)` back to exactly `s`, including spaces, quotes, newlines, and non-ASCII. | property | unaudited |
| INV-PURE-2 | Truncation never splits a UTF-8 rune, never exceeds the requested width, and is idempotent. | property | unaudited |
| INV-PURE-3 | Age rendering is monotonic in its input: a larger duration never renders as a smaller age. | property | unaudited |

## What is deliberately not an invariant

These read like invariants and are not. Each is a misreading an ADR explicitly warns about, recorded
here so nobody encodes one as a test.

- **An idle pane means the worker is dead.** It does not. An idle, still-present harness is exactly
  the shape a resume recovers. Only an explicit operator attestation may end an attempt on that
  judgment. See [Liveness is observed, not assumed from launch](adr/liveness-is-observed-not-assumed-from-launch.md).
- **An unobservable lease means ownership is wrong.** Unknown is the absence of an answer, not a
  mismatch, and nothing about it disproves the recorded ownership. See
  [Unobservable ownership is not a mismatch](adr/unobservable-ownership-is-not-a-mismatch.md).
- **The stored pid identifies the watcher.** It is advisory metadata. The kernel lock is the only
  ownership authority. See [Watcher takeover is generation-attributed](adr/watcher-takeover-is-generation-attributed.md).
- **A lock file's absence, size, or mtime says whether a lock is held.** None of them carry that
  information. See [Lock pathnames are permanent rendezvous points](adr/lock-pathnames-are-permanent-rendezvous-points.md).
- **A watcher process exiting is delivery.** Delivery is host-specific and only an owning host
  guarantees the conversion. An exit code, a notification, and an accepted request are each not
  proof that anyone reasoned.
- **A green CI run means the change is safe.** It means the suite did not object. Whether the suite
  can object is what mutation testing measures, and atqamz/hand#442 exists because that is currently
  unmeasured.
