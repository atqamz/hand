---
source_issue: 346
source_title: "docs(architecture): lock WorkerInput/WorkerWake and canonical capability-adapter boundaries"
source_url: https://github.com/atqamz/hand/issues/346
source_body_updated_at: 2026-09-24T01:41:13Z
contract_version: v19-adapters-v2
supersedes_revision: b5e4a324543df1762ba23b336b7cda9eec26029a
supersedes_path: docs/architecture/v19-contracts/346-capability-adapters.md
supersedes_blob: 859b80207a625fb4be8f5ff1a5eaf336bb7e8c77
---

# Canonical capability and adapter contract — revision 2 candidate

This candidate supersedes the revision-1 #346 snapshot when an independently reviewed permanent manifest anchors this exact Git blob. Revision-1 bytes stay immutable historical evidence. Until the anchor lands, revision 1 governs and the current fail-closed refusals of Herdr Launch, WorkerWake, Interrupt and WorkerInput drain/acknowledge stay required. The GitHub issue is a tracker; its comments are not normative architecture state.

Revision 2 changes one thing: how the `herdr` adapter proves execution identity, executable object, positive cessation and caller attestation. Hand supplies those proofs with a Hand-owned execution guard. Herdr keeps Session, terminal, addressability and doorbell transport. Every other revision-1 rule still applies. Where a section below is stricter than revision 1, the stricter rule governs.

DDL impact: **NONE**. The mapping to the anchored `docs/architecture/v19-v6.sql.gz` columns is in "Persistence without relock".

## Decision provenance

- Operator decision 2026-09-25. Canonical Launch, WorkerWake, Interrupt and WorkerInput drain/acknowledge refuse today because managed Herdr 0.8.2 cannot supply the proof each one needs. The exact refusal texts are:
  - Launch: "exact executable-object and never-reused execution-incarnation identity" (`internal/store/v19launch_herdr.go`).
  - Interrupt: "exact execution identity and positive cessation evidence" (`internal/store/v19interrupt_herdr.go`).
  - WorkerWake: "exact live execution identity" (`internal/store/v19workerwake_herdr.go`).
  - WorkerInput protocol: "exact caller-to-ExecutorBinding attestation" (`internal/store/v19launch_herdr_capability.go`, `cmd/worker_input.go`).
- Herdr Discussion #4568 has no answer. Hand does not wait for it and does not fork Herdr.
- `docs/architecture/346-provider-execution-authority-proposal.md` stays a non-normative draft. It placed execution authority at the provider's process-creation boundary and advised against a second process supervisor inside Hand. This revision makes the opposite placement decision for the `herdr` adapter. It keeps the draft's distinctions: root cessation versus tree cessation, a sampled digest versus mapped bytes, the stale-caller threat model, and no replay after `submitted`.

## Retained revision-1 contract

These revision-1 sections stay normative without change: canonical vocabulary; adapter identity and the common contract; WorkerInput capability boundary; Native Git Worktree capability; Worker Harness; Supervisor Harness; Decision / Answer delivery; Worker routing / configuration; provider extension rule; replacement / substitutability tests; lock conditions.

These sections stay normative, with the additions stated below: WorkerWake adapter (live-identity preconditions, shell-inert doorbell); Session / Herdr environment boundary (the guard scrubs the environment). The sentence "Daemon PID/ancestry/environment is observation, never identity authority" holds without exception.

The "Launch / Executor / Interrupt" section is restated below for the `herdr` adapter. Its revision-1 rules remain lower bounds. #343 submission precedes the first provider start mutation. Launch success requires positive exact ExecutorBinding establishment. Ambiguous launch fabricates nothing. Interrupt success means positive cessation, not request acceptance. WorkerWake, Interrupt and residual cleanup share executor-control exclusion.

---

# Responsibility split

```text
Herdr (adapter_ref = herdr)
  Session container, workspace/tab/pane, pane terminal
  pane addressability: pane -> terminal device, foreground process group, process list
  types one fixed-shape guard invocation into the pane shell
  transports the constant Hand-owned doorbell

hand exec-guard (inside adapter_ref = herdr; not an adapter)
  claims one Launch handoff, pins the harness executable, starts the harness
  contains the execution tree, terminates it, observes positive cessation
  writes typed guard records (provider observations)
  never opens SQLite, never decides currentness, never writes canonical rows

Hand core
  commits request and credential verifier before the first mutation
  reads OS facts itself to verify every guard record
  performs every canonical transition under #343/#345
```

The guard is a Hand mechanism, not a replaceable provider, so it gets no `adapter_ref` of its own. A guard record is a claim by a Hand-owned mechanism. Hand accepts a record only when facts that Hand reads from the OS itself corroborate it.

Identity authority comes only from guard records corroborated by OS facts that Hand reads. It never comes from Herdr daemon PID, ancestry or environment; from a bare PID, path, argv, basename or label; or from pane text. Herdr's pane reports (terminal device, foreground process group, process list) are addressability locators. A binding requires them. They are never sufficient.

Host precondition: Hand, the Herdr panes and the guard must share one kernel boot, one PID namespace and one OS user. Otherwise the guard capability is unsupported on that host, and the revision-1 refusals remain.

# Subjects

