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
[Promote refuses to launch against an unrevised brief](adr/promote-refuses-to-launch-against-an-unrevised-brief.md),
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
| INV-TASK-9 | Promote refuses, and launches no attempt, when the scout attempt's recorded brief digest equals the brief's current digest. A scout attempt recorded before digest tracking existed (an empty digest) is never treated as unchanged. | unit | `TestPromoteRefusesWhenBriefIsUnchangedSinceScoutLaunch`, `TestPromoteSucceedsWhenBriefWasRewrittenSinceScoutLaunch`, `TestPromoteSucceedsWhenScoutAttemptPredatesBriefDigestRecording` |
| INV-TASK-10 | Every attempt launch records a digest of the brief it launched against, alongside `planned_against`. | unit | `TestSpawnRecordsBriefDigestAtLaunch` |
| INV-TASK-11 | Hand's own brief appendix (grok/pi launch delivery) never changes the brief digest, whether the appendix is already present or freshly appended. | unit | `TestAppendPromptToBriefLeavesBriefDigestUnchanged`, `TestDigestUnaffectedByHandsOwnAppendix`, `TestDigestChangesWhenSupervisorEditsPrecedeHandsAppendix`, `TestPromoteRefusesWhenOnlyHandsOwnAppendixWasAddedSinceScoutLaunch` |

## Reconciliation

