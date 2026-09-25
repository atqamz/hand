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

This candidate supersedes the revision-1 #346 snapshot when an independently reviewed permanent manifest anchors this exact Git blob. Revision-1 bytes stay immutable historical evidence. Until the anchor lands, revision 1 governs, and the current fail-closed refusals of Herdr Launch, WorkerWake, Interrupt and WorkerInput drain/acknowledge stay required. The GitHub issue is a tracker; its comments are not normative architecture state.

Revision 2 changes one thing: how the `herdr` adapter proves execution identity, the executable object, positive cessation and caller attestation. Hand supplies those proofs with a Hand-owned execution guard. Herdr keeps the Session, terminal, addressability and doorbell transport. Every other revision-1 rule still applies. Where a section below is stricter than revision 1, the stricter rule governs.

DDL impact: **NONE**. "Persistence without relock" maps every fact to a column of the anchored `docs/architecture/v19-v6.sql.gz`.

## Decision provenance

- Operator decision, 2026-09-25. Canonical Launch, WorkerWake, Interrupt and WorkerInput drain/acknowledge refuse today because managed Herdr 0.8.2 cannot supply the proof each one needs. The exact refusal texts are:
  - Launch: "exact executable-object and never-reused execution-incarnation identity" (`internal/store/v19launch_herdr.go`).
  - Interrupt: "exact execution identity and positive cessation evidence" (`internal/store/v19interrupt_herdr.go`).
  - WorkerWake: "exact live execution identity" (`internal/store/v19workerwake_herdr.go`).
  - WorkerInput protocol: "exact caller-to-ExecutorBinding attestation" (`internal/store/v19launch_herdr_capability.go`, `cmd/worker_input.go`).
- Herdr Discussion #4568 has no answer. Hand does not wait for it and does not fork Herdr.
- `docs/architecture/346-provider-execution-authority-proposal.md` stays a non-normative draft. That draft put execution authority at the provider's process-creation boundary and advised against a second process supervisor inside Hand. This revision makes the opposite placement decision for the `herdr` adapter. It keeps the draft's distinctions: root cessation versus tree cessation, a sampled digest versus mapped bytes, the stale-caller threat model, and no replay after `submitted`.

## Platform enablement

The guard capability is enabled per platform. Each platform needs its own native qualification.

| Platform | 0.8.0 | Reason |
| --- | --- | --- |
| Linux | enabled after qualification | subreaper gives tree containment |
| Windows | enabled after qualification | a job object gives tree containment; wake needs the console association described in "WorkerWake" |
| macOS | revision-1 refusals kept | no tree containment; process-group cessation would allow a false success. `internal/toolchain/runtime.lock.json` already lists darwin as unsupported |

A later revision may enable macOS with its own proof. No macOS test gates Linux or Windows.

## Retained revision-1 contract

These revision-1 sections stay normative without change:

- canonical vocabulary;
- adapter identity and the common contract;
- WorkerInput capability boundary;
- Native Git Worktree capability;
- Worker Harness;
- Supervisor Harness;
- Decision / Answer delivery;
- Worker routing / configuration;
- provider extension rule;
- replacement / substitutability tests;
- lock conditions.

Two sections stay normative with the additions stated below:

- WorkerWake adapter: live-identity preconditions and the doorbell grammar.
- Session / Herdr environment boundary: the guard scrubs the environment, and Session release runs a guarded-execution pre-check ("Session release with guarded executions"). The rule "Daemon PID/ancestry/environment is observation, never identity authority" holds without exception.

This revision restates the "Launch / Executor / Interrupt" section for the `herdr` adapter. Its revision-1 rules stay as lower bounds:

- #343 submission precedes the first provider start mutation.
- Launch success requires positive, exact ExecutorBinding establishment.
- An ambiguous launch fabricates nothing.
- Interrupt success means positive cessation, not request acceptance.
- WorkerWake, Interrupt and residual cleanup share executor-control exclusion.

The one non-observational success path is the "Operator-attested settlement" section. It is scoped #345 `operator-attested` authority, and it is recorded as attestation, never as observation.

---

# Responsibility split

```text
Herdr (adapter_ref = herdr)
  Session container, workspace/tab/pane, pane terminal
  pane addressability: pane -> terminal device, foreground group, shell PID, agent status
  types one fixed-shape guard invocation into the pane shell
  transports the constant Hand-owned doorbell

hand exec-guard (inside adapter_ref = herdr; not an adapter)
  claims one Launch handoff, pins the harness executable, starts the harness
  contains the execution tree, terminates it, observes positive cessation
  writes typed guard records (provider observations)
  never opens SQLite, never decides currentness, never writes canonical rows

Hand core
  commits the request and credential verifier before the first mutation
  reads the OS facts it can read, to check every guard record
  performs every canonical transition under #343/#345
```

The guard is a Hand mechanism, not a replaceable provider, so it has no `adapter_ref` of its own.

Hand reads these facts from the OS itself: boot identity, incarnation, liveness, controlling terminal, foreground group, PID namespace, user, and (on Windows) process creation times. A record is rejected when it conflicts with any of them.

Hand cannot observe the other record facts itself: object identity and digest, verifier echo, exit status, and the cessation predicate. They are claims by a Hand-owned mechanism, and they are trusted only under the threat model in "Credential".

Identity authority comes only from guard records that agree with these OS facts. It never comes from:

- Herdr daemon PID, ancestry or environment;
- a bare PID, path, argv, basename or label;
- pane text.

Herdr's pane reports are addressability locators. Association checks require them, but they are never sufficient.

Host precondition: Hand, the Herdr panes and the guard share one kernel boot, one PID namespace and one OS user. Hand writes its own PID-namespace identity and user into handoff(L). On Linux these are the `/proc/self/ns/pid` inode and the uid; on Windows, the user SID. The guard refuses when its own values differ. Hand rejects a record whose values differ from its own.

# Subjects

| Subject | Meaning |
| --- | --- |
| L | one exact `launch` external operation; `launch_operation.binding_id` pre-allocates B |
| B | the ExecutorBinding ID that L establishes on success |
| S_B | 32 bytes from a CSPRNG, generated by Hand core in the process that prepares L; carried as unpadded base64url |
| V_B | lowercase hex SHA-256 over the canonical digest fields (`writeCanonicalV19DigestField` framing) `domain=hand:v19:exec-guard-credential:v1`, `fleet_id`, `executor_binding_id=B`, `credential=S_B` (encoded) |
| handoff(L) | Fleet-private file with the persisted spec and its resolved values, request digest, launch-spec digest, B, S_B, Hand's boot identity, PID namespace and user |
| G | guard incarnation: (boot identity, guard PID, guard OS start time) |
| R | harness root incarnation, recorded by the guard as its direct child |
| P | harness process group (Linux), created by the guard as the pane terminal's foreground group |
| T(G) | execution tree: on Linux, all descendants of G; on Windows, every process in G's job |
| records(L) | guard records `claimed`, `pinned`, `running`, `console` (Windows), `refused`, `ceased`, and Hand's `interrupt-request`; each names L and G |