| Subject | Meaning |
| --- | --- |
| L | one exact `launch` external operation; `launch_operation.binding_id` pre-allocates B |
| B | the ExecutorBinding id that L establishes on success |
| S_B | 32 random bytes from a CSPRNG, generated by Hand core in the process that prepares L |
| V_B | SHA-256 over domain-separated fields `hand:v19:exec-guard-credential:v1`, Fleet ID, B, S_B |
| handoff(L) | Fleet-private file with the exact resolved LaunchSpec, request digest, launch-spec digest, B and S_B |
| G | guard incarnation: (boot identity, guard PID, guard OS start time) |
| R | harness root incarnation, recorded by the guard as its direct child |
| P | harness process group (POSIX), created by the guard; P is the pane terminal's foreground group |
| T(G) | execution tree: Linux, all descendants of G; Windows, every process in G's job; macOS, every process in P |
| records(L) | guard records `claimed`, `pinned`, `running`, `refused`, `ceased`, and Hand's `interrupt-request` and `abort-request`; each names L and G |

Records and the handoff live in a Fleet-private directory under the Fleet home. The directory has mode 0700; on Windows it inherits the user-private ACL of the Fleet home. The implementation owns the exact path and encoding. Records are written with a temp file and an atomic rename. An unparsable, torn or wrong-version record counts as absent.

---

# Execution incarnation

The guard reads its own incarnation from the OS, never from its own clock. Hand reads the same facts from the OS again to verify them.

| Platform | Boot identity | Guard incarnation | Live(G) as observed by Hand |
| --- | --- | --- | --- |
| Linux | `/proc/sys/kernel/random/boot_id` | boot_id, PID, `/proc/<pid>/stat` field 22 `starttime` | boot_id equal; `starttime` equal; state not `Z`/`X` |
| macOS | `kern.bootsessionuuid` | bootsessionuuid, PID, `proc_pidinfo(PROC_PIDTBSDINFO)` start sec/usec | UUID equal; start time equal; not zombie |
| Windows | none used for equality | PID, `GetProcessTimes` creation FILETIME | `OpenProcess` succeeds; creation time on that handle equal; handle not signaled |

- `kern.boottime` is not a boot identity. It is a wall-clock timestamp, so equality would depend on clock behavior. Wall-clock-derived Windows boot time is not an identity either: a clock step could fabricate a boot change.
- Positive boot-change evidence is:
  - Linux: `boot_id` differs.
  - macOS: `kern.bootsessionuuid` differs.
  - Windows: current `GetTickCount64` is lower than the value the guard recorded at start.
- The Windows witness cannot fabricate a reboot. It can miss one. A missed reboot degrades to `unknown`, never to success.
- Absent(G): same boot, and no process with G's PID has G's start time. Absent(G) proves the guard is gone. It says nothing about T(G).
- PID reuse cannot impersonate G. A reused PID has a different start time. The contract assumes that the OS does not reuse one PID within one start-time resolution unit on one boot. The units are Linux clock ticks, macOS microseconds and Windows 100 ns. Linux and macOS allocate PIDs cyclically, so such reuse requires a full wrap of the PID space inside one unit.
- Hand never sends a signal by PID. Control goes through records in the guard's private directory (see "Interrupt"), so an action can never reach a reused PID.

# Launch

```text
validate      absolute executable; reserved names absent from Harness env
Tx A          external_operation(prepared) + launch_operation(binding_id=B)
              + launch_argument + launch_environment
              + launch_environment(HAND_WORKER_CREDENTIAL, secret-ref,
                                   'hand-exec-guard:v1', V_B)
handoff(L)    O_EXCL, 0600: spec, request digest, launch-spec digest, B, S_B,
              Hand's boot identity
Tx B          external_operation(submitted)                 [#343 boundary]
pane run      existing exact preflight, then type only:
              <absolute hand> exec-guard <handoff locator>
guard         claim -> verify -> scrub env -> chdir -> pin -> pinned
              -> start harness (stopped where verified) -> verify object
              -> running -> resume
Hand          observe records + OS + Herdr -> classify
```

Hand core validates before Tx A:

- The LaunchSpec executable is an absolute path. The guard performs no PATH search.
- The Worker Harness LaunchSpec does not define `HAND_WORKER_CREDENTIAL`. Only Hand core adds it.

A validation failure refuses the request before any operation row exists. A `prepared` L still settles `no-effect` as it does today, and Hand deletes its handoff if one exists.

The terminal and the `herdr pane run` argv carry only the fixed-shape guard invocation and the handoff locator. The harness argv, environment, secret-ref values and S_B never pass through the terminal, the shell history or Herdr. This also removes the current path, where literal environment values are typed into the pane shell.

Guard sequence (every step before `running` is fail-closed):