Source: [Deterministic reconciliation observes before mutating](adr/deterministic-reconciliation-observes-before-mutating.md),
[Liveness is observed, not assumed from launch](adr/liveness-is-observed-not-assumed-from-launch.md),
[Attention is one derivation over three channels](adr/attention-is-one-derivation-over-three-channels.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REC-1 | Reconciliation converges: applied twice to unchanged observed reality, the second run changes no durable state. | model | unaudited |
| INV-REC-2 | The decision table is a function of (durable intent, observed evidence). Same pair, same decision, for any pair. | property | unaudited |
| INV-REC-3 | An observation *failure* never becomes contradiction evidence, and never clears an existing repair marker. | property | unaudited |
| INV-REC-4 | A repair marker survives until the same contradiction is proven gone or a safe lifecycle transition resolves it. | model | unaudited |
| INV-REC-5 | Reconciliation applies at most one action per loop iteration, then re-observes. | model | unaudited |
| INV-REC-6 | Automatic resource cleanup requires exact ownership proof, a clean worktree, and positive proof that returning the worktree discards no commit held nowhere else. All three, never two. | property | unaudited |
| INV-REC-7 | Classifying an attempt idle-unreported changes no lifecycle, releases no lease, returns no worktree, and touches no Herdr resource. | property | unaudited |
| INV-REC-9 | A task whose recorded PR GitHub reports merged reaches a merged lifecycle through `hand merge` or `hand reconcile`, regardless of whether its Herdr pane is present, absent, or alive, for any pane state. | property | `TestMergeConvergesOnAlreadyMergedPR`, `TestDecideTerminalConvergenceMatrix`, `TestReconcileConvergesLiveWorkerWhoseMergedPRLanded` |
| INV-REC-10 | Landing is never inferred from pane state, presence or absence: a recorded PR observed *not* merged does not interrupt a running Attempt whose pane is still alive, for any such pane observation. | property | `TestDecideTerminalConvergenceMatrix`, `TestReconcileKeepsLiveWorkerWithUnmergedRecordedPR` |
| INV-REC-11 | A merge hand performed and a merge hand only observed are recorded in distinguishable durable state (`MergeExecuted` versus `MergeAnnounced`), for either path. | unit | `TestMergeConvergesOnAlreadyMergedPR`, `TestMergePRSucceedsWhenChecksGreen` |
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

## Home resolution

Source: [Two fleet homes in play is a refusal, not a silent choice](adr/two-fleet-homes-in-play-is-a-refusal.md),
[The worktree pool lives outside every fleet home](adr/the-worktree-pool-lives-outside-every-fleet-home.md),
`internal/home/home.go` (`Resolve`), `internal/secondhand/home.go` (`PoolsRoot`), `internal/runtime/provision.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-HOME-1 | `Resolve` refuses with `ErrAmbiguousHome`, naming both paths, whenever `HAND_HOME` is set and the working directory sits inside a fleet home other than the one `HAND_HOME` names. | unit | `TestResolveRefusesWhenHandHomeAndCwdNameDifferentHomes` |
| INV-HOME-2 | `Resolve` stays silent and returns `HAND_HOME` unchanged when the working directory's nearest fleet home is the same one `HAND_HOME` names, when the working directory has no fleet home above it at all, or when `HAND_HOME` is unset. | unit | `TestResolveStaysSilentWhenHandHomeAndCwdNameTheSameHome`, `TestResolveStaysSilentWhenHandHomeSetAndCwdInsideNoHome` |
| INV-HOME-3 | A working directory inside the Treehouse worktree pool never triggers the ambiguity walk: `Resolve` returns `HAND_HOME` from a path comparison against `secondhand.PoolsRoot()` alone, so every managed worker's invocation pays no extra cost. | unit | `TestResolveUsesTheWorktreePoolShortcutWhenHandHomeIsSet` |
| INV-HOME-4 | `hand init`'s target is never `home.Resolve()`'s result: building or refreshing a fleet home is unaffected by an ambiguous or merely different `HAND_HOME`. | unit | `TestInitBuildsAScratchHomeWhileHandHomeNamesTheLiveFleet`, `TestInitRefreshesAnExistingScratchHomeWhileHandHomeNamesADifferentLiveFleet` |

## Usage-limit signatures

Source: [Usage-limit detection is a harness capability](adr/usage-limit-detection-is-a-harness-capability.md),
`internal/harness/usagelimit.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-LIMIT-1 | A catalogued harness's stop refusal is detected as a usage limit; an uncatalogued harness never is, for any text. | property | `TestSupportsUsageLimitOnlyWhereASignatureIsCatalogued`, `TestDetectUsageLimitDeclinesForAnUncataloguedHarness` land unit cases; property form unaudited |
| INV-LIMIT-2 | No catalogued signature matches an approaching-limit warning, only the stop itself. | unit | `TestDetectUsageLimitIgnoresTextThatIsNotAStop`, `TestDetectUsageLimitIgnoresCodexTextThatIsNotAStop` |
| INV-LIMIT-3 | One harness's signature never matches another harness's wording. | unit | `TestDetectUsageLimitDoesNotCrossMatchClaudeAndCodex` |
| INV-LIMIT-4 | A reset instant that does not parse degrades to no prediction, never to a guessed one, for any unrecognized wording. | property | `TestDetectCodexUsageLimitResetInstant` lands a unit case; property form unaudited |
| INV-LIMIT-5 | A dateless clock-time reset resolves against `now` in the local zone, rolling forward when the named time is not after `now`, for any (now, clock time) pair. | property | `TestDetectUsageLimitReadsTheResetInstant`, `TestDetectCodexUsageLimitResetInstant` land unit cases; property form unaudited |

## Registry and fleet identity

Source: [Fleet identity and user registry](adr/fleet-identity-and-user-registry.md),
[A test build cannot resolve the real Secondhand infrastructure root](adr/a-test-build-cannot-resolve-the-real-secondhand-root.md),
`internal/registry`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REG-1 | `Register` is idempotent per (home, fleet id): repeating it yields one row, not many. | property | `TestRegisterIsIdempotentAcrossRepeatedCalls` |
| INV-REG-2 | A home's fleet identity is never reissued or rewritten by any registry operation. | property | `TestNoRegistryOperationRewritesAHomesFleetIdentity` |
| INV-REG-3 | Entry classification is a function of (stored rows, observed filesystem), and reading it mutates nothing. | property | `TestListIsPureAndReadingMutatesNothing` |
| INV-REG-4 | Losing or deleting the registry changes no fleet identity; re-registering a home restores the identity its own `state/hand.db` carries. | property | `TestRegistryLossLosesNoIdentityAndRegisterRecoversIt` |
| INV-REG-5 | Pruning removes only entries already classified `missing`, and never the current home. | property | `TestMissingFleetsNamesOnlyEntriesClassifiedMissing`, `TestPruneRemovesOnlyEntriesClassifiedMissingAndNeverTheCurrentHome`, `TestFleetPruneApplyRemovesOnlyMissingEntries` |
| INV-REG-6 | A full test run adds zero entries to the operator's real user registry. Reaching it from a test is impossible, not merely discouraged. | property | `TestHomeRefusesWithoutOverrideUnderTestBuild` |
| INV-REG-7 | A fleet home inside a Treehouse worktree, or inside another fleet's managed tree, is refused rather than registered. | property | `TestInitRefusesATargetInsideHandsTreehousePool`, `TestInitRefusesATargetInsideAnotherFleetsProjectsTree` |

## Project identity and the completion store

Source: [A project is identified by a surrogate id, not its name](adr/a-project-is-identified-by-a-surrogate-id-not-its-name.md)
governs INV-PROJ-4 through INV-PROJ-6 as written below, including the one rewrite exception
INV-PROJ-5 names; [Completions use an uncapped append-only sibling](adr/the-completion-store-is-an-uncapped-append-only-sibling.md)
is superseded in part by it (its own status line says so) and remains the source only for the
uncapped/one-lock/append-only shape and, unaudited, for INV-PROJ-7.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-PROJ-1 | A project's `p_`-prefixed id is minted once and never reissued, for any sequence of add, rename, and remove. | model | unaudited |
| INV-PROJ-2 | Rename writes exactly one row, found by id, and nothing else. | property | unaudited |
| INV-PROJ-3 | A name is unique among registered projects and reusable after removal. | model | unaudited |
| INV-PROJ-4 | A completion record keeps the label it was written with; it is an audit line, not a view. | property | unaudited |
| INV-PROJ-5 | The completion file is append-only. The single exception - the one-time project-identity migration (`completion.MigrateProjectIdentity`) replacing the whole file through a temp file and a rename under the same lock - never touches an unrelated record, including on rollback. Corrected from an earlier wording naming project rename as the exception: rename stopped touching completion records once records and tasks started referencing a project's surrogate id instead of its mutable name (atqamz/hand#388, atqamz/hand#396); `internal/project/project.go`'s `Rename` says so at its own call site. | property | unaudited |
| INV-PROJ-6 | Unplaceable lineage is marked, never guessed: an unresolvable project id is written as the explicit unknown value rather than empty. | property | unaudited |
| INV-PROJ-7 | A completion is appended before its task row is removed, never after. | model | unaudited - likely stale, flagged rather than corrected since it is phase 5's row: [Tasks are durable and Attempts own execution](adr/tasks-are-durable-and-attempts-own-execution.md) (atqamz/hand#193) replaced task-row removal at teardown with terminalization, and no production caller of `store.DeleteTask`/`state.Delete` remains (grepped clean; only test callers). The ordering claim (append before the task-row mutation) still holds in `internal/runtime/teardown.go` - `appendCompletion` runs before `finishTeardown`'s `TerminalizeTaskAndAttempt` - but "removed" is the wrong verb for what happens now. Left for phase 5 to restate against the state machine rather than rewritten here. |

## The report channel and attention

Source: [The report channel is the only outcome signal](adr/the-report-channel-is-the-only-outcome-signal.md),
[Attention is one derivation over three channels](adr/attention-is-one-derivation-over-three-channels.md),
[A file-only harness gets the launch statement appended to its brief](adr/a-file-only-harness-gets-the-launch-statement-appended-to-its-brief.md),
[Codex's Tab queue is confirmed by composer content, Enter is not yet](adr/codex-tab-queue-submission-is-confirmed-by-composer-content.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-REP-1 | `state/<id>.status` is authoritative for what a worker said. Where a stored projection disagrees with the file, the file wins. | property | unaudited |
| INV-REP-2 | Parsing the report file is total: arbitrary bytes yield a determinate result, never a panic and never a silently dropped report. | property | unaudited |
| INV-REP-3 | Evidence and claims are stored apart and rendered apart. No field carries a value from one channel in the shape the other channel's fields use. | property | unaudited |
| INV-REP-4 | Where the two channels disagree, the disagreement is what gets surfaced. Neither is silently resolved in favour of the other. | property | unaudited |
| INV-REP-5 | Acknowledgement is independent of both: reading status never clears it. | property | unaudited |
| INV-REP-6 | A send state that records a delivery failure - pending, uncertain, partial, or not-submitted - reaches at least one supervisor-visible attention subject, and no two of them collapse to the same kind. A submitted send raises none. | unit | `TestClassifyNextActionExactPrecedence/send-not-submitted_outranks_queued_work`, `TestClassifyNextActionExactPrecedence/send-partial_outranks_send-not-submitted`, `TestDeriveRaisesSendNotSubmittedDistinctFromSendUncertain`, and `TestStatusFlagsPartialSendAfterFreshRead/submitted` cover it |
| INV-REP-7 | Every harness hand dispatches to receives the report path and the operator-decision rule in identical wording, whether as a CLI prompt argument or appended to its brief file; a harness wired into neither channel is not launched silently. | unit | `TestCarriesPrompt`, `TestAppendPromptToBriefGrokAndPi`, `TestAppendPromptToBriefIsIdempotent` |
| INV-REP-8 | Reconstructing already-persisted launch evidence (reconcile's confirm-launch arm) never mutates the brief file, even for a harness whose provisioning path appends to it. | unit | `TestReconcileConfirmLaunchDoesNotModifyBriefFile` |
| INV-REP-9 | A codex Tab/queue send reaches `submitted` only once the pane's composer is observed to no longer hold a recognizable fragment of what was sent (excluding its own queued-message echo). Two successful external calls (Text, then Tab) are never sufficient on their own for this path. | unit | `TestExecuteDoesNotConfirmCodexTabWhenMessageStaysInTheLiveComposer` (the two calls succeed and the send still lands on `not-submitted`) |
| INV-REP-10 | A codex Tab/queue send hand could not confirm is distinguishable by why: observed still holding the sent message (`not-submitted`) is a different fact from the confirmation read itself failing (`uncertain`). Neither is ever reported as `submitted`. | unit | `TestExecuteDoesNotConfirmCodexTabWhenMessageStaysInTheLiveComposer`, `TestExecuteMarksCodexTabConfirmationReadFailureUncertain` |
| INV-REP-11 | Enter has no composer-content confirmation: once Text and Enter both return success, the send is `submitted` regardless of what the composer shows afterward, and the pane is never read back to check. This is a known limitation, not a design goal - atqamz/hand#420 tracks giving Enter a working signal. | unit | `TestExecuteSubmitsOnEnterWithoutReadingTheComposerBack`, `TestSendReportsSentOnEnterRegardlessOfComposerContent` |
| INV-REP-12 | A codex Tab/queue composer that clears within the bounded confirmation poll - including a worker that consumes and finishes before hand's first read - confirms as `submitted`, with no resend on any path. | unit | `TestExecuteConfirmsCodexQueuedMessageDespiteItsOwnEchoAboveTheComposer` |
| INV-REP-13 | The submit key is Tab instead of Enter only for a codex-harness pane currently showing codex's own queue-instead-of-submit text; no other harness's submit key is ever conditioned on pane content. | unit | `TestExecuteSendsTabInsteadOfEnterWhenCodexAdvertisesQueueing`, `TestExecuteConfirmsCodexQueuedMessageDespiteItsOwnEchoAboveTheComposer`, `TestExecuteDoesNotConfirmCodexTabWhenMessageStaysInTheLiveComposer` |

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
[Arming a watch observes before it waits](adr/arming-a-watch-observes-before-it-waits.md),
[A contended refusal names its recorded holder](adr/a-contended-refusal-names-its-recorded-holder.md),
`internal/watcher/watcher.go` (`probeAllTasks`, `attemptStillNeedsArm`; atqamz/hand#455).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-WATCH-1 | At most one watcher per fleet home holds the kernel lock, under any interleaving of starts and takeovers. | model | unaudited |
| INV-WATCH-2 | The stored pid is advisory. No decision treats it as ownership. | property | unaudited |
| INV-WATCH-3 | Takeover proceeds only after the incumbent releases the kernel lock. | model | unaudited |
| INV-WATCH-4 | Arming observes already-actionable state before waiting, so a condition that arrived while nothing watched still wakes it. | property | unaudited |
| INV-WATCH-5 | Startup state is not an event: until-event mode takes a baseline and exits on the first *new* matching event. | property | unaudited |
| INV-WATCH-6 | Delivery, an empty window, and an unprobeable task have distinct exit codes, and each is reachable. | property | unaudited |
| INV-WATCH-7 | A contended refusal names the durably recorded holder - pid and generation - and never asserts an identity the owner record does not carry; an unreadable or absent record is reported as such, not guessed. | property | unit - `TestAcquireRefusesASecondWatcherAndNamesTheRecordedHolder`, `TestContendNamesABridgeHolderAndOffersNoTakeover`, `TestContendWithNoRecordSaysSoRatherThanGuessingAHolder`, `TestContendWithAMalformedRecordSaysSoRatherThanGuessingAHolder`; e2e - `TestWatchNamesALiveSupervisionBridgeHolderAndOffersNoTakeover` |
| INV-WATCH-8 | `--takeover` is offered, and attempted, only against a holder recorded as able to honor it; a recorded bridge holder refuses immediately instead of waiting out the takeover grace. | property | unit - `TestContendNamesABridgeHolderAndOffersNoTakeover`, `TestTakeoverAgainstABridgeHolderFailsFastWithoutWaitingOutTheGrace` |
| INV-WATCH-9 | Tearing a task down never fails an arm: an attempt already marked mid-teardown at the histories snapshot is skipped without a probe, and a teardown that commits after the snapshot but before that task's probe is caught by a fresh re-read on a not-found pane. | property | unit - `TestProbeAllTasksSkipsATornDownAttemptWithoutProbingItsPane`, `TestProbeAllTasksClosesTheRaceWhenATeardownCommitsAfterTheHistoriesSnapshot` |
| INV-WATCH-10 | A pane missing for an open task not being torn down still fails the arm and still names that task, even through the same not-found path INV-WATCH-9 forgives. | property | unit - `TestProbeAllTasksFailsWhenAnOpenTasksPaneIsGoneWithoutATeardown` |

## Locks and schema

Source: [Lock pathnames are permanent rendezvous points](adr/lock-pathnames-are-permanent-rendezvous-points.md),
[The schema version lives in PRAGMA user_version](adr/the-schema-version-lives-in-pragma-user-version.md),
[Holds are their own table](adr/holds-are-their-own-table.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-LOCK-1 | A logical lock key maps to one permanent pathname for the life of the fleet home, created on first acquisition and never unlinked by any caller. | property | unit - `TestLockPathnameIsTheHashOfTheLogicalKey`, `TestLockPathnamePersistsAfterRelease`, `TestDistinctKeysDoNotCollideAndDistinctHomesDoNotShare`, `TestEveryLogicalKeyShapeInTheTreeIsCovered`; property - `TestLockKeyMapsToOnePermanentPathnameAcrossRepeatedAcquisition` |
| INV-LOCK-2 | Neither zero size nor an old mtime is read as evidence about whether a lock is held. | property | `TestLockIgnoresFileSizeAndModTimeAsHeldEvidence` |
| INV-SCHEMA-1 | A fresh database is created directly at the latest version; an existing one applies pending steps transactionally. | property | unit - `TestFreshOpenRecordsSchemaVersionAtLatest`, `TestPendingMigrationAppliesAutomaticallyAndOnlyOnce`, `TestFreshDatabaseSkipsAMigrationTheSchemaAlreadyBuilds`, `TestFailedMigrationDoesNotAdvanceUserVersion`; property - `TestFreshOpenLandsDirectlyAtLatestWithoutExecutingPendingMigrations` (fresh half), `TestExistingDatabaseAppliesPendingMigrationsTransactionallyForAnyCountAndFailurePoint` (existing half) |
| INV-SCHEMA-2 | A database newer than the binary is refused before any other statement runs. | property | unit - `TestOpenRefusesADatabaseNewerThanThisBuild`; property - `TestNewerSchemaVersionRefusesBeforeAnyOtherStatementRuns` |
| INV-SCHEMA-3 | Migration is all-or-nothing: an interrupted migration leaves the recorded version and the schema agreeing with each other. | model | unaudited - an honest test needs to halt a real process between a migration step's `tx.Exec` and its `tx.Commit` (`internal/store/schemaversion.go`'s `applyMigration`), the way an OS kill or power loss would. `internal/store/store.go`'s `open` hardcodes `sql.Open("sqlite", ...)` against `modernc.org/sqlite`'s self-registered driver name, leaving no seam to intercept that boundary from a test without either a fault-injection hook in production code (which would make the row pass for the wrong reason - it would prove the Go code never *calls* Commit early, not that a real interruption leaves version and schema agreeing) or OS-level fault injection (ptrace/FUSE) disproportionate to one row. Left for whoever picks this row up next, model layer as marked. |
| INV-HOLD-1 | A hold is a standalone row with no foreign key to a task, and survives the task's teardown when human-authored. | property | unaudited |
| INV-HOLD-2 | A machine-authored hold is cleared only after its kind is checked, never by kind-blind clearing. | property | unaudited |
| INV-HOLD-3 | A blocked hold whose blocked_on task is terminal is actionable in both `hand orient` and `hand status`, naming the blocker; a blocked_on task that is still open leaves the hold exactly as before. | property | `TestOrientAndStatusReportASatisfiedBlockedHold`, `TestOrientAndStatusLeaveABlockedHoldUnchangedWhileItsBlockerRuns`, `TestDeriveMakesASatisfiedHoldActionableDespiteHeld` |
| INV-HOLD-4 | A blocked_on naming an id the store has never heard of is reported as an inconsistency, never as satisfied - a not-found lookup never reads as terminal. | property | `TestStatusFlagsAnUnknownBlockedOnAsInconsistentNotSatisfied`, `TestStatusSingleTaskShowsInconsistentHeldLine` |
| INV-HOLD-5 | Reporting a satisfied hold never clears it; only `hand hold clear` removes a hold row. | property | `TestOrientAndStatusReportASatisfiedBlockedHold` |

## Worktree lease and ownership proof

Source: [Unobservable ownership is not a mismatch](adr/unobservable-ownership-is-not-a-mismatch.md),
[Mechanical plans verify acquired HEAD](adr/mechanical-plans-verify-acquired-head.md),
[The landed-work guard reads the work, not the record](adr/the-landed-work-guard-reads-the-work-not-the-record.md),
[The worktree pool lives outside every fleet home](adr/the-worktree-pool-lives-outside-every-fleet-home.md),
[Every diagnosis names a reachable treatment](adr/every-diagnosis-names-a-reachable-treatment.md),
`internal/worktree/worktree.go` (`Get`), `cmd/doctor.go` (`doctorWorktreeFindings`), decided in
atqamz/hand#404 and atqamz/hand#421.

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
| INV-POOL-5 | Acquisition never hands back a lease whose worktree is rooted in a Git repository other than the registered clone, even when a distinct, same-named repository is reachable nearby. | unit | `TestGetRejectsAWorktreeFromAnotherRegisteredClone` |
| INV-POOL-6 | `hand doctor` reports a task's worktree rooted outside its registered clone as an error, not a warning, for any such task, so the command fails rather than merely noting it. | property | `TestDoctorFindsWorktreeUsingAnotherFleetClone`, `TestDoctorFindsWorktreeUsingTheFleetHomeCheckout` land unit cases; property form unaudited |
| INV-POOL-7 | `hand doctor` classifies a leased slot's holder into exactly one of: absent from the Fleet registry, registered but not ready, unparseable, or registered and ready. A ready holder is reported to neither of the first three, and none of the first three is reported as another. | property | `TestDoctorReportsLeaseHolderAbsentFromRegistry`, `TestDoctorReportsLeaseHolderRegisteredButNotReady`, `TestDoctorReportsUnparseableLeaseHolder`, `TestDoctorSaysNothingForLeaseHolderRegisteredAndReady` land unit cases; property form unaudited |
| INV-POOL-8 | A Fleet registry that cannot be consulted - missing entirely or unreadable - is reported as one diagnosis about the registry, never as a per-holder classification, and never once per leased slot. | property | `TestDoctorReportsAMissingRegistryAsOneFindingRatherThanPerHolderAbsence`, `TestDoctorReportsRegistryUnreadableRatherThanTreatingHoldersAsAbsent` land unit cases; property form unaudited |
| INV-POOL-9 | Lease-holder classification never returns, releases, or otherwise mutates a Treehouse lease. | property | unaudited |

## Pull-request observation and terminal-task release

Source: [Terminal release closes on evidence, not a new escape hatch](adr/terminal-release-closes-on-evidence-not-a-new-escape-hatch.md),
[Every diagnosis names a reachable treatment](adr/every-diagnosis-names-a-reachable-treatment.md),
[Cross-repo PR delivery is an explicit opt-in](adr/cross-repo-pr-delivery-is-an-explicit-opt-in.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-PR-1 | A pull request is proven absent only when HEAD is detached, no branch is recorded (durably or live), and no commit is missing from a remote-tracking ref. Any one condition failing leaves the observation unknown. | property | `TestObservePRReportsAbsentWhenDetachedHeadHasNoBranchAndNoLocalOnlyCommits`, `TestObservePRStaysUnknownWhenABranchIsRecordedDespiteDetachedHeadWithNoLocalOnlyCommits`, `TestObservePRStaysUnknownWhenDetachedHeadCarriesALocalOnlyCommit`, `TestObservePRUnaffectedByTheAbsenceProofWhenHeadIsNotDetached` |
| INV-PR-2 | Recording a pull request changes no task lifecycle column and gains a terminal task no liveness in attention, next-action ranking, or `hand status`. | property | `TestPRRecordsOnATerminalTaskWithoutReopening` |
| INV-PR-3 | `hand pr` is write-once regardless of task lifecycle: a second, different URL is refused whether the task is open or terminal, and whether or not `--cross-repo` was used. | unit | `TestPRRecordsOnATerminalTaskWithoutReopening`, `TestPRCrossRepoWriteOnceRefusesASecondDifferentURL` |
| INV-PR-4 | An attempt proven to have produced no committable work releases its worktree without a pull request, and the completion record never claims a merge for it, including across a retry. | unit | `TestTeardownReleasesADetachedWorktreeWithNoBranchAndNoLocalOnlyCommits`, `TestTeardownCircleClosesOnceHandPRRecordsTheEvidenceItAsksFor` |
| INV-PR-5 | A PR whose repo is neither the project's own nor its declared upstream is refused unless `--cross-repo` is passed, regardless of whether the project declares an upstream at all. | unit | `TestPRRefusesWhenRepoMismatch`, `TestPRRefusesSiblingRepoWithoutCrossRepoFlagAndNamesTheRealEscape` |
| INV-PR-6 | `--cross-repo` requires `--reason`, and `--reason` is refused without `--cross-repo`: neither is accepted alone. | unit | `TestPRCrossRepoWithoutReasonRefused`, `TestPRReasonWithoutCrossRepoRefused` |
| INV-PR-7 | `--cross-repo` waives only the repo-match check: the PR still has to exist and be observable on GitHub before it is recorded. | unit | `TestPRCrossRepoStillRefusesAPRThatDoesNotExistOnGitHub` |
| INV-PR-8 | A fork project's declared-upstream PR is recorded unaffected by the `--cross-repo` opt-in's existence: `hand project upstream` still means what it means, and fork PR resolution is untouched. | unit | `TestPRAcceptsTheDeclaredUpstreamAndStillRefusesAnyOtherRepo` |
| INV-PR-9 | A PR recorded through `--cross-repo` is distinguishable from a same-repo PR on every surface `hand status` renders a PR through: the `pr` field, the `flags` cell, and `--json`. | unit | `TestStatusRendersCrossRepoPRDistinctlyEverywhere` |
| INV-PR-10 | Neither `hand pr`'s own branch-based re-detection nor the watcher's auto-recording from a worker's report line ever records a PR cross-repo: the opt-in is asserted only through `hand pr`'s explicit flag. | unit | `TestDetectPRSearchesRenamedRepositoryAfterProjectSetURL` |

## Output rendering

Source: [Output is TOON by default and JSON is retained](adr/output-is-toon-by-default-and-json-is-retained.md),
`internal/axi`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-OUT-1 | A rendered block's `[N]` always equals the number of rows that follow it. | property | `TestRowsHeaderCountAgreesWithRenderedRowCount` |
| INV-OUT-2 | Rendering is total: arbitrary field values, including quotes, newlines, commas, and non-ASCII, render to a document that parses back to the same values. | property | `TestFieldValueRoundTripsThroughRender`, `TestRowValuesRoundTripThroughRender` - scoped to `Field`/`Rows` values; `List` items are not field values and are documented-lossy on embedded newlines (`oneLine`) |
| INV-OUT-3 | An empty result renders its header with a count of `0` rather than nothing. | property | unit - `TestEmptyRowsBlockKeepsCountAndSchema`, `TestTableOnNoItemsStillEmitsSchema`; property - `TestEmptyRowsRenderCountZeroWithSchemaHeader` |
| INV-OUT-4 | Every failure writes exactly one document carrying `error`, `kind`, and `exit`, and a command that already produced stdout keeps it. | property | `TestRenderErrorAlwaysWritesExactlyOneDocumentWithErrorKindAndExit` covers the document shape; `TestDoctorViolationsKeepStdoutReportAndRenderASeparateErrorDocument` (cmd layer, a real command) covers the stdout-survives-the-error-path half, which has no meaningful generated-input space |
| INV-OUT-5 | Field selection is a projection: it changes which fields appear and never their values or their order relative to each other. | property | `TestFieldSelectionProjectsValuesUnchangedInRequestedOrder` |
| INV-OUT-6 | A request combining TOON-only field selection with JSON is rejected, never silently honoured in part. | property | unit - `TestStatusFieldsWithJSONIsAUsageError`; property - `TestRejectFieldsWithJSONRefusesOnlyWhenBothAreRequested` |

## Diagnosis and treatment

Source: [Every diagnosis names a reachable treatment](adr/every-diagnosis-names-a-reachable-treatment.md).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-DIAG-1 | Every reachable stuck state has an entry naming the supported commands that leave it, or is explicitly marked undiagnosed. | property | unaudited |
| INV-DIAG-2 | A persisted repair reason carries its treatment text with the task id substituted, so a refusal and the way out cannot drift apart. | property | unaudited |
| INV-DIAG-3 | Every treatment falls in exactly one of the three classes. | property | unaudited |
| INV-DIAG-4 | The Claude wake-bridge refusal on an arm failure names a treatment that can actually observe the condition (`hand status`, which checks pane reachability), never one that cannot (`hand doctor`, which does not). | property | unaudited |

## Launch-level delivery authorization

Source: `internal/harness/harness.go` (`briefPrompt`), `internal/runtime` (spawn, reopen, promote,
reconcile), `cmd/doctor.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-AUTH-1 | Given a kind, a ship task's rendered launch prompt always states the worker is authorized to commit, push its branch, and open the pull request, regardless of what the brief itself says. | unit | `TestBuildShipCarriesDeliveryAuthorization` |
| INV-AUTH-2 | Given a kind, a scout task's rendered launch prompt always states its deliverable is a report and that it must not commit, push, or open a pull request, and never carries the ship grant. | unit | `TestBuildScoutCarriesNoDeliveryGrant` |
| INV-AUTH-4 | Every launch path supplies the task's kind to `harness.Build`: spawn, reopen, promote (always ship), and reconcile's confirm-launch arm all carry it through, so INV-AUTH-1/2 hold for a real launch and not only for a prompt built by hand. | unit | `TestSpawnBuildsHarnessCarryingTaskKind`, `TestReopenBuildsHarnessCarryingTaskKind`, `TestPromoteBuildsHarnessCarryingShipKind`, `TestReconcileConfirmLaunchCarriesTaskKind` |
| INV-AUTH-3 | `hand doctor` names every open ship task whose *last* reported state is `done` with no pull request recorded, and the finding names the command that shows the way out. This does not catch a ship worker that is functionally finished but still reporting `working:` - that case is not mechanically detectable from the report channel alone. | unit | `TestDoctorWarnsForOpenShipTaskReportedDoneWithNoPR` |

## Runtime transport readiness

Source: `internal/toolchain` (`Runtime.SupportsGitTransport`, `Store.Status`, `Store.runtimeFromCurrent`,
`GitArgsWithTemplate`), `cmd/doctor.go`, `cmd/runtime.go`, `cmd/coreexec.go`, `internal/worktree/worktree.go`
(`runCore`), `cmd/project.go` (`gitClone`, `diagnoseCloneFailure`).

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-RTGIT-1 | A runtime's `git_https_ready` reflects whether `git-remote-https` is present next to the installed git binary, observed from the bundle on disk rather than assumed from the runtime id or version - for a fake bundle with and without the helper. | unit | `TestStatusObservesInstalledRemoteHelperRatherThanRuntimeID` |
| INV-RTGIT-2 | A bundle missing the https helper is reported as a warning naming the ssh treatment, and never joins `hand doctor`'s blocking list or flips `runtime_ready` - only a runtime that fails its own integrity checks does that. | unit | `TestRuntimeHTTPSFindingsNamesTheSSHTreatmentWithoutJoiningBlocking` |
| INV-RTGIT-3 | `hand project add` replaces git's own "remote-\<scheme\>' is not a git command" text with the named runtime defect and the ssh treatment; any other git failure, including one a source rewritten by git config resolves without an external helper, passes through unchanged. | unit | `TestDiagnoseCloneFailureNamesMissingHelperInsteadOfPassingGitTextThrough`, `TestDiagnoseCloneFailureReadsTheSchemeFromGitsOwnText`, `TestDiagnoseCloneFailureLeavesUnrelatedGitErrorsUnchanged` |
| INV-RTGIT-4 | A selected runtime's `GitTemplateDir` names a directory hand has actually created on disk, under the store root, containing nothing - because the pinned bundle ships no templates for it to seed (hand#464), so pointing Git at real emptiness is honest, not hidden. | unit | `TestSelectedRuntimeCarriesAnExistingEmptyGitTemplateDirectory` |
| INV-RTGIT-5 | Every managed `git` dispatch - `cmd/coreexec.go`'s `runManagedCore` and `internal/worktree/worktree.go`'s `runCore` alike - gets `-c init.templateDir=<GitTemplateDir>` prepended ahead of its own arguments when that directory exists, and is left unchanged when it does not, via the one shared `toolchain.GitArgsWithTemplate` - never by matching or filtering Git's warning text. | unit | `TestGitArgsWithTemplatePrependsInitTemplateDir`, `TestGitArgsWithTemplateLeavesArgsUnchangedWhenNoDirectory` |

## Pure helpers

Source: `internal/shellquote`, `internal/pathdisplay`, `internal/age`, `cmd/statusview.go`.

| id | invariant | layer | coverage |
|---|---|---|---|
| INV-PURE-1 | For any string, a real shell parses `shellquote.Quote(s)` back to exactly `s`, including spaces, quotes, newlines, and non-ASCII. | property | `TestQuoteRoundTripsPOSIXShellArguments` lands unit cases; property - `TestQuoteRoundTripsAnyStringThroughARealShell` |
| INV-PURE-2 | `axi.Truncate` never splits a UTF-8 rune: a string within budget returns unchanged, and a string over budget keeps exactly its first `budget` runes as a stable, unsplit prefix - budget bounds the retained prefix, not the returned string, since the recovery annotation is deliberately appended past it - a note that could itself be truncated away would be useless. `Truncate` is meant to be applied to original text exactly once: its only caller, `cmd/status.go:82`, always passes un-truncated text, and re-truncating `Truncate`'s own output corrupts the annotation's reported total rather than merely repeating it - a precondition the next caller needs to see stated, not rediscover. | property | unit - `TestTruncateLeavesShortTextAlone`, `TestTruncateCarriesSizeAndRecoveryHint`, `TestTruncateCountsRunes`, `TestTruncateMustBeAppliedToOriginalTextOnce`; property - `TestTruncateKeepsAnUnsplitPrefixBoundedByBudget` |
| INV-PURE-3 | Age rendering is monotonic in its input: a larger duration never renders as a smaller age. | property | unit - `TestFormatDuration`, `TestFormatAge`; property - `TestFormatDurationIsMonotonic` |
| INV-PURE-4 | A path hand itself resolved and names to the operator only as context (never as something typed or run) renders via `pathdisplay.Context`, which delimits with backticks and never escapes the path's own bytes, so it never doubles a separator the way `%q` does on Windows. This does not apply to `%q` on untrusted or adversarial input (e.g. an archive member name), where showing an escaped byte such as a literal backslash is the message's own point, not a rendering defect. | unit | `TestContextDelimitsWithoutEscapingSeparators` |

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
- **`hand send` confirming submission means every submit key confirms.** Only the codex Tab/queue
  substitution does. Enter has no marker to distinguish an accepted message's own history from a
  still-unsent composer, so it stays claim-based - two successful external calls are the whole signal -
  until atqamz/hand#420 gives it a working one. See
  [Codex's Tab queue is confirmed by composer content, Enter is not yet](adr/codex-tab-queue-submission-is-confirmed-by-composer-content.md).
