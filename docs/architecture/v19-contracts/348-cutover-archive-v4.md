---
source_issue: 348
source_title: "docs(architecture): lock v18→v19 cutover, WorkerInput non-fabrication, and legacy archival semantics"
source_url: https://github.com/atqamz/hand/issues/348
contract_version: v19-cutover-v4
supersedes_path: docs/architecture/v19-contracts/348-cutover-archive-v3.md
supersedes_blob: b67acfad620ca08ff031531ce93fe5e8e39589f9
---

# Canonical persistence cutover and legacy archival contract — revision 4 candidate

This candidate supersedes revision 3 for source-writer cessation, cutover eligibility, and the publication ordering that exclusion depends on. It does not modify revision-2 or revision-3 bytes.

Revision 2 continues to govern everything else unless a section below tightens it: the original archive, the SHARED → observed reader barrier → OUR EXCLUSIVE handoff, Fleet-local lock and provider quiescence, the 21 freeze guards and `user_version = 22` sentinel, import classification, WorkerInput non-fabrication, the fresh target, the advisory marker, durability, the registry boundary, and the startup recovery matrix. Revision 3's exact-source counterexample and its refusal of live automatic cutover remain in force. The #344 DDL does not change.

This revision becomes normative only after an independently reviewed permanent manifest anchors this exact Git blob. Until then revision 3 remains the anchored snapshot.

## Decisions

1. Cutover from exact v0.7.2 is offline only. Live automatic cutover stays refused before MigrationLock, as revision 3 requires.
2. Cessation of every process that could act on pre-freeze legacy intent is proven by the freeze plus a built-in boot-identity witness. An operator assertion, a free lock, a stable digest, a delay, and a process scan are not that proof.
3. After a positive witness and before any canonical build for publication, every imported Project fact and the Fleet's Treehouse and Herdr state are re-observed against the frozen evidence. Any drift or unknown blocks.
4. 0.8.0 keeps serving legacy v18 homes. The offline cutover is optional and starts only when the operator runs it.

## Model

Subjects:

- Home H: one Fleet home directory and its `state/hand.db`.
- Legacy source S: the exact v0.7.2 schema family at `state/hand.db`.
- Legacy writer: a process of any Hand build that can act on H through the legacy path. This includes v0.7.2, 0.8.0's legacy commands, edge and checkout builds, and test binaries. It also includes every descendant such a process started, even after that descendant outlives or is reparented away from its parent. Git, Treehouse, Herdr, and `gh` children acting for a Hand command are legacy writers.
- Freeze commit F: the COMMIT of the frozen-bridge transaction.
- Boot session: the lifetime of one running kernel instance on the machine that runs the cutover.
- Pre-freeze writer: a legacy writer alive at F. It may hold intent from an authoritative DB read made before F and act on that intent after F without touching the DB. The revision-3 `project sync` lock waiter is the reproduced instance; 0.8.0's legacy `project sync` has the same read, release, lock, then mutate order.
- Post-freeze writer: a legacy writer started after F. It meets the schema check before it can act on imported facts (see "New legacy processes after the freeze").

What each fact proves:

- The frozen bridge proves that no legacy DML and no new legacy open of `state/hand.db` succeeds after F. It proves nothing about external effects of pre-freeze writers.
- The boot witness proves that no process alive at F is alive now. It observes kernel identity, not Hand.
- The drift gate proves that imported and quiescence-relevant external state equals the frozen evidence when the gate runs. Only after the witness is that result stable, because no pre-freeze writer remains to change it.

## 0.8.0 behavior for a legacy home

- 0.8.0 serves a legacy v18 home through its legacy commands until the operator runs the offline cutover.
- No startup path, `init`, `update`, read-only command, or ordinary mutating command begins, resumes, or completes a cutover.
- `hand init` on 0.8.0 keeps creating legacy-family homes. Refusing legacy homes and canonical-default `init` are outside this revision.
- After F, the frozen `user_version = 22` refuses legacy access. v0.7.2 and 0.8.0's legacy path both fail it as newer than supported before any legacy DML.
- 0.8.0 refuses a frozen bridge before any side effect under H. Its refusal names the pending cutover instead of advising an upgrade.
- Only the offline cutover command and `hand cutover recover` act on a frozen bridge.

## Offline sequence

The operator runs one command twice, with a restart of the machine between the two runs.

Freeze run:

1. Refuse unless the filesystem that holds `state/hand.db` is classified local (see "Kernel scope").
2. Acquire MigrationLock.
3. Run revision 2's gate, nonblocking Fleet-local lock closure, durable-state classification, and provider/resource quiescence. Here they are preconditions for a consistent snapshot, not cessation proof. A busy or unknown result still refuses.
4. Promote the original archive and persist the pre-freeze manifest as revision 2 requires. Manifest format v2 records revision 2's manifest facts and the certificate version. It omits the certificate value, which depends on the manifest digest.
5. Read the freeze boot evidence E_F for this platform (see "Boot witness"). Unreadable or malformed evidence refuses before any source mutation.
6. In the one freeze transaction on OUR EXCLUSIVE connection:
   - require that no `meta` key starting with `v19-cutover-` exists;
   - insert `meta.key = 'v19-cutover-freeze'` with value `v2:<source-sha256>:<manifest-sha256>:<evidence-sha256>`;
   - insert `meta.key = 'v19-cutover-freeze-evidence'` whose value is exactly the E_F bytes hashed into the certificate;
   - create the 21 revision-2 guards;
   - set `user_version = 22`;
   - COMMIT.
   Every digest is a lowercase 64-hex SHA-256.
7. Release locks and handles as revision 2 requires. Perform no provider, Git, registry, or network action after F. Report that a full restart is required before completion.

Completion run, after the restart:

1. Refuse unless the filesystem is classified local.
2. Acquire MigrationLock.
3. Recognize the exact v2 bridge: revision 2's recognition rules, exactly the two `v19-cutover-` `meta` rows above, and a matching original archive and manifest.
4. Evaluate the witness against E_F read from that bridge. A negative result is `reboot-required`; an unknown result is `cessation-unknown`. Both leave every file unchanged.
5. Run the drift gate. Drift or unknown is `drift`, names the differing subjects, and changes nothing.
6. Build and validate the canonical temp from the original archive and import plan, as revision 2 requires.
7. Publish by atomic replacement (see "Publication without an absent active DB").
8. Continue with revision 2's post-publication validation and registry projection.

Every path that builds for publication, retires, or publishes from a frozen bridge runs steps 4 and 5 first within the same MigrationLock hold. This includes `hand cutover recover`. No witness or drift result is cached across runs.

## Freeze evidence

- E_F is authoritative only as committed inside the freeze transaction. A copy in a manifest, marker, log, or earlier attempt never counts.
  - Counterexample: attempt 1 writes the manifest in boot session B1 and crashes before F. The machine restarts. Attempt 2 reuses the persisted manifest and commits F in B2. Evidence taken from the manifest would name B1, so the witness would pass with no restart after F.
- The freezing process reads E_F after manifest persistence and before COMMIT. A restart between the read and COMMIT kills that process, so a committed E_F always names the boot session of F.
- E_F contains: evidence format `v1`; platform (`linux`, `darwin`, or `windows`); token kind; token value; an informational UTC wall time. Wall time never decides an outcome.
- The certificate binds the manifest digest, so the drift baseline is unique. A manifest whose digest differs from the certificate refuses.
- A `v1:<source-sha256>` bridge has no E_F and never satisfies the witness. Read-only inspection still recognizes it; recovery refuses to build from it or publish it. No command in any released or `main` build ever called the freeze; only tests did, so no production home holds a v1 bridge.

## Boot witness

The witness is positive only when current evidence is read positively, on the platform recorded in E_F, and meets that platform's rule. Every read failure, malformed value, and platform mismatch is unknown and refuses.

| Platform | Token | Positive when | Limits |
| --- | --- | --- | --- |
| Linux | `/proc/sys/kernel/random/boot_id` | current UUID differs from recorded UUID | This is the host kernel's identity. A container restart is not a machine restart, so completion refuses. Hibernate and resume keep the ID and the processes. |
| macOS | `sysctl kern.bootsessionuuid` | current UUID differs from recorded UUID | Apple does not formally document this interface. Qualification must show it stays stable across sleep and changes across restart on each supported major version. `kern.boottime` may be recorded for diagnostics only, because the kernel can adjust it when the clock is set. |
| Windows | milliseconds since boot from `GetTickCount64` | current value is less than recorded value | Completion refuses once uptime since the restart reaches the uptime recorded at F. The freeze report states that bound. Shutdown with Fast Startup resumes a hibernated kernel and is expected not to reset the count, so a full Restart is required. Wall-clock boot time is rejected because clock changes move it. |

The witness assumes that no process survives a change of boot session. Process checkpoint/restore (for example CRIU) and VM memory snapshot restore are outside the model.

## Process scan is not a witness

