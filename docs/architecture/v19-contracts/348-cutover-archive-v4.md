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
2. Cessation of every process that could act on pre-freeze legacy intent is proven by the freeze plus a built-in boot-identity witness. In 0.8.0 a restart verified by that witness is the only accepted cessation witness. An operator assertion, a free lock, a stable digest, a delay, and a process scan are not that proof. The process scan is rejected on every platform on purpose (see "Process scan is not a witness").
3. After a positive witness and before any canonical build for publication, every imported Project fact and the Fleet's Treehouse and Herdr state are re-observed against the frozen evidence. Any drift or unknown blocks.
4. 0.8.0 keeps serving legacy v18 homes. The offline cutover is optional and starts only when the operator runs it.
5. Until publication starts, the operator can abort. Abort returns the home to the exact pre-freeze legacy DB, so legacy access resumes (see "Abort"). This is the resolution for drift.

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
- The boot witness proves that no process that was alive on this kernel at F is alive now. It observes kernel identity, not Hand.
- The drift gate proves that imported and quiescence-relevant external state equals the frozen evidence when the gate runs. Only after the witness is that result stable, because no pre-freeze writer remains to change it.

## 0.8.0 behavior for a legacy home

- 0.8.0 serves a legacy v18 home through its legacy commands until the operator runs the offline cutover.
- No startup path, `init`, `update`, read-only command, or ordinary mutating command begins, resumes, or completes a cutover.
- `hand init` on 0.8.0 keeps creating legacy-family homes. Refusing legacy homes and canonical-default `init` are outside this revision.
- After F, the frozen `user_version = 22` refuses legacy access until publication or abort. v0.7.2 and 0.8.0's legacy path both fail it as newer than supported before any legacy DML. After abort, legacy access resumes on the exact pre-freeze DB.
- 0.8.0 refuses a frozen bridge before any side effect under H. Its refusal names the pending cutover instead of advising an upgrade.
- Only the offline cutover command, its `--abort` form, and `hand cutover recover` act on a frozen bridge.

## Offline sequence

The operator runs one command twice, with a restart of the machine between the two runs.

Freeze run:

1. Preflight, before MigrationLock and before any archive, manifest, or source change:
   - Refuse unless the filesystem that holds `state/hand.db` is classified local (see "Kernel scope").
   - Probe `state/`: create probe file A and hard-link it as B; create probe file C and atomically rename it over B, replacing it; confirm that B then has C's file identity and A keeps its own; then remove all probe names. Each probe name carries 128 random bits and is created no-replace, because two runs can overlap before MigrationLock. A collision or any failed step refuses. Publication step 1 and abort step 5 depend on hard links, and every replacement depends on replace-existing rename. A local filesystem without them, such as FAT or exFAT, is refused here and never frozen.
   - Read the freeze boot evidence E_F for this platform (see "Boot witness"). Unreadable or malformed evidence, a machine identity that is not persistent, or a Windows uptime below the floor refuses. This one read is the E_F that the freeze transaction commits.
2. Acquire MigrationLock.
3. Run revision 2's gate, nonblocking Fleet-local lock closure, durable-state classification, and provider/resource quiescence. Here they are preconditions for a consistent snapshot, not cessation proof. A busy or unknown result still refuses.
4. Promote the original archive and persist the pre-freeze manifest as revision 2 requires. Manifest format v2 records revision 2's manifest facts, with every Project physical identity in the restart-stable form (see "Drift gate"). It records the certificate version but neither the certificate value nor any digest of itself; the certificate binds the manifest's digest. A Project whose restart-stable identity is unavailable refuses the freeze.
5. In the one freeze transaction on OUR EXCLUSIVE connection:
   - require that no `meta` key starting with `v19-cutover-` exists;
   - insert `meta.key = 'v19-cutover-freeze'` with value `v2:<source-sha256>:<manifest-sha256>:<evidence-sha256>`;
   - insert `meta.key = 'v19-cutover-freeze-evidence'` whose value is exactly the E_F bytes hashed into the certificate;
   - create the 21 revision-2 guards;
   - set `user_version = 22`;
   - COMMIT.
   Every digest is a lowercase 64-hex SHA-256.
