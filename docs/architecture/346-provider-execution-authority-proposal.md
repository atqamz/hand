# Provider execution authority — proposal 1

Status: **DRAFT; not a replacement for any frozen v19 contract.**

Owners: Hand #346 (adapter responsibility), #343 (external effects), #345
(currentness), #339 (production integration), #305 (qualification).

Baselines: Hand `c15cbd3d444ef47e8f9cd4321314c6310a59e2b8`; Herdr
`23479dd162caba32379d405b8c8107ff2ae8dbaf`. The operator supplied a Linux-local
investigation dated 2026-09-23. Its scripts/logs were not supplied with the report;
its findings are reported evidence, not independently rerun qualification.

## Decision proposed

Execution authority belongs at the provider's real process-creation/control
boundary. Herdr owns that mechanism; Hand owns Task/Plan/Attempt, input,
acknowledgements, qualification, decisions, and external-operation currentness.
Do not add a second process supervisor inside Hand or infer authority from pane
text, argv, foreground inventory, or a new SQLite helper.

The first implementation slice is a Linux native-executable process primitive.
It is not a new stable API or a completed Hand adapter. No runtime-lock change,
existing fail-closed guard removal, automatic migration, or release follows from
this proposal or a component test passing.

## Identities and scope

An execution is one provider-created root process lifetime. Its handle is minted
by the owning provider instance; it is not a PID, pane locator, path, or caller
supplied label. A new spawn always receives a different handle. PID values may
be returned only as diagnostic observations.

The initial executable object is a separate fact. The provider pins the object
before spawn and executes through that same open object, not a later pathname
lookup. It reports exactly what this proves. Device/inode identify the pinned
object, not a cryptographic guarantee of immutable file contents, dependencies,
or program behavior. A digest sampled before exec is not silently promoted to
proof of the bytes mapped by the kernel.

A same-PID `execve` continues the root process lifetime. The immutable launch
receipt still describes the initial image; it is not a claim that the process
cannot replace its image. Current-image observation, if offered, must be separate
and timestamped. Continuous executable-image enforcement is not introduced.

Initial support is direct native ELF execution with explicit argument vector,
explicit child environment, and a pinned working directory. Scripts, shell
command strings, implicit PATH search, and script/interpreter identity are
unsupported in this first slice. This implementation sequence does not remove
any platform/harness obligation from 0.8.0; extending the support matrix requires
its own positive evidence.

## Launch and receipt boundary

Future API names below are proposed, not existing Herdr commands.

`execution.start` must be a newly advertised method. Adding ignored fields to
old `pane.run`/`pane.send_keys` cannot establish a conditional operation.

Request fields are an explicit provider epoch, caller-retained operation ID,
structured executable/arguments/environment/cwd, and only the placement
preconditions actually required by the owning runtime. Argument-vector semantics
must specify whether argv[0] is included; no string joining or shell escaping is
an execution protocol.

Hand commits its exact submitted operation before the first possible provider
mutation. The provider associates the operation with one execution at the spawn
boundary, not by searching the pane afterward. A successful native exec is launch
evidence, not harness readiness, an input acknowledgement, or Task completion.
Immediate successful exec followed by exit remains a real execution, not no-effect.

The candidate intentionally exposes `RootCreated`, not a successful-exec receipt.
`RootCreated != ImageObserved != harness readiness != semantic completion`.
If an executable exits before positive image observation, the result stays
`Unverified`, even if its exit code is zero.

A launch that may have created a process cannot be reclassified as no-effect
because later identity observation failed. Errors must distinguish refusal before
creation, failed exec with a positively reaped child, and possible/uncertain effect.
No fallback to ordinary shell-text launch is permitted.

## Request replay and provider restart

The API needs exact same-operation replay without a second spawn. Changed payload
under the same ID refuses. Concurrent duplicate requests have one effect. A lost
reply is recovered using the original operation and provider epoch.

Minimal first design: keep bounded receipts/tombstones for one provider epoch;
refuse new requests on capacity exhaustion instead of evicting replay authority.
After provider restart, the old epoch is rejected before mutation. A caller may
not reissue an uncertain old request under a fresh epoch. Hand retains the
unresolved submitted operation and requires observation/recovery.

This conservative design does not promise live handoff or reconnectable control
of orphaned execution after provider restart. Epoch invalidation does not prove
old processes ceased. Resources remain protected. If durable cross-restart
receipt/handle recovery is required, specify and test that extension before
advertising it; do not reconstruct authority from matching PID/path/argv.

## Control and cessation

Provider observation/control resolves the exact execution handle while holding
the owning runtime's mutation exclusion. Kernel-backed process references must
survive PID reuse races. A client-side observe-then-signal-by-PID sequence is not
conditional control.

The first leaf primitive controls only its owned root process. A successful
signal request is not positive cessation. Cessation needs the owning child's
terminal wait status or the exact kernel reference's exit evidence. Unknown/error
is not exited. All error paths retain or reap owned resources; none search for a
process by name.