A fail-closed scan must treat process P as a possible writer unless it proves that P holds no pre-freeze legacy intent for H. No platform offers an observation that decides this:

- Orphaned effect child: a pre-freeze Hand process reads the DB, starts `git -C <clone> merge --ff-only` or a Herdr client call, and exits. The child runs a non-Hand executable, can start after F, and is reparented. A Herdr client need not reference any path under H. v0.7.2 records no ancestry or intent registry that could recover the link.
- Identity: `hand update`, renamed binaries, deleted images, checkout builds, and test binaries defeat name and path matching. Linux can read the running image through `/proc/<pid>/exe`. macOS and Windows expose only a path, which may now name different bytes.
- Visibility: `hidepid` or `subset=pid` on `/proc`, a non-initial PID namespace, other users' processes, macOS System Integrity Protection, Windows protected processes, and non-elevated `OpenProcess` denial are all unknown. Fail-closed handling therefore refuses on any ordinary desktop.
- Races: a PID can be reused between enumeration and inspection. Linux can pin a process with pidfd and start time; macOS and Windows cannot without privileged handles.

The only sound rule is that every non-kernel process alive at F is a possible writer. That rule is never satisfiable on a general-purpose system. This revision therefore defines no process-scan witness on any platform. An implementation may print likely Hand processes as advice before the freeze; that output never authorizes a step.

## Drift gate

The gate runs after a positive witness, under MigrationLock, before the canonical build, and again on every resume:

- Projects: for every manifest Project, the re-observed canonical locator, repository physical identity, common-dir physical identity, and HEAD revision equal the manifest values exactly. A missing clone, a changed HEAD, or an alias blocks.
- Managed namespace and Treehouse: revision 2's Project and Treehouse quiescence, evaluated against the plan derived from the original archive, is positive. A same-Fleet lease, an unresolved or colliding pool slot, or a managed Project path without a manifest Project blocks.
- Herdr: revision 2's Herdr quiescence for that plan is positive. Any Hand workspace, tab, or pane for this Fleet blocks. Herdr unavailable or unclassifiable is unknown and blocks.
- A drift result is never repaired, re-baselined, or imported. A drifted frozen home stays frozen. This revision defines no thaw or re-observation path.

## New legacy processes after the freeze

F stops a new legacy process at its schema check. It does not stop work that runs before that check. A read-only audit of `v0.7.2` (`09b7c2b3d48458700fa5ef121f798530892ff072`) and `main` (`3812a730318a27bda794cc9a7e9f421a57d74413`), and runs of both builds against a minimal `user_version = 22` fixture, found:

- v0.7.2 runs no Git, Treehouse, Herdr, or `gh` subprocess, registry write, or clone change before the check. Its `PersistentPreRunE` can still:
  - E1: create the `config:routing` lock file under `state/`;
  - E2: rename `config/model` or `config/effort` to its harness-keyed name;
  - E3: query the GitHub releases API and write `state/.version-check`.
- v0.7.2 `hand help` performs E1–E3 and never checks the DB.
- On main, every ordinary command refuses a frozen bridge before any side effect under H. A main process that passed its check before F can still write `state/.version-check` after F.

E1–E3 touch no imported fact, no drift-gate subject, and not the frozen source, so the import needs no protection from them. A later change that lets a pre-check effect reach those subjects breaks this revision.

## Publication without an absent active DB

Revision 2 retires the frozen bridge and then publishes the canonical temp. Between those two renames `state/hand.db` is absent.

- Counterexample: a v0.7.2 or 0.8.0 legacy process opens H inside that window. SQLite creates a fresh `state/hand.db`, and the legacy schema step mints a new Fleet ID. No-replace publication then refuses, and the recovery matrix classifies the home as a valid fresh legacy source.

This revision replaces that order:

1. Durably link the frozen bridge to its deterministic retired path. The active name keeps pointing at the bridge.
2. Atomically replace `state/hand.db` with the validated canonical temp: `rename` on POSIX, a replace-existing write-through move on Windows. A Windows sharing violation stops the attempt and leaves the bridge active and retryable.
3. Flush the parent directory and revalidate as revision 2 requires.

After F, `state/hand.db` names either the frozen bridge or the validated canonical DB at every instant. An absent active DB after F comes only from pre-revision-4 code or outside action. Recovery publishes into it only with no-replace semantics and refuses if anything appears.

## Kernel scope

The witness covers processes on the kernel that runs the cutover. Both runs refuse when the filesystem that holds `state/hand.db` is remote or cannot be classified. Remote includes NFS, SMB/CIFS, 9p, virtiofs, FUSE network filesystems, and Windows remote drives.