6. Release locks and handles as revision 2 requires. Perform no provider, Git, registry, or network action after F. Report that a full restart is required before completion and, on Windows, the completion window.

Completion run, after the restart:

1. Refuse unless the filesystem is classified local.
2. Acquire MigrationLock.
3. Recognize the exact v2 bridge: revision 2's recognition rules, exactly the two `v19-cutover-` `meta` rows above, a matching original archive and manifest, and no abort record for it.
4. Evaluate the witness against E_F read from that bridge. A negative result is `reboot-required`; an unknown result is `cessation-unknown`. Both are retryable and leave every file unchanged.
5. Run the drift gate. It returns `pass`, `observation-unknown`, or `drift`, naming each subject. `observation-unknown` is retryable. `drift` means a frozen fact no longer holds, and only abort resolves it. Neither changes any file.
6. Build and validate the canonical temp from the original archive and import plan, as revision 2 requires.
7. Publish by atomic replacement (see "Publication without an absent active DB").
8. Continue with revision 2's post-publication validation and registry projection.

Every path that builds for publication, retires, or publishes from a frozen bridge runs steps 4 and 5 first within the same MigrationLock hold. This includes `hand cutover recover`. No witness or drift result is cached across runs.

## Freeze evidence

- E_F is authoritative only as committed inside the freeze transaction. A copy in a manifest, marker, log, or earlier attempt never counts.
  - Counterexample: attempt 1 writes the manifest in boot session B1 and crashes before F. The machine restarts. Attempt 2 reuses the persisted manifest and commits F in B2. Evidence taken from the manifest would name B1, so the witness would pass with no restart after F.
- The freezing process reads E_F once, in preflight, and commits that read. The process cannot survive a restart, so the committed E_F always names the boot session of F. No re-read is compared, because wall time and the Windows tick count differ between any two reads. On Windows the preflight tick count precedes F and is no larger than the count at F, so the decrease rule stays sound.
- E_F contains: evidence format `v1`; platform (`linux`, `darwin`, or `windows`); machine identity; token kind; token value; an informational UTC wall time. Wall time never decides an outcome.
- Machine identity is `/etc/machine-id` on Linux, `gethostuuid(2)` on macOS, and `MachineGuid` under `HKLM\SOFTWARE\Microsoft\Cryptography` on Windows.
- A freeze refuses when the machine identity is unreadable or not persistent across restarts. On Linux that covers a missing, empty, or `uninitialized` `/etc/machine-id`, a value that is not 32 lowercase hex digits, and a file that is a mount point or lives on tmpfs or ramfs. systemd regenerates such an identity at every boot, often bind-mounting it from `/run`. Accepting it would make every completion `cessation-unknown`.
- The command's operator documentation must say that a container or image without a persistent machine identity cannot run the offline cutover.
- At completion a different or unreadable machine identity is `cessation-unknown`. A home moved to another machine is never completed there; abort is its way back.
- The certificate binds the manifest digest, so the drift baseline is unique. A manifest whose digest differs from the certificate refuses.
- A `v1:<source-sha256>` bridge has no E_F and never satisfies the witness. Read-only inspection still recognizes it; recovery refuses to build from it or publish it. No command in any released or `main` build ever called the freeze; only tests did, so no production home holds a v1 bridge.

## Boot witness

The witness is positive only when current evidence is read positively, on the platform and machine recorded in E_F, and meets that platform's rule. Every read failure, malformed value, platform mismatch, and machine mismatch is unknown and refuses.