Root cessation is explicitly **not** process-tree cessation or worktree cleanup
permission. A background child may outlive its parent or leave the foreground
inventory. Session/worktree release must remain blocked until its separate
ownership and preservation criteria are proven. No process-group scan, timeout,
or empty foreground list is substituted for that proof.

## Wake and input attestation remain separate slices

`execution.wake` would require the same exact handle and executor-control
exclusion. It may carry only the bounded Hand-owned mechanism, never arbitrary
WorkerInput/Answer payload. Method availability, stale-target refusal, replay,
and residual-control behavior need real tests before advertisement. Root signal
support does not implement canonical WorkerWake.

Hand mints a separate per-execution worker credential and binds its verifier to
the exact ExecutorBinding/launch generation. Only the corresponding structured
launch receives the credential. Drain/ack verifies it and currentness before
semantic access; caller-supplied IDs and `HAND_ROLE` are insufficient.

The future design must close the interval between native spawn and canonical
ExecutorBinding establishment: an early worker cannot manufacture binding or
ack authority. Retrying a pending binding is not permission to retarget another
executor. Old credentials never authorize successor input. Credentials are not
included in receipts, logs, command lines, or ordinary structured output.

This protects accidental/stale callers through the supported protocol. It is not
a security boundary against a hostile same-UID process with memory/credential
or direct database access. Do not claim such protection without actual OS
isolation and restricted DB authority.

## Contract and schema impact

The existing opaque provider executor key is a candidate for the versioned
provider handle plus bounded verification provenance. No DDL change has been
proved necessary. Before wiring, verify exact existing size, grammar, currentness,
secret-redaction, and recovery consumers. A changed frozen semantic contract must
be versioned/reviewed; actual relational changes require the established relock.
Neither a mutable issue edit nor this draft redefines current authority.

## Acceptance by slice

1. Native process primitive: literal argv/env, pinned cwd/executable, pre-spawn
   rejection, exact root control/wait, fast exit, path replacement, same-PID exec,
   failed spawn cleanup, and background-child counterexample. Actual kernel
   process tests, not parsed transcript examples.
2. Provider ownership/API: one-winner operation replay, payload conflict, bounded
   tombstones, required epoch, restart refusal, receipt-loss recovery, real PTY
   lifetime and serialized replacement/control. No stable codec reinterpretation.
3. Hand adapter: exact handle/currentness, early binding, per-execution drain/ack,
   stale positive/negative caller controls, source-attested reports, and no blind
   replay of submitted/uncertain operations.
4. Full vertical workflow: production init/project/Task/Plan/Attempt, real coding
   harness, supervisor replacement, input/ack/report, independent acceptance,
   and preservation-safe resource release. Native platform/release qualification
   remains separate.

Passing a leaf test does not check off slices 2–4. Do not release an unsupported
provider by replacing errors with fabricated receipts.

## Candidate evidence as of 2026-09-23

A standalone downstream Rust component was implemented and tested separately
from the Herdr application. It uses `clone3(CLONE_PIDFD)`, pinned executable/cwd
descriptors, and `execveat(AT_EMPTY_PATH)`; root signals/wait use the owned pidfd.
An in-memory epoch/operation registry retains replay and refusal tombstones and
refuses changed payloads or old epochs. It neither installs a provider nor
adds a service to Hand.

On Linux x86_64 kernel 6.18.44 with Rust 1.98.1, one registry unit test and 18
native-process tests passed, including 20 complete repetitions. The final test
binaries also passed under UID 65534. Two deliberate regressions (path
re-resolution and disabled replay lookup) caused assertion failures in their
own tests, then the source was restored and the suite passed again.

These tests exercise real local process creation/control, not a coding harness
or Herdr's PTY/API. Same-PID exec is exercised in the new component; actual kernel
PID reuse is not forced. Epoch replacement is exercised using separate authority
instances, not an actual Herdr crash/handoff. Original operator probe logs/scripts
were not independently rerun. No independent code review or full repository,
native multi-platform, or release qualification is claimed.

Candidate gaps that must remain visible: PTY reservation/wire dispatch,
per-execution caller attestation, canonical WorkerWake, provider restart
recovery, process-tree containment/cleanup, and production Hand callers. Stdio
fingerprints identify dev/inode only, not an open-file-description, stream offset,
or qualified PTY placement. The owning runtime must retain the root object until
cessation; dropping the component is not cleanup. None of these gaps is closed
by a passing kernel test.

## Upstream boundary

At the recorded Herdr revision, CONTRIBUTING.md restricts implementation PRs to
approved contributors; atqamz is not in the approved-contributor list. No upstream
implementation PR or feature acceptance is assumed. Keep the candidate as a
separate downstream patch pending a maintainer-supported path or an explicit
product decision. Do not silently commit to a permanent Herdr fork.