A local filesystem that another kernel reaches through an export, a VM share, or a container host share cannot be detected from this side. This residual is unmitigated. The command's operator documentation must name it.

## Crash and lost-response outcomes

| Point | Durable state | Next run |
| --- | --- | --- |
| Before F | exact v0.7.2 source; candidate or original archive; manifest | new freeze attempt under revision 2's candidate/archive rules, with a new E_F |
| COMMIT outcome lost | v0.7.2 source or v2 bridge | read-only reclassification decides; a freeze never runs on a bridge |
| After F, before restart | v2 bridge | `reboot-required`; no change |
| Witness positive, crash in drift gate or canonical build | v2 bridge; maybe an invalid temp | witness and drift gate rerun; temp rebuilt |
| Drift found | v2 bridge | `drift`; stays frozen |
| After bridge link, before replace | active bridge plus retired link to the same inode | recovery requires the same inode, then replaces |
| Replace outcome lost | active bridge or canonical DB | a valid canonical DB wins; otherwise continue from the bridge |
| After replace | canonical DB | revision 2's post-publication rules |

## Invariants

Each invariant names a trace that falsifies it.

- C4-1: No canonical build for publication, bridge retirement, or publication happens unless the witness is positive against E_F committed in the same bridge, within the same MigrationLock hold. Falsified by publication from a bridge in F's boot session, from a v1 bridge, or with evidence read from a manifest.
- C4-2: No canonical publication happens unless the drift gate passed after that positive witness, within the same MigrationLock hold. Falsified by the revision-3 lock waiter fast-forwarding a clone after F and before the restart, followed by a successful publication.
- C4-3: An unreadable, malformed, cross-platform, or non-qualifying witness input causes a refusal with zero file changes. Falsified by any mutation under H after such an input.
- C4-4: After F, `state/hand.db` is never absent and is never replaced by anything except the validated canonical DB. Falsified by a legacy open during publication that creates a fresh legacy DB.
- C4-5: A legacy process started after F causes no side effect on an imported fact, a drift-gate subject, or the frozen source. Falsified by any pre-check Git, Treehouse, Herdr, clone, or DB write in v0.7.2 or in 0.8.0's legacy path.
- C4-6: Live cutover stays refused. Falsified by any path that freezes a source and publishes from it in the same boot session.
- C4-7: No path in this revision unfreezes or reverts a frozen bridge.

## Unknowns

- Cross-kernel access through exports and VM or container shares (see "Kernel scope").
- Process checkpoint/restore and VM memory snapshot restore.
- macOS `kern.bootsessionuuid` behavior and Windows Fast Startup behavior need native qualification. CI cannot provide it because both need a real restart.
- A stale v0.7.2 binary run against a published canonical home. Static reading shows its schema step fails at `attempt_one_active` (`no such column`) inside a transaction that rolls back. E1–E3 still run first, and E2 can rename `config/model` or `config/effort`. This needs a separate-process proof, and canonical configuration (#324) must not treat those legacy file names as authority.
- Resolving a drifted frozen home needs a later reviewed revision.

## Required tests

- Separate process: the revision-3 lock waiter acts after F. Completion then refuses with `reboot-required` in the same boot session. With a test-only evidence source that makes the witness positive, completion refuses on the resulting drift.
- Witness, per platform: equal, different, malformed, unreadable, and cross-platform tokens; on Windows, a tick count that decreased and one that did not.
- Freeze transaction: the v2 certificate, evidence row, 21 guards, and sentinel commit atomically. A crash before COMMIT leaves exact v0.7.2. Evidence carried in a manifest from an earlier attempt is ignored.
- v1 bridge: inspect recognizes it; recover and completion refuse it.
- Drift gate: changed HEAD, missing clone, new managed path, new same-Fleet lease, Hand Herdr pane, Herdr unavailable.
- Publication: `state/hand.db` is never absent; a concurrent legacy open during publication cannot mint a Fleet; a Windows sharing violation leaves the bridge active and retryable.
- 0.8.0: before F the legacy home keeps working; after F every ordinary command refuses the frozen bridge with zero changes under H.
- Filesystem classification refuses remote and unclassifiable filesystems.

## Remaining acceptance

This candidate defines the offline mechanism; it does not implement or qualify it. Implementation of every section above, native POSIX and Windows qualification, macOS and Windows restart qualification, final-head integration, and #305 exact-candidate qualification remain. No 0.8.0 release is authorized by this candidate.