| Platform | Token | Positive when | Limits |
| --- | --- | --- | --- |
| Linux | `/proc/sys/kernel/random/boot_id` | current UUID differs from recorded UUID | This is the host kernel's identity. A container restart is not a machine restart, so completion refuses. Hibernate and resume keep the ID and the processes. |
| macOS | `sysctl kern.bootsessionuuid` | current UUID differs from recorded UUID | Apple does not formally document this interface. Qualification must show it stays stable across sleep and changes across restart on each supported major version. `kern.boottime` may be recorded for diagnostics only, because the kernel can adjust it when the clock is set. |
| Windows | milliseconds since boot from `GetTickCount64` | current value is less than recorded value | The freeze refuses when uptime is below one hour, so every completion window after a restart is at least one hour. Completion refuses once uptime since the restart reaches the recorded uptime; another restart reopens the window, so a missed window never strands the home. The freeze report states the bound. Shutdown with Fast Startup resumes a hibernated kernel and is expected not to reset the count, so a full Restart is required. Wall-clock boot time is rejected because clock changes move it. |

The witness assumes that no process survives a change of boot session. Process checkpoint/restore (for example CRIU) and VM memory snapshot restore are outside the model.

## Process scan is not a witness

A fail-closed scan must treat process P as a possible writer unless it proves that P holds no pre-freeze legacy intent for H. No platform offers an observation that decides this:

- Orphaned effect child: a pre-freeze Hand process reads the DB, starts `git -C <clone> merge --ff-only` or a Herdr client call, and exits. The child runs a non-Hand executable, can start after F, and is reparented. A Herdr client need not reference any path under H. v0.7.2 records no ancestry or intent registry that could recover the link.
- Identity: `hand update`, renamed binaries, deleted images, checkout builds, and test binaries defeat name and path matching. Linux can read the running image through `/proc/<pid>/exe`. macOS and Windows expose only a path, which may now name different bytes.
- Visibility: `hidepid` or `subset=pid` on `/proc`, a non-initial PID namespace, other users' processes, macOS System Integrity Protection, Windows protected processes, and non-elevated `OpenProcess` denial are all unknown. Fail-closed handling therefore refuses on any ordinary desktop.
- Races: a PID can be reused between enumeration and inspection. Linux can pin a process with pidfd and start time; macOS and Windows cannot without privileged handles.

The only sound rule is that every non-kernel process alive at F is a possible writer. That rule is never satisfiable on a general-purpose system. This revision therefore defines no process-scan witness on any platform, and a restart verified by the boot witness is the only cessation witness 0.8.0 accepts. The rejection is deliberate, not deferred work. An implementation may print likely Hand processes as advice before the freeze; that output never authorizes a step.

## Drift gate

The gate runs after a positive witness, under MigrationLock, before the canonical build, and again on every resume. It separates two refusals:

- `observation-unknown`: an observation could not be made or classified, or a resource the operator can stop is live. It is retryable and never requires abort.
- `drift`: a fact recorded at F no longer holds. Only abort resolves it.

Physical identity, as recorded in manifest v2 and compared here, must stay stable across the required restart and must distinguish a different object reallocated at the same path:

- POSIX: `unix-v2`, the inode number plus the file birth time (Linux `statx` `STATX_BTIME`; macOS `st_birthtimespec`). The device number is excluded because btrfs, ZFS, LVM, device-mapper, and disk reordering can renumber it at boot. `statfs` `f_fsid` is excluded because XFS derives it from the device number.
- Windows: volume serial number plus file index.
- A `unix-v1` identity (device and inode) is never compared across a restart.
- Birth time or file index unavailable at completion is `observation-unknown`, not drift.
- Canonical v19 uses the same form. The import copies the manifest's repository and common-dir identity digests into `workspace_binding`. Canonical Project registration and Plan git-basis verification recompute those identities under the same digest domains, so they must recompute this restart-stable form. A canonical consumer that recomputes `unix-v1` rejects every imported Project after publication, and after any device renumbering. Digests of different identity forms are never treated as equal.

Subjects:

- Projects: for every manifest Project, the re-observed canonical locator, repository physical identity, common-dir physical identity, and HEAD revision must equal the manifest values exactly. A difference, a missing clone, or an alias is `drift`. An unreadable clone or identity is `observation-unknown`.
- Managed namespace and Treehouse: revision 2's Project and Treehouse quiescence is evaluated against the plan derived from the original archive. A same-Fleet lease, an unresolved or colliding pool slot, or a managed Project path without a manifest Project is `drift`. Treehouse unavailable or unclassifiable is `observation-unknown`.
- Herdr: after a positive witness, the restart has killed every Herdr server and pane that existed at F. Herdr identities recorded before F belong to a dead server instance and are never matched.
  - A Herdr server positively observed as stopped for both the `hand-<fleet_id>` and `default` sessions proves that no pane of this Fleet exists; that passes.
  - In a running server, a resource belongs to this Fleet when it is in session `hand-<fleet_id>` or carries a label starting `hand:<fleet_id>:`. A live resource of this Fleet is `observation-unknown`: this Fleet's legacy commands refuse before any Herdr call after F, so such a resource was created or restored after the restart, and closing it resolves the refusal.
  - A label `hand:<other_fleet_id>:...` belongs to another Fleet and passes.
  - The v0.7.2 label `hand:<project>` in `default` carries no Fleet ID. When `<project>` names no manifest Project, it passes. When it does, it is `observation-unknown` in this revision, because Herdr's workspace-restore behavior across a server restart is not established. The workspace may belong to another Fleet, so the operator must not close it (#519 cross-Fleet ownership). The exits are waiting until that Fleet no longer holds it, or abort. Only a later #348 revision that states Herdr's restore behavior as its own evidence can make this label pass.
  - A Herdr client that cannot reach or classify a server is `observation-unknown`.
- A drift result is never repaired, re-baselined, or imported. A drifted frozen home stays frozen until the operator aborts.

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
2. Atomically replace `state/hand.db` with the validated canonical temp: `rename` on POSIX, a replace-existing write-through move on Windows. On Windows, `ERROR_SHARING_VIOLATION` and `ERROR_ACCESS_DENIED` both stop the attempt and leave the bridge active and retryable.
3. Flush the parent directory and revalidate as revision 2 requires.

After F, the path `state/hand.db` names the frozen bridge, the validated canonical DB, or, after abort, the restored pre-freeze DB at every instant. A POSIX handle opened before a replacement keeps its old file. A pre-freeze process holding a bridge handle keeps reading the bridge, and the guards refuse its writes.

After F, an absent active DB refuses in every run: completion, `hand cutover recover`, and abort. Revision 2's "active missing" recovery rows no longer publish.

- Counterexample: a v2 freeze commits in boot session B1, and something outside Hand moves `state/hand.db` away. `hand cutover recover` in B1 takes revision 2's "active missing + archive + temp" row and publishes in F's boot session. A pre-read lock waiter then fast-forwards the clone.

The operator restores the bridge at `state/hand.db`; then the normal rules apply.

Between a replacement and its parent-directory flush, other processes can already open the new file. A power loss in that window can revert the name and lose writes they made. Publication and abort flush immediately, which bounds the window but does not close it. After such a revert, the recovery rules above apply to whatever the path names.

Replacement relies on the guards. A stale connection on the bridge inode cannot write a page, so it cannot leave a hot journal at `state/hand.db-journal` for SQLite to pair with the replacement file. Publication, like abort, refuses when a non-empty `-journal` or any `-wal` exists at replacement time.

## Abort

`hand cutover offline --abort <home>` reverses a freeze before publication starts. Publication starts at the atomic replacement of `state/hand.db` by the canonical DB; that is the first publish mutation. Until then the active DB is still the frozen bridge. The canonical temp and the retired-bridge link from publication step 1 are not publish mutations.

Abort needs no witness. It restores the exact pre-freeze DB. No legacy write landed between archive promotion and F, and none could land after F. A surviving pre-freeze writer that resumes therefore sees the state it would have seen with no freeze.

Start state, classified under MigrationLock:

| Active `state/hand.db` | Abort record | Outcome |
| --- | --- | --- |
| exact frozen bridge (v1 or v2) with a matching original archive | any | abort runs or continues |
| exact v0.7.2 | any | repeat only the parent-directory flush; report `not-frozen`, naming any abort record |
| canonical, absent, or anything else | any | refuse; a canonical DB follows revision 2's recovery rules, and an absent one follows "Publication without an absent active DB" |

Sequence:

1. Acquire MigrationLock.
2. Classify the start state as above.
3. Re-hash the original archive. Require that it equals the certificate source digest.
4. Refuse if a non-empty `state/hand.db-journal` or any `state/hand.db-wal` exists.
5. Create the abort record:
   - Create a new directory `state/v19-cutover-<migration_id>/aborted-<nonce>` no-replace, where the nonce is 128 random bits.
   - Durably link the active bridge into it, no-replace. This link is the abort commit point: from here, completion and recover refuse for this bridge, and only abort continues.
   - On resume, the record is the `aborted-*` directory whose bridge link has the same file identity as the active bridge. A record directory without a bridge link is non-authoritative, and abort removes it by exact path.
6. Durably move the frozen manifest into the abort record. Remove the canonical temp, and any retired-bridge link with the same file identity, by exact path.
7. Copy the original archive to a same-volume, non-authoritative restore candidate beside `state/hand.db`. Flush it, reopen it, and require its SHA-256 to equal the certificate source digest.
8. Atomically replace `state/hand.db` with the restore candidate, using the primitives from "Publication without an absent active DB". On Windows, `ERROR_SHARING_VIOLATION` and `ERROR_ACCESS_DENIED` stop the attempt. The bridge stays active and abort remains the only continuation.
9. Flush the parent directory. Reopen read-only and require exact v0.7.2 validation, with a SHA-256 equal to the certificate source digest.

Record and scope:

- The abort record holds the unmodified bridge, including its certificate and E_F, and the frozen manifest. It is immutable evidence that the abort happened.
- No witness, drift gate, recovery classification, or later cutover reads the abort record as input. Inspection may report that it exists.
- The original archive stays in place. A later offline cutover may reuse it only under revision 2's exact-digest rule. That cutover writes a fresh manifest and commits a fresh E_F.
- Abort performs no Git, Treehouse, Herdr, registry, or network action.
- Abort does not reconcile external effects that a pre-freeze writer made while the source was frozen. Those effects are legacy residuals whose DB record was refused, the same as a crashed legacy command. Legacy repair owns them.

## Residuals after publication

The drift gate checks imported facts and quiescence, not every effect. A pre-freeze writer may have acted while the source was frozen and had its DB record refused. Examples: `gh` calls, `git push`, `git branch -D`, a `.git/info/exclude` write, or `project sync` changing a clone's `origin` URL mid-repoint. After publication, each of these remains as external state that neither the archive nor canonical v19 records, the same as the residue of a crashed legacy command. Canonical v19 imports no Task, branch, PR, or remote history, so it asserts nothing these effects contradict. Canonical Project policy derived from the archived legacy URL can disagree with such a clone's `origin` until canonical repair observes it.

## Kernel scope

The witness covers processes on the kernel that runs the cutover. Both runs refuse when the filesystem that holds `state/hand.db` is remote or cannot be classified. Remote includes NFS, SMB/CIFS, 9p, virtiofs, FUSE network filesystems, and Windows remote drives.

A local filesystem that another kernel reaches through an export, a VM share, or a container host share cannot be detected from this side. This residual is unmitigated. The command's operator documentation must name it.

## Crash and lost-response outcomes

| Point | Durable state | Next run |
| --- | --- | --- |
| Preflight refusal | exact v0.7.2 source; probe files removed | fix the cause and rerun |
| Before F | exact v0.7.2 source; candidate or original archive; manifest | new freeze attempt under revision 2's candidate/archive rules, with a new E_F |
| COMMIT outcome lost | v0.7.2 source or v2 bridge | read-only reclassification decides; a freeze never runs on a bridge |
| After F, before restart | v2 bridge | `reboot-required`; no change |
| Witness positive, crash in drift gate or canonical build | v2 bridge; maybe an invalid temp | witness and drift gate rerun; temp rebuilt |
| Drift gate unknown | v2 bridge | `observation-unknown`; rerun after the cause is fixed |
| Drift found | v2 bridge | `drift`; stays frozen until abort |
| Active DB absent after F | archive; maybe temp | every run refuses until the bridge is restored at `state/hand.db` |
| After bridge link, before replace | active bridge plus retired link to the same inode | recovery requires the same inode, then replaces |
| Replace outcome lost | active bridge or canonical DB | a valid canonical DB wins; otherwise continue from the bridge |
| After replace | canonical DB | revision 2's post-publication rules; abort refuses |
| Abort before its record link | frozen bridge | abort restarts; completion is still allowed |
| Abort after its record link, before replace | frozen bridge plus abort record | only abort continues; completion and recover refuse |
| Abort replace outcome lost | frozen bridge or restored v0.7.2, plus abort record | continue from the bridge, or repeat the flush and report `not-frozen` |
| After abort | exact v0.7.2 equal to the original archive | legacy access; a later cutover starts fresh |