1. **Claim.** The guard atomically renames handoff(L) to a name that carries G, reads it, then deletes it. Exactly one guard can win a rename. A guard that loses exits without starting a harness. The guard then writes `claimed`. It syncs the rename and its directory to disk before the harness starts, so that a power loss cannot undo a claim whose harness already ran.
2. **Verify.** The protocol version is known. The handoff carries the persisted spec and its resolved values. The guard recomputes the launch-spec digest over the persisted spec and checks each resolved value against its committed value digest. It recomputes V_B from S_B, Fleet ID and B. It records the request digest, the launch-spec digest and V_B for Hand to check against SQLite.
3. **Environment.** The harness environment is the guard's inherited environment with every name removed that `daemonEnvironmentKey` (`internal/herdr/lifecycle.go`) classifies as semantic. The guard then adds the exact resolved spec environment and `HAND_WORKER_CREDENTIAL=S_B`. No inherited Fleet, Attempt, role or credential value survives.
4. **Cwd.** The guard changes directory to the spec cwd. It verifies that the result is the same file as the WorktreeBinding path. A failure refuses before the harness exists.
5. **Pin.** The guard opens the executable, then records file identity and SHA-256 through that same handle in `pinned`. File identity is device and inode, or on Windows the volume serial number and 128-bit file ID.
6. **Start.** The harness starts with default signal dispositions and an empty signal mask. On POSIX it starts in P, and P becomes the foreground group of the pane terminal before the first harness instruction. On macOS the guard calls `tcsetpgrp` itself while the child is suspended. Where the object class needs verification, the child is stopped before its first instruction:
   - macOS: `POSIX_SPAWN_START_SUSPENDED`, or an equivalent stop at exec.
   - Windows: `CREATE_SUSPENDED`, and the child joins the kill-on-close job before it resumes, as `tests/e2e/background_windows_test.go` and `internal/integration/reference_child_windows.go` already do.
   - Linux needs no stop. The `exact` path executes the pinned descriptor, and scripts are `sampled`.
7. **Verify object.** The guard compares the child's image with the pinned object, then writes `running` with R, P, the pane terminal device and the object class. On a mismatch the guard kills the stopped child, reaps it, and records `refused`.
8. **Resume** the stopped child (macOS, Windows).

Hand's classification of L:

| Outcome | Required positive evidence |
| --- | --- |
| `succeeded` | Records `claimed`, `pinned` and `running` for L. Each names V_B, the request digest and the launch-spec digest exactly as committed. G is Live and associated with the SessionBinding pane: Herdr's pane terminal device equals the guard's OS-reported controlling terminal, and Herdr's foreground group equals P. On Windows, Herdr lists R among the pane's processes and R's creation time is verified. Alternatively, T(G) positively ceased: then the binding and its termination are both written. |
| `rejected` | A `refused` record for L. Its guard claimed L and exited before any harness instruction ran; any stopped child was killed and reaped. |
| `no-effect` | Hand fenced handoff(L) on the boot recorded in it: an atomic rename to a tombstone succeeded. No guard can claim L after the fence. `no-effect` additionally needs a qualified Herdr ordering property: once a later exact pane observation completes, the earlier `pane run` has been processed, so no late guard-invocation bytes can reach a harness. Without that property, the fenced Launch stays `uncertain`, with evidence that no execution can exist. |
| `uncertain` | Everything else. Examples: a claim with no `running` record and G absent; `running` present but the pane association is unobservable; an unverifiable incarnation; a fence that succeeded after a boot change relative to the boot recorded in handoff(L). |

- A harness that exits at once after `running` is a real execution, never `no-effect`.
- An early harness can call `drain` before B exists. That call refuses with a typed not-established result, and the caller can manufacture nothing.
- Hand never re-types a guard invocation for a `submitted` or `uncertain` L. Two recovery mutations exist, both under L's own executor-control claim:
  - The fence removes the ability to start.
  - An `abort-request` record for a live G makes the guard terminate T(G). The guard treats it like an interrupt request. The resulting `ceased` settles L through the ceased branch of `succeeded`.