Records and the handoff live in a Fleet-private directory under the Fleet home. The directory has mode 0700; on Windows it inherits the user-private ACL of the Fleet home. The implementation owns the exact path and encoding. Records are written as a temp file and then atomically renamed.

A torn or unparsable record counts as absent.

A record with a protocol version Hand does not know is refused as a typed error. It does not count as absent. Every Hand build keeps a reader for each guard protocol version that appears in the key of any open ExecutorBinding. Generations are user-global (#519), so the check reaches beyond one Fleet. Before an upgrade replaces the binary, it scans the open ExecutorBinding keys of every Fleet in the user-global registry, and the live guard leases. It refuses if the new build would drop a reader that any of them needs. A Fleet it cannot read also refuses the upgrade.

The guard holds a managed Hand generation lease for the generation it executes from, per #519, through `toolchain.Store.AcquireHandLease`, as the supervision waiter does (`internal/supervision/wait.go`, `acquireWaiterGenerationLeases`). It holds the lease until it exits. A guard run from an unmanaged checkout build has no generation to lease.

---

# Execution incarnation

The guard reads its own incarnation from the OS, never from its own clock. Hand reads the same facts from the OS again to verify them.

| Platform | Guard incarnation | Live(G) as observed by Hand |
| --- | --- | --- |
| Linux | `/proc/sys/kernel/random/boot_id`, PID, `/proc/<pid>/stat` field 22 `starttime` | boot_id equal; `starttime` equal; state not `Z`/`X` |
| Windows | PID, `GetProcessTimes` creation FILETIME | `OpenProcess` succeeds; the creation time read through that handle is equal; the handle is not signaled |

A boot witness must change on every event that ends user processes. It must never change on an event that preserves them, such as sleep or resume from hibernation. Positive boot-change evidence:

- **Linux.** `boot_id` differs.
- **Windows.** The current `GetTickCount64` is lower than the value the guard recorded at start. This witness cannot fabricate a reboot. It misses a Fast Startup shutdown, which ends every user process without resetting the tick count.
- **Windows, candidate.** `KUSER_SHARED_DATA.BootId` is documented as "incremented for each boot attempt by the OS loader". It may serve as a witness only after native qualification proves three things: it changes across a full restart, it changes across a Fast Startup shutdown and power-on, and it does not change across sleep or hibernation resume. Until then a Windows guard crash stays `unknown` until operator attestation or a full restart.

Wall-clock-derived boot time is never a witness, because a clock step could fabricate a boot change.

Absent(G): same boot, and no process with G's PID has G's start time. Absent(G) proves that the guard is gone. It says nothing about T(G).

PID reuse cannot impersonate G, because a reused PID has a different start time. The contract assumes that the OS does not reuse one PID within one start-time resolution unit (a Linux clock tick, or 100 ns on Windows) on one boot. Linux allocates PIDs cyclically, so such a reuse would need a full wrap of the PID space within one tick.

Hand never sends a signal by PID. All control goes through records in the guard's private directory ("Interrupt"), so an action can never reach a reused PID.

# Launch

```text
validate      absolute executable; reserved names absent from the Harness env
Tx A          external_operation(prepared) + launch_operation(binding_id=B)
              + launch_argument + launch_environment
              + launch_environment(HAND_WORKER_EXECUTOR_BINDING, literal, B)
              + launch_environment(HAND_WORKER_CREDENTIAL, secret-ref,
                                   'hand-exec-guard:v1', V_B)
handoff(L)    O_EXCL, 0600
Tx B          external_operation(submitted)                 [#343 boundary]
pane run      existing exact preflight, then type only:
              <absolute hand> exec-guard <handoff locator>
guard         claim -> check -> scrub env -> chdir -> pin -> pinned
              -> start (suspended on Windows) -> verify -> running -> resume
Hand          observe records + OS + Herdr -> classify
```

Hand core validates before Tx A:

- The LaunchSpec executable is an absolute path. The guard performs no PATH search.
- The Worker Harness LaunchSpec defines neither `HAND_WORKER_EXECUTOR_BINDING` nor `HAND_WORKER_CREDENTIAL`. Only Hand core adds them.

A validation failure refuses before any operation row exists.

The terminal and the `herdr pane run` argv carry only the fixed-shape guard invocation and the handoff locator. The harness argv, its environment, secret-ref values and S_B never pass through the terminal, the shell history or Herdr. This also removes the current practice of typing literal environment values into the pane shell.

Guard sequence. Every step before `running` fails closed.

1. **Claim.** The guard atomically renames handoff(L) to a name that carries G. Exactly one guard can win that rename; a guard that loses exits without starting a harness. The guard writes `claimed`. It syncs the rename and its directory to disk before the harness starts, so that power loss cannot undo a claim whose harness already ran.
2. **Check.** Every item below must hold. If any fails, the guard records `refused` before any harness exists:
   - The protocol version is known.
   - The handoff's boot identity equals the current boot. This defeats a guard invocation recalled from shell history after a reboot.
   - The handoff's PID namespace and user equal the guard's own.
   - The recomputed launch-spec digest over the persisted spec equals the handoff's value. That digest covers each environment entry's kind, material and value digest (`canonicalV19LaunchSpecDigest`).
   - Each resolved value matches its committed value digest.
   - V_B recomputed from S_B, the Fleet ID and B matches.

   The guard records the request digest, the launch-spec digest and V_B so that Hand can check them against SQLite.
3. **Environment.** The guard starts from its inherited environment and removes every name that `daemonEnvironmentKey` (`internal/herdr/lifecycle.go`) classifies as semantic. It then adds the exact resolved spec environment, which contains `HAND_WORKER_EXECUTOR_BINDING` and `HAND_WORKER_CREDENTIAL`. No inherited Fleet, Attempt, role or credential value survives.
4. **Cwd.** The guard changes directory to the spec cwd and verifies that the result is the same file as the WorktreeBinding path. On failure it refuses before the harness exists.
5. **Pin.** The guard opens the executable, then records its file identity and SHA-256 in `pinned`, reading both through that same handle. File identity is device and inode on Linux, and volume serial number plus 128-bit file ID on Windows.
6. **Start.** The harness starts with default signal dispositions and an empty signal mask.
   - Linux: R starts in P, and P becomes the foreground group of the pane terminal before exec.
   - Windows: R starts with `CREATE_SUSPENDED` and joins the kill-on-close job before it resumes, as `tests/e2e/background_windows_test.go` and `internal/integration/reference_child_windows.go` already do.
7. **Verify.**
   - Windows: the guard compares the suspended child's image with the pinned object. It then records `GetConsoleProcessList` together with each listed process's creation time in `console`.
   - The guard writes `running` with R, P (Linux), the controlling terminal device (Linux) and the object class.
   - On a mismatch the guard kills the child, reaps it, and records `refused`.
8. **Resume** (Windows only).

Pane association, A(B), is a WorkerWake precondition only (W(B) in "WorkerWake"). Launch success never requires it on any platform, and Launch success never implies that a wake can be delivered.

ExecutorBinding identity is the guard incarnation G plus the harness root R. It is proven by the guard claiming the exact handoff of L, and L already names the SessionBinding and pane that Hand targeted. Interrupt and cessation act on the guard directly, not through the pane, so they do not need A(B). An established B can therefore always be interrupted, with no abort operation and no relock. Executor-control exclusion still applies: an `uncertain` wake blocks Interrupt until that wake settles.

The preflight and `pane run` are separate Herdr calls (`internal/herdr/exact_launch.go`). If Herdr reuses a pane ID between them, the guard can land in another Session's pane. For that reason the key records whether Launch observed A(B) (`assoc=observed|unobserved|mismatch`). Session release also re-checks where every live guard actually sits ("Session release with guarded executions").

A(B) is:

- **Linux.** Herdr's pane terminal device equals G's controlling terminal as read from the OS. The terminal's foreground group equals P, both as read from the OS (`tpgid`) and as reported by Herdr.
- **Windows.** Herdr reports a shell PID for the pane. Hand reads that process's creation time from the OS. The guard's latest `console` record lists that PID with that creation time, and also lists R. A PPID walk is ancestry and is never used.

Hand's classification of L:

| Outcome | Required positive evidence |
| --- | --- |
| `succeeded` | Records `claimed`, `pinned` and `running` for L, naming the committed V_B, request digest and launch-spec digest. In addition, one of: (a) Live(G); (b) accepted cessation of T(G), in which case the binding and its termination are written together. |
| `rejected` | A `refused` record for L. Its guard claimed L and exited before any harness instruction ran; any child was killed and reaped. |
| `no-effect` | Hand fenced handoff(L) on the boot recorded in it: an atomic rename to a tombstone succeeded, so no guard can claim L after the fence. `no-effect` also needs the qualified Herdr property O1 ("WorkerWake"), so that no late guard-invocation bytes can reach a harness. Without O1 the fenced Launch stays `uncertain`, with evidence that no execution can exist. |
| `uncertain` | Everything else. Examples: a claim with no `running` record and G absent; an unverifiable incarnation; a fence after a boot change. |

- A harness that exits immediately after `running` is a real execution, never `no-effect`.
- An early harness can call `drain` before B exists. That call refuses with a typed not-established result, and the caller can manufacture nothing.
- Hand never types a second guard invocation for a `submitted` or `uncertain` L. The fence is the only recovery mutation, and it only removes the ability to start.
- A live guard with valid records always yields `succeeded`, so a running harness always has a B that Interrupt can target.
- `uncertain` with a live harness remains possible only in a narrow window. On Linux, R can start before the guard writes `running`. A guard that stops at that point (for example, from an outside SIGSTOP) leaves no B. When the guard resumes it writes `running`, or its hangup path writes `ceased`. A guard that disappears without either goes to "Operator-attested settlement".
- The handoff is deleted in this order: fence, settle L, then delete the tombstone or leftover claim file. A `prepared` L follows the same order, then settles `no-effect` as it does today.
- A Launch prepared without the credential row keeps the revision-1 refusal. `uncertain` rows from before the guard use "Operator-attested settlement".

# Executable object

This revision accepts file identity plus a content digest, both recorded by the guard before exec, together with a stated verification class:

| Class | Meaning | Where achievable |
| --- | --- | --- |
| `exact` | the kernel executed the object the guard opened and digested | Linux native ELF started through the pinned descriptor (`execveat` with `AT_EMPTY_PATH`, or `/proc/self/fd/N` as in `internal/integration/reference_child_unix.go`) |
| `verified` | started by path; a kernel-reported identity of the suspended child's main image equals the pinned object before the harness runs | Windows: the guard holds a `FILE_SHARE_READ`-only handle (deny write and delete) from pinning until the image is verified |
| `sampled` | pinned identity and digest are recorded; the kernel or an interpreter resolves the path again after pinning | interpreter scripts on every platform; any native case where the suspended image identity cannot be read |

- On Linux, content digests are sampled at pin time. A writer can modify and restore the file between the digest and exec, and nothing detects that. On Windows, the deny-write handle freezes the content from the digest until the image is mapped. The loader then denies writes to the mapped image, so the digest describes the loaded main image.
- For a script, the recorded object is the script file at pin time. The guard also records the interpreter image it observed; on Linux that observation happens after the script starts. No claim is made that the interpreter consumed the sampled bytes. Dependencies, dynamic libraries and later `execve` calls by R are outside the claim. An `execve` by R keeps R's incarnation, so the launch receipt describes only the initial image.
- Launch success requires a pinned object record and its class. The class is persisted in the provider executor key. #305 may require `exact` or `verified` for a given harness and platform, and must report `sampled` as a limit.
- Basename, argv or path equality is never object evidence. The same-basename-in-another-directory counterexample from #346 is excluded, because the guard opens exactly the absolute spec path.

# Credential and WorkerInput caller attestation

The guard gives the harness B through the literal environment variable `HAND_WORKER_EXECUTOR_BINDING`, and S_B through `HAND_WORKER_CREDENTIAL`.

- `hand runtime worker-input drain` and `hand runtime worker-input acknowledge <worker-input-id>` read B and S from their own environment, never from argv.
- If the caller also passes Attempt or ExecutorBinding IDs as arguments, they must equal B's.

Acceptance predicate, evaluated in one read or writer transaction:

- Hand recomputes V from the presented S, this DB's Fleet ID and B.
- V equals, in constant time, the `value_digest` of the `HAND_WORKER_CREDENTIAL` row reached through `executor_binding.launch_operation_id`.
- B exists, has no `executor_binding_termination` row, and its key uses a guard grammar.
- The Attempt is active.
- The existing input/ack currentness and FK rules hold.

Refusal cases: credential missing; wrong credential; B not yet established; B terminated; B from another Fleet; Attempt inactive. `HAND_ROLE=worker` stays a routing check, not authentication.

The guard-run environment is the smaller sound option:

- An inherited descriptor does not survive ordinary harness tool spawning, because Node and Python close extra descriptors by default.
- A socket that checks peer credentials would rest attestation on PID and ancestry, and it needs per-OS peer code.
- The environment reaches exactly T(G), which is the intended caller set.

Any process in T(G) can drain or acknowledge for B. The claim is that the caller belongs to the exact execution, not that the caller is the harness root.

S_B plaintext exists only in:

- memory of the preparing Hand process;
- handoff(L), until the guard claims it or Hand fences it;
- a claim file left behind by a crashed guard;
- the environment and memory of T(G).

S_B never appears in SQLite, argv, Herdr requests, terminal bytes, other records, logs, receipts, error text or structured output. handoff(L) also holds the plaintext of every resolved secret-ref value, and the same confinement applies to those values.

**Threat model.** The goal is to exclude stale, misrouted and cross-Fleet callers that use the supported protocol. It is not a boundary against a hostile process of the same OS user. Such a process can:

- read `/proc/<pid>/environ` or the Windows equivalent;
- read the handoff, claim and record files, and forge records;
- write an `interrupt-request`;
- stop the guard;
- duplicate the Windows job handle;
- write the SQLite DB directly.

This revision claims no protection against such a process. "Positive cessation" bounds the damage a forged interrupt request can do.

# Positive cessation

**Termination policy.** The execution ends when R exits. The guard then terminates what remains of T(G): a termination request, a bounded grace period, then a forced kill. It records `ceased` only after the platform predicate holds.

`ceased` carries R's exit status and a cause, which is one of:

- `harness-exit`;
- `interrupt-request`, naming the Interrupt operation ID;
- `hangup`;
- `external-termination`.

The guard handles SIGTERM, SIGHUP and the Windows console close event on the same path. Pane close and Herdr daemon loss therefore normally produce positive cessation. Hand never closes a pane while B is open, because the anchored trigger refuses Session release while an ExecutorBinding is open.

**Signals (Linux).**

- Before R starts, the guard catches and discards SIGINT, SIGQUIT and SIGTSTP. It must not set them to `SIG_IGN`, because an ignored disposition survives exec into the harness.
- After R starts, the guard ignores SIGTTIN and SIGTTOU in itself. A disposition changed after the child's exec does not reach the child. Ignoring these two lets `tcsetpgrp` and `tcflush` complete from the guard's background group. With SIGTTOU caught instead, those calls would restart forever.

A launcher that forks, exits and leaves its child running is unsupported: the launcher's exit ends the execution.

| Platform | Containment | Cessation predicate recorded in `ceased` |
| --- | --- | --- |
| Linux | `PR_SET_CHILD_SUBREAPER` is set before the harness starts, so every orphaned descendant reparents to the guard. | `wait4(-1)` returns `ECHILD` |
| Windows | An unnamed job with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`, no breakaway flags, and one non-inheritable handle held by the guard (as in `cmd/runtime_guardian_windows.go`). Unlike that file, the guard stays outside the job, because otherwise `ActiveProcesses` could never reach zero while the guard lives. Termination uses `TerminateJobObject`. | job `ActiveProcesses == 0` (as `backgroundJobActiveProcesses` reads it) |

**Linux termination sweep.**

1. Walk the descendants of G through `/proc/<pid>/task/<tid>/children`.
2. For each descendant, open a pidfd, check its `starttime`, and send SIGSTOP with `pidfd_send_signal`.
3. Repeat until one full sweep finds no descendant that is not stopped. A stopped process cannot fork, so the sweep converges.
4. Send SIGKILL to every descendant the same way, then reap until `ECHILD`.

A double fork or `setsid` cannot leave the subreaper. A descendant PID namespace ends with its own init, which is itself a descendant. A kernel without `CONFIG_PROC_CHILDREN` falls back to repeatedly killing direct children; a descendant that forks fast can then delay `ceased` indefinitely.

**Outside every platform's claim.** Work that a process in T(G) asks an outside daemon to start: `systemd-run --user`, WMI, Task Scheduler, services, the Secondary Logon service, Docker, an existing tmux or Herdr server, or another `hand`. Positive cessation means that T(G) ceased. It does not mean that every effect the harness caused has stopped.

**Nested Fleets.** Hand detaches its Herdr daemon with `setsid` (`internal/herdr/detach_unix.go`). A Supervisor or Herdr daemon for another Fleet that starts inside T(G) therefore remains part of T(G): it reparents to the subreaper, or stays in the job, and it ends with T(G). That Fleet's own guards then die without `ceased` and become `unknown`. Nested Fleets must start outside every guarded execution.

Hand records `executor_binding_termination` only from one of these:

- a `ceased` record that names L and exactly G, with the platform predicate;
- positive boot-change evidence;
- operator-attested settlement.

If the guard is gone with no `ceased` record on the same boot, the state is `unknown`. No termination row is written and B stays open until a boot change or operator-attested settlement. This also holds on Windows: kill-on-close makes surviving processes unlikely there, but not proven.

Terminal kind, chosen by cause:

| Kind | Condition |
| --- | --- |
| `interrupted` | the cause is `interrupt-request`, and it names the exact `submitted` or `uncertain` Interrupt for B; `interrupt_operation_id` is set to it |
| `provider-gone` | the cause is `hangup`; or the evidence is a boot change or operator attestation, which gives no exit status |
| `completed` | the cause is `harness-exit` and R exited with status 0 |
| `failed` | the cause is `harness-exit` with a nonzero status or a signal; or `external-termination`; or an `interrupt-request` that names no exact pending Interrupt for B |

A forged interrupt request therefore ends at most in `failed` or `completed`, never in a false `interrupted`. The first writer of the termination row wins through its primary key. Termination permits nothing destructive by itself: Session release and WorktreeRemove keep their own revision-1 proofs.

Durability:

- The claim rename is the one write that must reach disk before the harness starts ("Launch", step 1).
- A fence proves "no execution" only on the boot recorded in handoff(L), and the guard refuses a handoff from another boot.
- No other record depends on `fsync`. A `running` or `ceased` record lost to power loss is harmless, because power loss is a boot change, and a boot change is positive cessation of every earlier execution.
- The guard should still sync `running` as soon as it writes it, so that fewer Launches become `uncertain` after a crash.

# Interrupt

```text
Tx A/Tx B      existing interrupt_operation (reason_code) prepared -> submitted
boot changed   -> termination (provider-gone) -> succeeded
ceased(G)      -> termination (kind by cause) -> succeeded
Live(G)        -> write interrupt-request naming L, G and the operation ID
               -> guard terminates T(G) -> ceased(G) -> termination -> succeeded
Absent(G), no ceased, same boot -> uncertain -> operator-attested settlement
```

- The request is a record in G's directory that names G, not a signal. A signal by PID could reach a reused PID, and Windows has no signals. Every other incarnation ignores a request that names G1.
- The guard polls for requests at a bounded interval, set by the implementation to one second or less. The request is idempotent: a replay under the same operation key writes the same request and cannot cause a second effect.
- None of these is success: a written request, a guard acknowledgement, R's exit, or an empty pane inventory. Only an accepted termination is. While the guard does not record cessation, the Interrupt stays `submitted` or `uncertain`.
- If the executor terminates for another cause first, the Interrupt still `succeeded`, because the executor has ceased. The termination keeps the kind the first writer chose. Current code cannot take this path: `CompleteCanonicalV19Interrupt` always inserts `interrupted` and requires that no termination row exists yet.
- No DDL trigger checks that `executor_binding_termination.interrupt_operation_id` names an Interrupt for the same B. The writer must check it.

# WorkerWake

## Doorbell grammar

The doorbell stays constant, bounded and Hand-owned, and its digest stays in `worker_wake_operation.doorbell_digest`. WorkerInput bytes never enter the terminal.

The doorbell is one line plus Enter, and it satisfies all of the following:

- It starts with `|`. Under `sh`, `bash`, `zsh` and `pwsh` that is a parse error before anything executes, so a doorbell that lands in the pane shell runs nothing. These are the shells that `shellForProcess` accepts (`internal/herdr/shell.go`).
- It contains only lowercase ASCII letters, spaces, `-` and `|`. That excludes history expansion (`!`, a leading `^`), comments (`#`), expansion, quoting, redirection and separators.
- It contains no digits, IDs, markers or ordinals. The drain and acknowledge commands take B from the environment.
- It is identical for every wake.

A conforming example:

```text
| hand wake - canonical worker input is pending - run hand runtime worker-input drain and acknowledge each input you observed
```

The current doorbell (`internal/store/v19workerwake_herdr_doorbell.go`) violates every rule above. Under bash it runs `hand` twice through backticks, then fails on a `<` redirection. Replacing it changes only the per-request `doorbell_digest` value.

## Live predicate W(B)

W(B) holds when every item below holds:

- B is open and its key uses a guard grammar.
- Live(G) and Live(R) hold.
- G has no `ceased` record.
- The Session workspace, tab and pane identities match the SessionBinding exactly.
- A(B) holds. On Windows, the `console` record must be written after the start of the check that uses it: the guard rewrites it with an increasing sequence number at every poll. If A(B) is not observed, on any platform, the wake is refused before any Herdr call.
- Herdr's `agent_status` for the pane is not `blocked`.

## Delivery and outcomes

Delivery uses Herdr's `agent prompt`. Its v0.8.2 contract refuses `agent_blocked`, `agent_not_ready`, `agent_not_found` and `empty_agent_prompt` before any terminal input is queued (`IsAgentPromptPreSideEffectRejection`).

| Observation | Outcome |
| --- | --- |
| W fails immediately after Tx B | Herdr is not called; this process records `no-effect` |
| Herdr returns a pre-side-effect rejection | `rejected` |
| W holds before delivery, Herdr returns success, W holds after with the same G and R | `succeeded` |
| W fails after delivery | `uncertain`, with typed residual evidence |
| The Herdr reply is lost | `uncertain`, then settlement below |

**Mechanism postcondition.** A `succeeded` WorkerWake for the `herdr` adapter means exactly this: the doorbell was applied at most once and whole, only to T(G), while G and R stayed the same incarnations. It never means that the doorbell was read, that input was acknowledged, or that the harness acted. A `succeeded` wake never suppresses a later wake while input stays pending.

**Settling a lost reply.** Settlement requires a qualified Herdr property O1: once the sending client has ended and a later exact pane observation has completed, no earlier request from that client can still be applied, and every applied request was applied whole. Hand runs every Herdr client with a bounded timeout, and O1 must cover a client that died mid-request.

With O1, the reconciler settles an `uncertain` wake as follows. It waits until the client has ended. It then observes W again. If W still holds with the same G and R, O1 positively establishes the postcondition: the send was applied at most once, whole, and only while P owned the terminal. The reconciler then settles `succeeded`. A send that was never applied still satisfies "at most once". The input stays pending, #347 Attention surfaces it, and a later wake remains allowed. Without O1, or when W fails, the wake stays `uncertain` until operator-attested settlement.

An `uncertain` wake keeps its executor-control claim on B. The anchored `operation_scope_claim_guard` trigger then refuses every Interrupt, residual cleanup and later wake for B until the wake settles. Natural or hangup cessation is not an operation, so it still closes B.

## Residual and Windows

- W proves only that P owns the terminal or console. It does not prove that the harness is at its prompt. If Herdr classifies a permission menu as not `blocked`, the doorbell letters and Enter can answer that menu. This hazard is disclosed, not closed.
- The same live incarnation before and after delivery means that neither G nor R died in between. The harness moving the foreground to one of its own groups is an accepted limit, as is an outside SIGSTOP of the guard.
- After cessation, and before it exits, the guard flushes pending terminal input.
- Windows has no foreground fact. Whichever console-attached process reads the input consumes it.
- Without the `console` association, Windows WorkerWake keeps the revision-1 refusal. A wake that could only ever be `uncertain` would block B.

# Session release with guarded executions

The anchored trigger already refuses Session release while the Session's own ExecutorBinding is open. Pane-ID reuse at Launch can put another binding's guard in this Session's workspace, so the `herdr` release adds a pre-check. The check runs before Tx B of the release, and again immediately before `WorkspaceClose` (`internal/store/v19session_herdr_release.go`). For every open ExecutorBinding of this Fleet whose key uses a guard grammar:

- **Live(G), Linux.** Hand reads G's controlling terminal from the OS and compares it with the terminal device of every pane in the workspace being closed, as Herdr reports them.
- **Live(G), Windows.** Hand needs a `console` record written after the check began. It compares that record's PIDs and creation times with every pane shell in the workspace.
- **Absent(G) on Linux, R known.** If Live(R), Hand compares R's controlling terminal the same way.
- **Absent(G) on Linux, R unknown.** The result is `unknown`.
- **Absent(G) on Windows.** Kill-on-close already ended T(G), so the binding is skipped.

A match refuses the release. So does anything that cannot be observed: an unknown incarnation, unobservable pane terminals, or a stale `console` record. A refusal before Tx B settles the release `no-effect`. A refusal after Tx B leaves it `uncertain`, and the reconciler retries the pre-check before any close. The recorded `assoc` value only points to likely offenders; the check always covers every guarded binding.

# Operator-attested settlement

#345 requires every emitted repair code to have a real recovery path, and it allows `operator-attested` only through the owning resource's contract. This section is that contract for the executor-control family. The settlement is always attestation, never observation, and it is never automatic.

Every settlement runs in one writer transaction that:

- inserts `repair_target` for exactly one target, plus `repair` with the code and bounded evidence;
- inserts `repair_resolution(resolution='operator-attested', actor_ref=<operator>, evidence_digest)`;
- applies the settlement below, with `state_evidence_digest` or `evidence_digest` equal to the resolution's digest.

| Repair code | Target | Precondition, re-observed in the same writer | Settlement |
| --- | --- | --- | --- |
| `exec-guard-wake-uncertain` | the wake `external_operation` | the wake is `uncertain`; no settlement through O1 applies | the operator attests `succeeded` (doorbell seen at the harness) or `no-effect` (not delivered and no residual) |
| `exec-guard-lost` | `executor_binding` B | no `ceased` record; no boot change; the absence check below holds | termination `provider-gone`; every `submitted` or `uncertain` Interrupt for B settles `succeeded`, citing the attested termination |
| `exec-guard-launch-uncertain`, claimed | the Launch `external_operation` | L is `uncertain`; a `claimed` record exists; no `ceased` record; the absence check below holds | in one transaction: L settles `succeeded`, B is established from the records (`r=unknown` when no `running` record exists), and B gets an attested `provider-gone` termination |
| `exec-guard-launch-uncertain`, never claimed | the Launch `external_operation` | L is `uncertain`; no `claimed` record; Hand's fence won on the boot recorded in the handoff | L settles `no-effect`; the attestation replaces O1 (no late guard-invocation bytes remain) |

**Absence check.** It runs in the same writer, and every part must hold:

- Absent(G) on the same boot.
- Absent(R), whenever R is known from a `running` record or the key.
- **Linux.** No process whose real uid is the Fleet user, in Hand's PID namespace, carries `HAND_WORKER_EXECUTOR_BINDING=B` in `/proc/<pid>/environ`. B is pre-allocated, so this check works without a `running` record. If any such process's environ is unreadable, the check is `unknown` and the repair refuses.
- **Windows.** Absent(G) and Absent(R) suffice. The guard held the job's only handle, so its death closed the job, and kill-on-close terminated T(G).

Only a `refused` record, which is an observation, produces `rejected`. An attestation never produces it.

The environ scan cannot see a descendant that re-executed with a cleaned environment. The operator's attestation covers that case.

- An `uncertain` Interrupt whose B already has an observed termination settles `succeeded` by observation, without Repair.
- `repair_resolution.actor_ref` defaults to `''` in the DDL. The Repair writer must refuse an empty actor (EG-16).
- Read models must project each attested settlement as attested. That is a required #347 follow-up.

---

# Persistence without relock

| Fact | Anchored column | Written |
| --- | --- | --- |
| guard protocol marker and credential verifier V_B | `launch_environment(name='HAND_WORKER_CREDENTIAL', value_kind='secret-ref', value_material='hand-exec-guard:v1', value_digest=V_B)` | Tx A |
| B for the harness environment | `launch_environment(name='HAND_WORKER_EXECUTOR_BINDING', value_kind='literal', value_material=B)` | Tx A |
| request and launch-spec commitment covering both rows | `external_operation.request_digest`, `launch_operation.launch_spec_digest` | Tx A |
| G, R (or `r=unknown`), P, pane locator and terminal, the association Launch observed (`assoc`), OS, object identity, digest and class | `executor_binding.provider_executor_key` with grammar `herdr-exec-guard:v1?...`; `executor_binding.adapter_ref='herdr'` | Launch success |
| Launch, WorkerWake and Interrupt evidence | `external_operation.state_evidence_digest`, `external_operation_event.evidence_digest` | each transition |
| cessation | `executor_binding_termination(terminal_kind, interrupt_operation_id, observed_at, evidence_digest)` | executor observation, Interrupt writer, or attested settlement |
| attested settlement | `repair_target`, `repair(repair_code, reason, evidence_digest)`, `repair_resolution(resolution='operator-attested', actor_ref, evidence_digest)` | Repair writer |
| wake residual | `executor_residual_cleanup_operation.residual_identity_digest` | residual cleanup |
| attested acknowledgement | `worker_input_acknowledgement.evidence_digest` | acknowledge |
| handoff, claim and guard records | Fleet-private files; non-canonical; only their digests reach SQLite | guard / Hand |

- Every column used accepts the planned values:
  - The text columns carry only length checks.
  - `launch_environment.value_kind` already allows `secret-ref`.
  - `terminal_kind` allows all four kinds.
  - `repair_resolution.resolution` allows `operator-attested`.
  - The `uncertain -> succeeded | rejected | no-effect` transitions are legal.
  - `executor_binding_insert_guard` keeps provider keys unique among open bindings, and the incarnation makes each key unique.
- **Why `adapter_ref` stays `herdr`.** The anchored triggers require one `adapter_ref` along the whole chain: `attempt.session_adapter_ref`, then the Session operations and binding, then Launch and `executor_binding`, then wake, Interrupt and residual cleanup. A new value would therefore also change the Session adapter of every new Attempt. It would strand open `herdr` Sessions, and it would present the guard as a replaceable provider. The key grammar version is the discriminator. The legacy grammar `herdr-executor:v1` was never established, and any key without a guard grammar keeps the revision-1 refusals.
- A #344 relock would be needed for any of: non-digest evidence rows; a new operation kind (for example a standalone abort); a CHECK on key grammar; or more than one ExecutorBinding per Attempt. This revision needs none of them.

# Adapter documentation (revision-1 common contract, `herdr` with guard)

| Item | Launch | WorkerWake | Interrupt |
| --- | --- | --- | --- |
| first mutation boundary | `herdr pane run` after Tx B | `herdr agent prompt` after Tx B and a passing W | interrupt-request write after Tx B |
| strongest positive postcondition | guard records plus Live(G), or accepted cessation; never wake-deliverability | at most one whole application, only to T(G), with the same G and R; shown by W before and after, or by O1 settlement | termination accepted |
| same-key idempotency | no replay; the single claim bounds effects to one execution | no; coalescing-safe | yes |
| destructive identity | G, verified from the OS | — | G, bound by the request record |
| unknown | `uncertain`, no fabricated binding, attested Repair | `uncertain`, O1 or attested Repair | `uncertain`, attested Repair |
| extension evidence | key grammar and digests | digests | digests |
| secret redaction | S_B confined as described in "Credential" | none carried | none carried |

---

# Per-platform strength

| Property | Linux | Windows |
| --- | --- | --- |
| incarnation | boot_id + PID + starttime | PID + creation time read through a handle |
| boot-change witness | exact | tick decrease: misses Fast Startup, never fabricates; `BootId` pending qualification |
| executable object | `exact` for native; `sampled` for scripts | `verified` with frozen content; `sampled` for scripts |
| content digest | sampled | describes the loaded main image |
| containment and cessation | subreaper, `tree` (`ECHILD`) | job without breakaway, `tree` (`ActiveProcesses == 0`) |
| pane association (wake only) | OS terminal and `tpgid` equal Herdr's pane | the guard's console list contains Herdr's shell PID with a creation time read from the OS |
| wake | W with foreground proof | W without a foreground fact; refused without the console association |
| guard crash | `unknown`, then a boot change or attestation | `unknown`, then a full restart or attestation |
| outside-daemon effects | not covered | not covered |

# Invariants

- **EG-1 Commitment before mutation.** Tx A commits V_B, B and the exact spec. Tx B precedes `herdr pane run`. S_B appears in no location that "Credential" excludes.
- **EG-2 Single claim.** At most one guard starts a harness for handoff(L). A guard claim and a Hand fence exclude each other. A claim is synced to disk before its harness starts, and the guard refuses a handoff from another boot.
- **EG-3 Exact incarnation.** Observation or control for B acts only on the process whose OS-read incarnation equals B's key. Any difference is `absent` or `mismatch`, never the target.
- **EG-4 No Herdr identity authority.** Herdr facts alone never establish, wake, interrupt or terminate B. Herdr daemon PID, ancestry and environment, and PPID walks, are never inputs to a positive result.
- **EG-5 Launch success.** A binding exists only with valid `claimed`, `pinned` and `running` records that name the committed digests, together with either Live(G) or accepted cessation. Launch success never depends on A(B) and never implies it.
- **EG-6 Positive no-effect.** `no-effect` for a `submitted` Launch requires a winning fence on the recorded boot, plus O1.
- **EG-7 Object honesty.** The recorded object is the object the guard opened, with its true class. Neither a PATH search nor a basename or argv match is ever object evidence.
- **EG-8 Termination source.** A termination row requires one of: a `ceased` record for exactly G with the platform predicate; a positive boot change; or an operator-attested settlement whose absence check held (Absent(G), Absent(R) when R is known, and on Linux an empty environ scan for B).
- **EG-9 Interrupt success.** `succeeded` requires an accepted termination for B. Request acceptance is never success. The kind is `interrupted` only when the `ceased` cause names that exact pending Interrupt.
- **EG-10 Wake success.** `succeeded` requires W before and after delivery, or O1 settlement, or attestation. A failed pre-check means Herdr is never called.
- **EG-11 Attestation.** Drain or acknowledge is accepted if and only if the presented secret hashes, for this Fleet and B, to B's stored V_B, and B is open and current.
- **EG-12 Environment neutrality.** No `daemonEnvironmentKey` value inherited from the daemon, pane or shell reaches the harness. Only the exact spec values do.
- **EG-13 Generation isolation.** No record, request, credential or key derived from G1 or B1 changes any state of G2 or B2.
- **EG-14 Terminal inertness.** The terminal carries only the fixed-shape guard invocation and a doorbell that conforms to the grammar.
- **EG-15 No guard writes.** The guard never opens the canonical SQLite DB.
- **EG-16 Attestation is not observation.** Every non-observed settlement has exactly one `operator-attested` Repair resolution, created by an operator request. The writer refuses an empty `actor_ref`. No code path creates one automatically.
- **EG-18 Release safety.** A Session release never closes a workspace that holds a live guard of any open binding, whichever Session that binding names.
- **EG-17 Version continuity.** A record from a guard protocol version present in any open key is always readable. An unknown version is refused, never read as absent.

# Counterexamples and the point that excludes each

1. **A stale G1 guard acts on G2.** G1 knows only L1 and writes only records that name G1. Hand checks each record's incarnation against B2's key (EG-3, EG-13). A request for G2 names G2, so G1 ignores it.
2. **PID reuse impersonates G.** G dies and an unrelated process receives its PID. The start time differs, so Absent(G) holds. No request can reach the new process, because control never signals by PID (EG-3).
3. **The guard crashes.** SIGKILL hits only G after `running`. On Linux, R reparents to init and keeps running with S_B. Hand sees Absent(G), no `ceased` and the same boot, so the state is `unknown` with no termination row. An `exec-guard-lost` attestation is refused while Live(R) holds, or while any readable environ carries B (EG-8). Recovery comes after R and every carrier of B are gone, or after a boot change.
4. **The guard dies before `running`.** On Linux, R starts and the guard dies before it writes `running`. L is `uncertain` and R is unknown. The environ scan finds R through `HAND_WORKER_EXECUTOR_BINDING=B`, so attestation is refused while R lives. Once no carrier remains, L settles `succeeded`, with B marked `r=unknown` and given an attested termination. It never settles `rejected` (EG-8).
5. **A wake reaches a replaced pane process.** R exits, the guard records `ceased`, and the shell regains the foreground. Pre-W then fails, Hand makes no send, and the wake is `no-effect`. If the replacement happens between pre-W and post-W, the wake is `uncertain`. The doorbell is then a parse error in the shell (EG-10, EG-14).
6. **A stale caller secret.** A caller presents S_B1 to drain B2: the hash differs, so the call is refused. A caller presents S_B1 after B1 terminated: refused, because positive cessation leaves no process of T(G1) (EG-11).
7. **Cross-Fleet environment leak.** A Fleet-B Herdr daemon started inside a Fleet-A worker inherits `HAND_HOME=A` and Fleet A's `HAND_WORKER_*` values. The Fleet-B guard scrubs them before starting its harness (EG-12). A Fleet-A harness that presents S_A to Fleet B is refused, because V is recomputed with Fleet B's ID (EG-11). That daemon also ends with T(G_A), as described under "Nested Fleets".
8. **Two guards for one handoff.** The pane shell runs the typed command twice. The second rename fails, and that guard starts no harness (EG-2).
9. **Path swap between pin and exec.** On Linux a native binary runs from the pinned descriptor. On Windows the guard verifies the suspended child against the pinned object and refuses on a mismatch (EG-7). A script records `sampled`.
10. **Crash after Tx B, before `pane run`.** The reconciler fences handoff(L). With O1 on the same boot the Launch is `no-effect`; otherwise it is `uncertain`, with no execution possible. There is never a second invocation (EG-6).
11. **The Interrupt is accepted but the tree survives.** No `ceased` is recorded, so the Interrupt stays `submitted` or `uncertain` (EG-9).
12. **A `setsid` grandchild.** On Linux it reparents to the guard, and `ECHILD` waits for it. On Windows it stays in the job.
13. **Fast exit.** R exits before Hand observes it. `running` and `ceased` exist, so Hand writes `succeeded` together with the binding and its termination, never `no-effect`.
14. **Power loss, then history recall.** The boot changes, and every earlier T(G) positively ceased. An operator recalls the old guard command from shell history. The guard compares the handoff's boot with the current boot and records `refused` (EG-2). A synced claim also makes a later fence fail.
15. **A forged interrupt request.** A same-user process writes a request that names G and no pending Interrupt. The guard terminates T(G), and Hand labels the termination `failed`, never `interrupted` (EG-9).
16. **Pane-ID reuse at Launch.** Herdr reuses a pane ID between the L2 preflight and its `pane run`, so the guard for B2 lands in Session 1's pane. L2 succeeds with `assoc=mismatch`, and wakes for B2 are refused. When B1 ends, the Session 1 release pre-check reads G2's controlling terminal (or, on Windows, its console list) on Session 1's workspace pane. It refuses, so B2 is not destroyed (EG-18).
17. **A pane that Herdr cannot observe.** The guard claims L and writes `running`, and G is live, but Herdr reports no usable pane association. L settles `succeeded`, B exists, and Interrupt works through the guard. Every wake for B is refused before any Herdr call, and on Windows the revision-1 refusal stays (EG-5, EG-10).
18. **A lost wake reply.** The wake stays `uncertain`, and Interrupt for B is refused. With O1 and the same G and R, the reconciler settles `succeeded`. Otherwise the operator attests the outcome (EG-10, EG-16).

# Known limits this revision does not close

- macOS has no enabled mechanism.
- Effects started through outside daemons are outside every platform's claim.
- There is no protection against a hostile process of the same user.
- Content digests are sampled on Linux, and scripts are `sampled` on every platform.
- W does not prove that the harness is at its prompt. The doorbell can answer a harness menu that Herdr does not classify as `blocked`.
- Harness-internal foreground changes and an outside SIGSTOP of the guard are accepted gaps.
- Windows has no foreground fact.
- A guard crash leaves B open until a boot change or `exec-guard-lost` attestation.
- A Windows Fast Startup shutdown is not detected until `BootId` qualifies.
- A lost wake reply blocks Interrupt for B until O1 settlement or attestation.
- A Linux guard stopped between R's start and its `running` record leaves a live harness with no B until the guard resumes or disappears.
- An established B whose pane association Hand never observes can be interrupted, but never woken.
- These Herdr 0.8.2 behaviors are unmeasured: O1, pane-close signals (SIGHUP versus SIGKILL to the guard), daemon restart, and agent-status accuracy. A SIGKILL of the guard degrades cessation to `unknown`.
- A harness that Herdr does not recognize as an agent gets `agent_not_found`, so every wake for it is `rejected`. Wake support is qualified per harness (#305).
- The Linux environ scan misses a descendant that re-executed with a cleaned environment. Attestation covers that case.
- A Session release whose pre-check cannot observe every guard of an open binding refuses.
- On a Linux kernel without `CONFIG_PROC_CHILDREN`, a fast-forking descendant can prevent convergence.
- A crash can leave the handoff or claim file, which holds S_B and every resolved secret-ref plaintext, until reconciliation deletes it.
- A Fleet started inside a guarded execution ends with that execution.
- The PID-namespace and user check covers only the facts named here. Container or silo isolation beyond them is unsupported.

# Required follow-ups

- **#347 read model.** Project, for #305: the object class, attested settlements, `unknown` executors (guard gone, no termination), and blocked executor-control scopes.
- **Implementation findings (code, not DDL).**
  - The termination writer must check that `interrupt_operation_id` targets the same B.
  - `CompleteCanonicalV19Interrupt` must support an executor that already terminated.
  - The doorbell must be replaced.
  - drain/acknowledge must read B from the environment.
  - The Repair writer must refuse an empty `actor_ref`.
  - Session release must add the guarded-execution pre-check.
  - The upgrade path must add the cross-Fleet reader scan.
- **#305.** Qualify Herdr agent recognition per harness.

# Acceptance tests implementation must add

Each test cites the invariant it checks. Native process tests run real processes on Linux and Windows. Fakes may cover SQLite-only transitions. The #346 tracker list stays required:

- same-basename foreign executable;
- PID reuse;
- stale executor generation;
- provider restart;
- unknown process inventory;
- wrapped or background execution;
- late historical acknowledgement without successor retargeting;
- zero mutation after a failed re-proof;
- structured argv, env and cwd behavior;
- refusal before execution when changing the cwd fails.

**Guard primitive, Linux and Windows.**

- The recorded incarnation equals Hand's OS re-read. A synthetic record with the same PID and a different start time is `absent` (EG-3).
- Two guards racing one handoff start exactly one harness. A fence racing a claim has exactly one winner. A simulated loss of unsynced writes cannot undo a claim whose harness started (EG-2).
- A handoff from another boot, PID namespace or user is refused before a harness exists (EG-2).
- A cwd failure or path replacement between pin and start refuses before any harness instruction runs. So do a symlink retarget and the same basename in another directory. A script records `sampled` (EG-7).
- The harness environment contains the exact spec and no inherited semantic key. S_B is absent from the command line, the records and the output (EG-1, EG-12).
- Linux: a double-fork plus `setsid` grandchild delays `ceased` until reaped. A fork loop is frozen and reaped by the sweep. A descendant PID namespace is covered where unprivileged user namespaces exist (EG-8).
- Windows: a grandchild stays in the job, breakaway is refused, and `ceased` comes only after `ActiveProcesses == 0`. The guard stays outside its own job (EG-8).
- A SIGKILLed guard leaves no `ceased` record. An immediate harness exit produces `running` and `ceased`. Terminal hangup produces `ceased` with cause `hangup` (EG-5, EG-8).
- Linux signals: an ignored disposition does not leak into the harness, and the guard's `tcsetpgrp` and `tcflush` calls from its background group complete.
- An unknown record version is refused, not treated as absent. The guard holds its Hand generation lease while it runs (EG-17).

**Hand Launch reconciler.**

- A crash at each boundary classifies as the Launch table requires, with no second invocation. The boundaries are: after Tx A, after the handoff write, after Tx B, after `pane run`, after `claimed`, and after `running` (EG-1, EG-5, EG-6).
- A fence after a boot change is not `no-effect`. The deletion order holds (EG-6).
- With Live(G) and valid records but no observable pane association, L succeeds. Interrupt of that B succeeds, and every wake for it is refused before any Herdr call (EG-5, EG-10).
- A record that names a different V_B, request digest or launch-spec digest never succeeds (EG-5).
- The key grammar round-trips, and the open-key uniqueness trigger holds.
- A Launch without the credential row keeps the refusal.

**Interrupt.**

- The Interrupt succeeds only after `ceased`. A stalled guard keeps it `submitted` (EG-9).
- A boot change gives `succeeded`. A guard crash gives `uncertain` and then `exec-guard-lost` attestation (EG-8, EG-16).
- `exec-guard-lost` is refused in each of these cases: Live(R); a readable environ carrying B; an unreadable same-uid environ; an empty `actor_ref`. On Windows, Absent(G) and Absent(R) are accepted (EG-8, EG-16).
- `exec-guard-launch-uncertain` with a `claimed` record gives `succeeded`, with B marked `r=unknown` and given an attested termination. Without a claim, and with a winning fence, it gives `no-effect`. No attestation produces `rejected` (EG-8).
- G2 ignores a G1 request (EG-13).
- A forged request is labeled `failed`. A natural exit before the request is labeled `completed` or `failed`, and the Interrupt succeeds (EG-9).

**WorkerWake.**

- A pre-check failure makes no Herdr call. `agent_blocked` gives `rejected`. A replaced pane process or R exit between pre-W and post-W gives `uncertain` (EG-10).
- A lost reply settles `succeeded` only with O1 and the same G and R. Without O1, it needs attestation. While `uncertain`, an Interrupt is refused, and natural cessation still closes B (EG-10, EG-16).
- A `succeeded` wake does not suppress a later wake while input stays pending (EG-10).
- The doorbell is constant, has no digits, and matches the grammar. Interactive `bash` and `zsh` (with `CORRECT` on), `sh` and `pwsh` all report a parse error, execute nothing, and read the next line normally (EG-14).
- Windows: without the console association, the wake refuses. On any platform, a failed A(B) refuses the wake before any Herdr call.

**WorkerInput protocol.**

- A valid credential drains in ordinal order.
- Each of these is refused: a missing credential; a wrong credential; S_B1 against B2; S_B1 after B1 terminated; another Fleet; B not yet established; argv IDs that differ from the environment's B.
- The comparison runs in constant time (EG-11).

**Session release and upgrade.**

- A release refuses while a live guard of another open binding sits in the workspace (simulated pane-ID reuse), and also while any guarded binding cannot be observed (EG-18).
- An upgrade refuses when the new build lacks a reader for a protocol version in any registered Fleet's open keys, or when it cannot read a registered Fleet (EG-17).

**Real provider (Herdr 0.8.2 with a stub harness and the real guard).**

- Launch, wake, drain, acknowledge, and Interrupt.
- Pane close and daemon restart.
- O1 qualification.
- Agent-status accuracy for permission prompts.
- Two Fleets on one host with a contaminated daemon environment.
- A Fleet started inside T(G) ends with it (EG-4, EG-6, EG-12).

# Lock conditions for revision 2

- [ ] Every "Retained revision-1 contract" section still holds without edits to revision-1 bytes.
- [ ] Guard records that agree with the OS facts Hand reads are the only execution identity authority. Herdr remains addressability and doorbell transport.
- [ ] Credential, handoff and terminal confinement is explicit. The threat model excludes hostile same-user processes.
- [ ] Linux and Windows classes are explicit and persisted. macOS keeps the revision-1 refusals, and no macOS test gates Linux or Windows.
- [ ] A guard crash is `unknown`. Only a `ceased` record, a boot change, or operator-attested `exec-guard-lost` terminates B.
- [ ] Each emitted repair code has the settlement path defined here, including its absence check.
- [ ] Session release refuses while any live guard of an open binding sits in the Session's workspace.
- [ ] "DDL impact: NONE" is verified against `docs/architecture/v19-v6.sql.gz`, with `adapter_ref='herdr'` and a versioned key grammar.
- [ ] The acceptance tests for a platform pass before any refusal is removed on that platform.
- [ ] A reviewed manifest anchors this blob before dependent implementation starts.