## Invariants

Each invariant names a trace that falsifies it.

- C4-1: No canonical build for publication, bridge retirement, or publication happens unless the witness is positive against E_F committed in the same bridge, within the same MigrationLock hold. Falsified by publication from a bridge in F's boot session, from a v1 bridge, or with evidence read from a manifest.
- C4-2: No canonical publication happens unless the drift gate passed after that positive witness, within the same MigrationLock hold. Falsified by the revision-3 lock waiter fast-forwarding a clone after F and before the restart, followed by a successful publication.
- C4-3: An unreadable, malformed, cross-platform, cross-machine, or non-qualifying witness input causes a refusal with zero file changes in completion and recover. In the freeze run, such an input refuses in preflight, before MigrationLock, and leaves only the removed probe files. Falsified by any other mutation under H after such an input.
- C4-4: After F, no Hand path makes `state/hand.db` absent, and no Hand path publishes into an absent `state/hand.db`. Hand replaces it only with the validated canonical DB or abort's restored pre-freeze DB. Falsified by a legacy open during publication or abort that creates a fresh legacy DB, or by any run that publishes after the active DB was moved away.
- C4-5: A legacy process started after F causes no side effect on an imported fact, a drift-gate subject, or the frozen source. Falsified by any pre-check Git, Treehouse, Herdr, clone, or DB write in v0.7.2 or in 0.8.0's legacy path.
- C4-6: Live cutover stays refused. Falsified by any path that freezes a source and publishes from it in the same boot session.
- C4-7: Abort never runs after the first publish mutation. It acts only while `state/hand.db` is the exact frozen bridge. Falsified by an abort that changes a canonical, absent, or unknown active DB.
- C4-8: Once an abort record exists for a bridge, no witness evaluation, drift gate, canonical build, or publication runs for that bridge. Falsified by a publication after abort's commit point.
- C4-9: Abort is safe to repeat from every crash point, including a crash in the freeze run. Before F it changes nothing. After F each rerun resumes from durable state and converges on the same restored bytes. Falsified by a rerun that writes different bytes or refuses permanently from a state abort itself produced.
- C4-10: After abort, the bytes of `state/hand.db` equal the original archive, whose SHA-256 equals the certificate source digest. A legacy writer that resumes sees exactly the pre-freeze DB. Falsified by any byte difference, or by an abort record that a later decision reads as input.
- C4-11: Unfreezing happens only through abort's replacement with the original archive. No path drops the guards, rewrites the sentinel, or edits the bridge in place.
- C4-12: A physical-identity comparison across the restart never reports drift for the same object and never passes for a different one. Falsified by a device renumbering that yields `drift`, or by an inode reused at the same path that yields `pass`.
- C4-13: `observation-unknown` never requires abort, and `drift` is never retried into `pass` without an abort and a new freeze. Falsified by a stopped Herdr server after a positive witness that blocks, or by a drifted bridge that later publishes.
- C4-14: Imported WorkspaceBinding identities verify under canonical v19 after publication. Falsified by an imported Project that canonical Project registration or Plan git-basis verification rejects because it recomputes a different identity form.

## Unknowns