- No B exists while L is `uncertain`, so Interrupt cannot target that execution. The abort request is the only way to stop it. When to abort is recovery policy (#345). Hand never aborts automatically after a single failed observation.
- A Launch prepared without the credential row keeps the revision-1 refusal. Existing `uncertain` rows from before the guard remain `uncertain` until Repair.

# Executable object

Revision 2 accepts file identity plus a content digest recorded by the guard before exec, with a stated verification class:

| Class | Meaning | Where achievable |
| --- | --- | --- |
| `exact` | the kernel executed the object that the guard opened and digested | Linux native ELF started through the pinned descriptor (`execveat` with `AT_EMPTY_PATH`, or `/proc/self/fd/N` as in `internal/integration/reference_child_unix.go`) |
| `verified` | started by path; before the harness runs, a kernel-reported identity of the stopped child's main image equals the pinned object | Windows: the guard holds a handle with `FILE_SHARE_READ` only (deny write and delete) from pinning until the image is verified in the suspended child. macOS native: the kernel's device/inode of the stopped child's main image mapping |
| `sampled` | pinned identity and digest are recorded; the kernel or an interpreter resolves the path again after pinning | interpreter scripts on every platform; any native case where the stopped-image identity cannot be read from the kernel |

- Content digests are sampled at pin time on Linux and macOS. A writer can modify and restore the file between the digest and exec, and the guard cannot detect that. On Windows the deny-write handle freezes the file content from digest until the image is mapped. The loader then denies writes to the mapped image, so the digest describes the loaded main image.
- For scripts, the recorded object is the script file at pin time. The guard also records the interpreter image it observed. On Linux there is no stop step, so that observation happens after the script has started. No claim is made that the interpreter consumed the sampled bytes. Dependencies, dynamic libraries and later `execve` calls by R are outside the claim. An `execve` by R keeps R's incarnation, so the launch receipt describes the initial image only.
- Launch success requires a pinned object record and its class. The class is persisted in the provider executor key. #305 may require `exact` or `verified` for a given harness and platform, and must report `sampled` as a limit.
- Basename, argv or path equality is never object evidence. The "same basename, different directory" counterexample from #346 is excluded: the guard opens exactly the absolute spec path.

# Credential and WorkerInput caller attestation

- The guard gives S_B to the harness only through the environment variable `HAND_WORKER_CREDENTIAL`. `hand runtime worker-input drain|acknowledge` reads S_B from its own environment, never from argv.
- Acceptance predicate, evaluated in one read or writer transaction:
  - Hand recomputes V from the presented S, this DB's Fleet ID and the named B.
  - V equals the `value_digest` of the `HAND_WORKER_CREDENTIAL` row that joins through `executor_binding.launch_operation_id`, compared in constant time.
  - B exists, has no `executor_binding_termination` row, and its key uses the guard grammar.
  - The Attempt is active.
  - The existing input/ack currentness and FK rules hold.
- Refusal cases: credential missing; wrong credential; B not yet established; B terminated; B from another Fleet; Attempt inactive. `HAND_ROLE=worker` stays a routing check, not authentication.
- The guard-run environment is the smaller sound option.
  - An inherited descriptor does not survive ordinary harness tool spawning: Node and Python close extra descriptors by default.
  - A socket that checks peer credentials would rest attestation on PID/ancestry and needs per-OS peer code.
  - The environment reaches exactly T(G), which is the intended caller set.
  - Any process in T(G) can drain or acknowledge for B. That is the claim: the caller belongs to the exact execution, not that the caller is the harness root.
- S_B plaintext exists only in:
  - memory of the preparing Hand process;
  - handoff(L), until the guard claims it or Hand fences and deletes it;
  - the environment and memory of T(G).
  S_B never appears in SQLite, argv, Herdr requests, terminal bytes, records, logs, receipts, error text or structured output. handoff(L) also holds the plaintext of every resolved secret-ref value in the spec. The same confinement therefore applies to those values. A crash can leave the 0600 handoff file, or the guard's renamed claim file, on disk. Reconciliation deletes both when L becomes terminal.
- **Threat model.** The goal is to exclude stale, misrouted and cross-Fleet callers that use the supported protocol. It is not a boundary against a hostile process of the same OS user. That process can read `/proc/<pid>/environ` or the macOS/Windows equivalent. It can also read the handoff and record files, forge records, stop the guard, duplicate the Windows job handle, or write the SQLite DB directly. This revision claims no protection against such a process.

# Positive cessation

**Termination policy.** The execution ends when R exits. The guard then terminates what remains of T(G): a termination request, a bounded grace period, then a forced kill. It records `ceased` only after the platform predicate holds. The `ceased` record carries R's exit status and the cause of cessation, so that Hand can choose the terminal kind. The guard handles SIGTERM, SIGHUP and the Windows console close event by the same path. Pane close and Herdr daemon loss therefore normally produce positive cessation.

Signal handling before R starts: the guard catches and discards SIGINT, SIGQUIT and SIGTSTP. It must not set them to `SIG_IGN`, because an ignored disposition survives exec into the harness. After R starts, the guard ignores SIGTTIN and SIGTTOU in itself; a disposition changed after the child's exec does not reach the child. Ignoring them lets the guard call `tcsetpgrp` and `tcflush` from its background group. With SIGTTOU caught instead, those calls would restart forever. A launcher that forks, exits and leaves its child running is unsupported: its exit ends the execution.

| Platform | Containment | Cessation predicate recorded in `ceased` | Class |
| --- | --- | --- | --- |
| Linux | `PR_SET_CHILD_SUBREAPER` set before the harness starts. Every orphaned descendant reparents to the guard. The guard kills and reaps its unreaped direct children repeatedly; their PIDs cannot be reused before the guard reaps them. | `wait4(-1)` returns `ECHILD` | `tree` |
| Windows | Unnamed job with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, no breakaway flags, one non-inheritable handle held by the guard, as in `cmd/runtime_guardian_windows.go`. Unlike that file, the guard stays outside the job; otherwise `ActiveProcesses` could never reach 0 while the guard lives. `TerminateJobObject` on termination. | job `ActiveProcesses == 0` (as `backgroundJobActiveProcesses` reads it) | `tree` |
| macOS | Process group P only. `killpg(P)` on termination. The guard samples the tree at a bounded interval and records any member it sees outside P. | R reaped, `kill(-P, 0)` returns `ESRCH`, and no escape observed | `process-group` |

- **Linux.** A double fork or `setsid` cannot leave the subreaper. A descendant PID namespace ends with its init, which is itself a descendant.
- **macOS.** An observed escape (a descendant in another group or session) makes cessation `unknown`, never success. An escape that nobody observed (a descendant calls `setsid` or `setpgid`, then its parent exits between samples) is undetectable. This revision accepts `process-group` as the macOS cessation class. It is weaker than `tree`, the class is persisted, and #305 must report it.
- **Outside every platform's claim.** Work that a process in T(G) asks an outside daemon to start: `systemd-run --user`, launchd, WMI, Task Scheduler, services, Docker, an existing tmux or Herdr server, or another `hand`. Positive cessation means T(G) ceased. It does not mean that every effect the harness caused has stopped.
- **Nested Fleets.** Hand detaches its Herdr daemon with `setsid` (`internal/herdr/detach_unix.go`). A Supervisor or Herdr daemon for another Fleet that starts inside T(G) is therefore still part of T(G): it reparents to the Linux subreaper, or stays in the Windows job, and it ends with T(G). Its own guards then die without `ceased` records, so they are `unknown`. Nested Fleets must start outside every guarded execution.
- **Kill-loop convergence.** Descendants that fork faster than the Linux kill loop, or a process stuck in an uninterruptible state, delay `ceased` indefinitely. The execution then stays open; nothing is fabricated.

Hand records `executor_binding_termination` only from one of these:

- a `ceased` record that names L and exactly G, with the platform predicate;
- positive boot-change evidence.

The guard gone with no `ceased` record on the same boot is `unknown`. No termination row is written, B stays open, and the case surfaces for Repair. This holds on Windows as well: kill-on-close makes surviving processes unlikely there, but not proven.

Terminal kind, in order of precedence:

| Kind | Condition |
| --- | --- |
| `interrupted` | an Interrupt for B is `submitted` or `uncertain` when cessation is accepted; `interrupt_operation_id` is set |
| `provider-gone` | pane loss or terminal hangup caused cessation, or a boot change proved it |
| `completed` | R exited with status 0 |
| `failed` | R exited with nonzero status or by signal |

The first writer of the termination row wins through its primary key. Termination permits nothing destructive by itself: Session release and WorktreeRemove keep their own revision-1 proofs.

Durability:

- The claim rename is the one write that must be synced to disk before the harness starts; see "Launch", step 1.
- Hand writes its boot identity into handoff(L). A fence proves "no execution" only on that same boot.
- No other record depends on `fsync`. A `running` or `ceased` record lost to power loss is harmless, because power loss is a boot change, and a boot change is positive cessation of every earlier execution.
- The guard should still sync `running` as soon as it writes it, so that fewer Launches become `uncertain` after a crash.

# Interrupt

```text
Tx A/Tx B      existing interrupt_operation (reason_code) prepared -> submitted
boot changed   -> termination (interrupted) -> succeeded
ceased(G)      -> termination (interrupted) -> succeeded
Live(G)        -> write interrupt-request naming L, G and the operation ID
               -> guard terminates T(G) -> ceased(G) -> termination -> succeeded
Absent(G), no ceased record, same boot -> uncertain
```

- The request is a record in G's directory that names G. It is not a signal. A signal by PID could reach a reused PID on macOS, and Windows has no signals. A request that names G1 is ignored by every other incarnation.
- The guard polls for requests at a bounded interval, set by the implementation to one second or less. The request is idempotent. For the same operation key, a replay writes the same request and cannot cause a second effect.
- A written request, a guard acknowledgement, R's exit or an empty pane inventory is not success. Only an accepted termination is. If the guard never records cessation, the Interrupt stays `submitted` or `uncertain`.
- If the executor terminates naturally before the Interrupt reconciler writes, the Interrupt still `succeeded`, because the executor has ceased. The termination kind keeps what the first writer recorded. Current code cannot take this path: `CompleteCanonicalV19Interrupt` always inserts `interrupted` and requires that no termination row exists. Implementation must add the path.

# WorkerWake

The doorbell stays the revision-1 constant, bounded, Hand-owned doorbell. Its digest stays in `worker_wake_operation.doorbell_digest`. WorkerInput bytes never enter the terminal.

The predicate W(B) must hold both immediately after Tx B, before delivery, and immediately after delivery:

- B is open and its key uses the guard grammar.
- Live(G) and Live(R) hold.
- G has no `ceased` record.
- The Session workspace, tab and pane identities match the SessionBinding exactly.
- POSIX: Herdr's pane terminal device equals G's OS-reported controlling terminal. The terminal's foreground group equals P, as read from the OS (`tpgid` on Linux, `e_tpgid` on macOS) and as reported by Herdr.
- Windows: Herdr lists R among the pane's processes.

| Observation | Outcome |
| --- | --- |
| W fails before delivery | Herdr is not called; this process records `no-effect` |
| W holds before and after | `succeeded`: the mechanism postcondition only, never acknowledgement |
| W holds before and fails after, or the Herdr reply is lost | `uncertain`, with typed residual evidence |

After a crash, a reconciler that cannot tell whether delivery happened records `uncertain`.

- The same live incarnation before and after means neither G nor R died in between.
- Two cases remain possible and are accepted limits. The harness can move the foreground to one of its own groups. An outside SIGSTOP of the guard can return the foreground to the shell and back.
- Windows has no foreground-group fact: whichever attached process reads the console consumes the input.
- The doorbell must be inert when a POSIX shell or PowerShell reads it, because the residual path can deliver it to the pane shell after the guard exits. That means no command substitution, expansion, redirection, pipeline or separator outside quotes. The current doorbell (`internal/store/v19workerwake_herdr_doorbell.go`) contains backticks and violates this. Changing its text changes only the per-request `doorbell_digest` value.
- After cessation and before exiting, the guard flushes pending terminal input. That narrows the shell-residual window; it does not close it.
- An `uncertain` WorkerWake keeps its executor-control claim on B. The anchored `operation_scope_claim_guard` trigger then refuses a residual cleanup or Interrupt for B until Repair settles the wake. Natural or hangup cessation is not an operation, so it still closes B. No guard evidence can prove whether a doorbell arrived after a lost Herdr reply, so this revision defines no automatic settlement.

---

# Persistence without relock

| Fact | Anchored column | Written |
| --- | --- | --- |
| guard protocol marker and credential verifier V_B | `launch_environment(operation_id, name='HAND_WORKER_CREDENTIAL', value_kind='secret-ref', value_material='hand-exec-guard:v1', value_digest=V_B)` | Tx A |
| request / launch-spec commitment covering V_B | `external_operation.request_digest`, `launch_operation.launch_spec_digest` | Tx A |
| guard incarnation G, R, P, pane locator and terminal device, OS, object identity, digest and class | `executor_binding.provider_executor_key`, grammar `herdr-exec-guard:v1?...`; `executor_binding.adapter_ref='herdr'` | Launch success |
| Launch / WorkerWake / Interrupt evidence | `external_operation.state_evidence_digest`, `external_operation_event.evidence_digest` | each transition |
| cessation | `executor_binding_termination(terminal_kind, interrupt_operation_id, observed_at, evidence_digest)` | executor observation or Interrupt writer |
| wake residual | `executor_residual_cleanup_operation.residual_identity_digest` | residual cleanup |
| attested acknowledgement | `worker_input_acknowledgement.evidence_digest` | acknowledge |
| handoff and guard records | Fleet-private files, non-canonical, digests only in SQLite | guard / Hand |

- Every used column accepts the planned values. The text columns carry only length checks, `launch_environment.value_kind` already allows `secret-ref`, and `terminal_kind` already allows all four kinds. The `executor_binding_insert_guard` trigger enforces one open binding per provider key, and the incarnation makes the key unique. No CHECK constrains the key grammar.
- **`adapter_ref` stays `herdr`.** The anchored triggers require one `adapter_ref` along the whole chain: `attempt.session_adapter_ref`, `session_acquire_operation`, `session_binding`, `launch_operation`, `executor_binding`, and the wake, interrupt and residual operations. A new value would therefore also change every new Attempt's Session adapter. It would strand open Sessions that use `herdr`, and it would present the guard as a replaceable provider. Neither is true, so the key grammar version is the only discriminator.
- The legacy grammar `herdr-executor:v1` was never established, because Launch refuses it. Any key that is not in the guard grammar keeps the revision-1 refusals.
- A #344 relock would be required if implementation needs any of these: non-digest evidence rows; a new operation kind (for example a separate guard-start operation); a CHECK on key grammar; or more than one ExecutorBinding per Attempt. This revision needs none of them.

# Adapter documentation (revision-1 common contract, `herdr` with guard)

| Item | Launch | WorkerWake | Interrupt |
| --- | --- | --- | --- |
| first mutation boundary | `herdr pane run` after Tx B | doorbell send after Tx B and passing pre-W | interrupt-request write after Tx B |
| strongest positive postcondition | guard records + Live(G) or ceased(G) + pane association | pre-W and post-W | termination accepted |
| same-key idempotency | no replay; the single claim bounds effects to one execution | no; coalescing-safe | yes |
| destructive identity | G, verified from the OS | — | G, bound by the request record |
| unknown | `uncertain`, no fabricated binding | `uncertain` + residual | `uncertain`, B open |
| extension evidence | provider key grammar + digests | digests | digests |
| secret redaction | S_B confined as stated above | none carried | none carried |

---

# Per-platform strength

| Property | Linux | macOS | Windows |
| --- | --- | --- | --- |
| incarnation | boot_id + PID + starttime: strong | bootsessionuuid + PID + start µs: strong | PID + creation time through a handle: strong |
| boot-change witness | exact | exact | tick decrease; can miss a reboot, safe |
| executable object | `exact` for native; `sampled` for scripts | `verified` for native; `sampled` for scripts | `verified` with frozen content; `sampled` for scripts |
| content digest | sampled | sampled | describes the loaded main image |
| tree containment | subreaper: every descendant | process group only | job without breakaway |
| positive cessation | `tree` (`ECHILD`) | `process-group`; unobserved escape is a false positive | `tree` (`ActiveProcesses == 0`) |
| wake target proof | OS `tpgid` == P on the pane terminal | OS `e_tpgid` == P | Herdr process list + R alive; no foreground fact |
| guard crash | `unknown` | `unknown` | `unknown` |
| outside-daemon effects | not covered | not covered | not covered |

# Invariants

- **EG-1 Commitment before mutation.** Tx A commits V_B and the exact spec. Tx B precedes `herdr pane run`. S_B appears in no location that "Credential" excludes.
- **EG-2 Single claim.** At most one guard starts a harness for handoff(L). A guard claim and a Hand fence exclude each other, and a claim is synced to disk before its harness starts.
- **EG-3 Exact incarnation.** Observation or control for B acts only on the process whose OS-read incarnation equals the one in B's key. Any difference is `absent` or `mismatch`, never the target.
- **EG-4 No Herdr identity authority.** Herdr facts alone never establish, wake, interrupt or terminate B. Herdr daemon PID, ancestry and environment are never inputs to a positive result.
- **EG-5 Launch success.** A binding exists only with valid `claimed`, `pinned` and `running` records that name the committed V_B, request digest and launch-spec digest, plus Live(G) with pane association or accepted cessation.
- **EG-6 Positive no-effect.** `no-effect` for a `submitted` Launch requires a winning fence plus the qualified Herdr ordering property.
- **EG-7 Object honesty.** The recorded object is the object the guard opened, with its true class. No PATH search, basename or argv match counts as object evidence.
- **EG-8 Positive cessation.** A termination row requires a `ceased` record for exactly G with the platform predicate, or a positive boot change. Absent(G) without a record on the same boot writes no row.
- **EG-9 Interrupt success.** `succeeded` requires an accepted termination for B. Request acceptance is never success.
- **EG-10 Wake success.** `succeeded` requires W(B) both before and after delivery. A failed pre-W means Herdr is never called.
- **EG-11 Attestation.** Drain or acknowledge is accepted if and only if the presented secret hashes, for this Fleet and B, to the V_B stored for B, and B is open and current.
- **EG-12 Environment neutrality.** No `daemonEnvironmentKey` value inherited from the daemon, pane or shell reaches the harness. Only the exact spec values and S_B do.
- **EG-13 Generation isolation.** No record, request, credential or key derived from G1 or B1 changes any state of G2 or B2.
- **EG-14 Terminal inertness.** The terminal carries only the fixed-shape guard invocation (Hand path, `exec-guard`, handoff locator) and the constant shell-inert doorbell.
- **EG-15 No guard writes.** The guard never opens the canonical SQLite DB.

# Counterexamples and the point that excludes each

1. **A stale G1 guard acts on G2.** G1 knows only L1 and writes only records that name G1. Hand checks every record's incarnation against B2's key and ignores G1's records (EG-3, EG-13). An interrupt request for G2 names G2, and G1 ignores it.
2. **PID reuse impersonates G.** G dies and an unrelated process gets its PID. Its start time differs, so Absent(G) holds. With no `ceased` record the result is `unknown`. No request can reach the new process, because control never uses signals by PID (EG-3).
3. **The guard crashes.** SIGKILL hits G after `running`. Orphans reparent to an outer subreaper or init. Hand sees Absent(G), no `ceased` record and the same boot: `unknown`, no termination, the Interrupt stays `uncertain` (EG-8). A later reboot supplies positive cessation.
4. **A wake reaches a replaced pane process.** R exits, the guard records `ceased` and exits, and the shell regains the foreground. Pre-W fails, so there is no send and the result is `no-effect`. If the replacement happens between pre-W and post-W, post-W fails: `uncertain` plus residual, and the shell-inert doorbell makes the residual harmless (EG-10, EG-14).
5. **A stale caller secret.** A process holding S_B1 calls drain for B2. The hash differs from V_B2: refused. The same process calls drain for B1 after B1 terminated: refused, because positive cessation means that no process of T(G1) remains (EG-11).
6. **Cross-Fleet environment leak.** A Fleet-B Herdr daemon started inside a Fleet-A worker inherits `HAND_HOME=A` and `HAND_WORKER_CREDENTIAL=S_A`. The Fleet-B guard scrubs both before starting the harness (EG-12). A Fleet-A harness that runs `hand` against Fleet B presents S_A: V is recomputed with Fleet B's ID and differs, so the call is refused (EG-11).
7. **Two guards for one handoff.** The pane shell runs the typed command twice. The second rename fails, so that guard starts no harness (EG-2).
8. **Path swap between pin and exec.** Linux native execution uses the pinned descriptor. macOS and Windows verify the stopped child against the pinned object and refuse on a mismatch before the harness runs (EG-7). Scripts record `sampled`.
9. **Crash after Tx B, before `pane run`.** The reconciler fences handoff(L). With the ordering property the result is `no-effect`; without it, `uncertain` with no execution possible. There is never a second typed invocation (EG-6).
10. **The Interrupt is accepted but the tree survives.** The guard is stopped or a descendant is stuck. No `ceased` record appears, so the Interrupt stays `submitted` or `uncertain` (EG-9).
11. **A `setsid` grandchild.** Linux: it reparents to the guard, and `ECHILD` waits for it. Windows: it stays in the job. macOS: observed means `unknown`; unobserved is the documented false positive.
12. **Fast exit.** R exits before Hand observes it. `running` and `ceased` exist, so Hand writes `succeeded` plus the binding plus the termination. The result is never `no-effect`.
13. **Power loss.** Records are lost or torn and the boot changes. Every earlier T(G) positively ceased. The synced claim survives, so a later fence fails. A fence on a later boot is not no-execution evidence anyway. A Launch that lost its `running` record stays `uncertain`, with positive cessation available for Repair.

# Known limits this revision does not close

- macOS cessation is process-group only. An unobserved escape is a false positive.
- Effects started through outside daemons are outside every platform's claim.
- There is no protection against a hostile process of the same user.
- Content digests are sampled on Linux and macOS. Scripts are `sampled` everywhere.
- The wake predicate has accepted gaps: harness-internal foreground changes, an outside SIGSTOP of the guard, and no foreground fact on Windows.
- A guard crash leaves B open until a boot change or operator Repair.
- A crash can leave the 0600 handoff file or the claim file on disk until reconciliation deletes it. Either file holds S_B and every resolved secret-ref plaintext.
- An `uncertain` WorkerWake blocks Interrupt and residual cleanup for B until Repair.
- An `uncertain` Launch with a live harness can be stopped only by an abort request, because no B exists.
- A Fleet started inside a guarded execution ends with that execution.
- A harness launcher that forks, exits and leaves its child running is unsupported.
- `uncertain` Launch rows from before the guard stay `uncertain` until Repair.
- The Herdr 0.8.2 behaviors must be measured: pane close (SIGHUP versus SIGKILL to the guard), daemon restart, and `pane run` ordering. A SIGKILL of the guard degrades cessation to `unknown`.
- Linux kill-loop convergence is not guaranteed against a descendant that forks quickly. cgroup freezing is a possible later strengthening; it is not required.

# Acceptance tests implementation must add

Each test cites the invariant it checks. Native process tests run real processes on Linux, macOS and Windows. Fakes may cover SQLite-only transitions. The #346 tracker list stays required: same-basename foreign executable, PID reuse, stale executor generation, provider restart, unknown process inventory, wrapped/background execution, late historical acknowledgement without successor retargeting, zero mutation after failed re-proof, structured argv/env/cwd behavior, and refusal before execution when changing the cwd fails.

Guard primitive (per OS):

- The incarnation the guard records equals Hand's OS re-read. A synthetic record with the same PID and a different start time is `absent` (EG-3).
- Two guards race one handoff: exactly one harness starts. A fence racing a claim: exactly one wins. A simulated loss of unsynced writes cannot undo a claim whose harness started (EG-2).
- A cwd change failure refuses with no harness process ever created (EG-7).
- Path replacement between pin and start: Linux runs the pinned object; macOS and Windows refuse before the first harness instruction. A symlink retarget and the same basename in another directory both fail (EG-7).
- A script records `sampled` plus the interpreter identity (EG-7).
- The harness environment contains the exact spec plus S_B and no inherited semantic key. S_B is absent from `/proc/<pid>/cmdline` (or the platform equivalent), from records and from output (EG-1, EG-12).
- Linux: a double-fork plus `setsid` grandchild delays `ceased` until reaped. A descendant PID namespace is covered where unprivileged user namespaces exist (EG-8).
- Windows: a grandchild stays in the job; breakaway is refused; `ceased` only after `ActiveProcesses == 0` (EG-8).
- macOS: an observed `setsid` escape yields `unknown` (EG-8).
- A SIGKILLed guard leaves no `ceased` record (EG-8).
- An immediate harness exit produces `running` and `ceased` (EG-5).
- Terminal hangup produces `ceased` (EG-8).
- An ignored-signal disposition does not leak into the harness. From its background group, the guard's `tcsetpgrp` and `tcflush` calls complete.
- A Windows guard stays outside its own job.
- An `abort-request` terminates a live T(G) and yields `ceased`.

Hand Launch reconciler:

- Crash at each boundary (after Tx A, after the handoff write, after Tx B, after `pane run`, after `claimed`, after `running`) classifies as the Launch table requires, with no second invocation (EG-1, EG-5, EG-6).
- A fence after a boot change is not `no-effect` (EG-6).
- An `uncertain` Launch with a live G settles through `abort-request`, then `ceased`, then `succeeded` with a terminated B (EG-5, EG-8).
- A record naming a different V_B, request digest or launch-spec digest never succeeds (EG-5).
- The key grammar round-trips, and the open-key uniqueness trigger holds.
- A Launch without the credential row keeps the refusal.

Interrupt:

- Success only after `ceased`. A stalled guard keeps the Interrupt `submitted` (EG-9).
- A boot change succeeds; a guard crash stays `uncertain` (EG-8).
- A G1 request is ignored by G2 (EG-13).
- A natural-exit race against Interrupt writes one termination row, and the Interrupt succeeds.
- An `uncertain` WorkerWake refuses a new Interrupt, and natural cessation still closes B.

WorkerWake:

- A pre-W failure makes no Herdr call. A replaced pane process or R exit between pre-W and post-W gives `uncertain` plus residual (EG-10).
- The doorbell is constant and bounded, and the digest matches. POSIX sh and PowerShell parse it as inert (EG-14).

WorkerInput protocol:

- A valid credential drains in ordinal order (EG-11).
- Refused cases: missing credential; wrong credential; S_B1 against B2; S_B1 after B1 terminated; another Fleet; B not yet established. The credential is taken only from the environment, and the comparison is constant-time (EG-11).

Real provider (Herdr 0.8.2, stub harness, real guard):

- Launch, wake, drain, acknowledge, Interrupt.
- Pane close; daemon restart.
- `pane run` ordering qualification.
- Two Fleets on one host with a contaminated daemon environment (EG-4, EG-6, EG-12).
- A Fleet started inside T(G) ends with it, and its guards become `unknown`.

# Lock conditions for revision 2

- [ ] Every "Retained revision-1 contract" section still holds without edits to revision-1 bytes.
- [ ] Guard records corroborated by OS facts that Hand reads are the only execution identity authority. Herdr remains addressability and doorbell transport.
- [ ] Credential, handoff and terminal confinement is explicit, and the threat model excludes hostile same-user processes.
- [ ] Per-platform incarnation, object, cessation and wake classes are explicit and persisted.
- [ ] Guard crash is `unknown`. Only a `ceased` record or a boot change terminates B.
- [ ] DDL impact NONE is verified against `docs/architecture/v19-v6.sql.gz`, with `adapter_ref='herdr'` and a versioned key grammar.
- [ ] Acceptance tests above are added before any refusal is removed.
- [ ] A reviewed manifest anchors this blob before dependent implementation.