- Cross-kernel access through exports and VM or container shares (see "Kernel scope").
- Process checkpoint/restore and VM memory snapshot restore.
- macOS `kern.bootsessionuuid` behavior and Windows Fast Startup behavior need native qualification. CI cannot provide it because both need a real restart.
- Whether Herdr restores workspaces across a server restart is not established. Until a later revision states it, a legacy `hand:<project>` label naming a manifest Project stays `observation-unknown` (see "Drift gate").
- Birth-time availability varies by filesystem (for example, ext4 with 128-byte inodes has none). Such a Project cannot be frozen.
- A stale v0.7.2 binary run against a published canonical home. Static reading shows its schema step fails at `attempt_one_active` (`no such column`) inside a transaction that rolls back. E1–E3 still run first, and E2 can rename `config/model` or `config/effort`. This needs a separate-process proof, and canonical configuration (#324) must not treat those legacy file names as authority.

## Required tests

- Separate process: the revision-3 lock waiter acts after F. Completion then refuses with `reboot-required` in the same boot session. With a test-only evidence source that makes the witness positive, completion refuses on the resulting drift.
- Witness, per platform: equal, different, malformed, unreadable, and cross-platform tokens; on Windows, a tick count that decreased and one that did not.
- Freeze transaction: the v2 certificate, evidence row, 21 guards, and sentinel commit atomically. A crash before COMMIT leaves exact v0.7.2.
- Retried freeze across a restart: attempt 1 crashes after manifest persistence. The machine restarts, and attempt 2 commits F. Its committed E_F names the second boot session, and completion in that session refuses `reboot-required`.
- Preflight: a filesystem without hard links or replace-existing rename refuses before MigrationLock with no archive, manifest, or source change. A Windows uptime below the floor refuses. An unreadable machine identity refuses, and so does an empty, `uninitialized`, mount-point, or tmpfs `/etc/machine-id`. Two overlapping preflights do not collide on probe names. A Windows freeze succeeds even though its tick count and wall time advance between reads.
- Canonical identity: after publication, canonical Project registration and Plan git-basis verification accept every imported Project, including after a device renumbering.
- Absent active DB after F: completion, recover, and abort all refuse.
- Recover on a v2 bridge in F's boot session refuses `reboot-required`.
- Machine identity differs at completion: `cessation-unknown`.
- v1 bridge: inspect recognizes it; recover and completion refuse it.
- Drift gate:
  - `drift` for a changed HEAD, a missing clone, a new managed path, a new same-Fleet lease, and a different inode or birth time at the same path.
  - `pass` when the device number changes but the filesystem object is the same.
  - `pass` for a stopped Herdr server after a positive witness.
  - `pass` for another Fleet's `hand:<fleet_id>:` workspace, and for a legacy `hand:<project>` label naming no manifest Project.
  - `observation-unknown` for a live resource of this Fleet, for Herdr or Treehouse unreachable, and for birth time unavailable.
  - `observation-unknown` for a legacy `hand:<project>` label naming a manifest Project, with no Herdr mutation.
- Publication: `state/hand.db` is never absent; a concurrent legacy open during publication cannot mint a Fleet; a Windows sharing violation or access denial leaves the bridge active and retryable.
- 0.8.0: before F the legacy home keeps working; after F every ordinary command refuses the frozen bridge with zero changes under H.
- Filesystem classification refuses remote and unclassifiable filesystems.
- Abort:
  - A crash at each abort step, with a rerun that converges.
  - Abort before F reports `not-frozen` with zero changes.
  - Abort after the canonical replace refuses.
  - Completion and recover refuse after the abort record link.
  - An empty `aborted-*` directory is removed and a fresh record is created.
  - A Windows sharing violation or access denial leaves the bridge active and abort retryable.
  - The restored DB's bytes equal the original archive, and a pre-freeze lock waiter that resumes after abort reads the pre-freeze rows.
  - A stale connection on the bridge inode cannot leave a journal that pairs with the restored file.

## Remaining acceptance

This candidate defines the offline mechanism; it does not implement or qualify it. Implementation of every section above, native POSIX and Windows qualification, macOS and Windows restart qualification, final-head integration, and #305 exact-candidate qualification remain. No 0.8.0 release is authorized by this candidate.
