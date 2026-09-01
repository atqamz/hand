# v19 semantic contract manifest

This manifest anchors the repository-owned semantic contract snapshots used by the v0.8.0 / canonical v19 implementation.

Snapshot revision containing the complete contract set, before this manifest commit:

```text
4cbc97490b6a793b3cc4abcd890f504496f40960
```

The GitHub issue bodies remain tracker/review surfaces. For the snapshots listed below, the repository file at the snapshot revision is the immutable semantic evidence. A later issue-body edit cannot retroactively change that evidence. Comments are never normative architecture state.

`#344` is intentionally **not duplicated** here. Exact canonical relational authority remains its existing immutable artifact mechanism:

```text
immutable commit: 67ca4b35b773ef25ac9ff88cd1b16213153ed498
DDL: docs/architecture/v19.sql.gz
Git blob: d529d7a687db8d0266d5592ade9520c04766bf43
stored gzip SHA-256: 6e92cb72ad52c135a0cb8ae8f6352f1ff2c938a289ae1e313a9ce1d6a9e42399
reconstructed DDL SHA-256: 81118c2e982be7e08c0f8bf3bbb980e2ec4c5bffbbc7d419e9952732ad36c58a
schema fingerprint: 8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8
proof: docs/architecture/v19-proof.py.gz
relock manifest: docs/architecture/v19-relock.md
```

`#304` is also intentionally not replaced by this manifest. Its issue body is the sole current semantic statement for Decision/Answer authority; its historical comment is audit history only. If #304 is later moved to a repository snapshot, that must be an explicit authority transfer rather than an accidental second source.

## Snapshot set

| Issue | Title / contract | Path | Git blob SHA-1 |
| ---: | --- | --- | --- |
| #323 | Worker Harness routing / typed fallback | `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| #324 | typed v1 configuration / role separation | `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| #343 | external effects / WorkerWake / reconciliation | `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| #345 | lifecycle / currentness / concurrency / crash recovery | `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery.md` | `1039bb9fe69fb84f4b2357c05e6dcbfc6dfb3254` |
| #346 | capability / adapter boundaries | `docs/architecture/v19-contracts/346-capability-adapters.md` | `859b80207a625fb4be8f5ff1a5eaf336bb7e8c77` |
| #347 | read models / Attention / SupervisorOrientation | `docs/architecture/v19-contracts/347-read-models-attention-orientation.md` | `72a9905c84e376b8cd5a07ff2c0643b244e97d14` |
| #348 | v18→v19 cutover / archive / non-fabrication | `docs/architecture/v19-contracts/348-cutover-archive.md` | `c5c8045698ca8bea3b093d1adbe13d0b87c651d0` |

Git blob SHA-1 is the deterministic content digest for each repository object. The contract-set digest below is SHA-256 over the UTF-8 concatenation, in the table's issue order, of:

```text
<basename> NUL <git-blob-sha1> LF
```

Contract-set SHA-256:

```text
dff7a465722683c11f7c2c0df2970af452c0693c6b9501d49fa45cf91c2f3c36
```

## Authority graph

Use these categories precisely:

1. **Current normative architecture**: this snapshot set, #344's exact immutable DDL/proof artifact, and #304's current issue body for Decision/Answer authority.
2. **Historical landed prerequisites**: closed work such as #338 LaunchSpec, #340 monitoring/stale-wake safety, and #353/#355 Supervisor runtime integration where the required invariant is already consumed/restated by current v19 contracts. They remain audit evidence; they do not compete as current v19 authority.
3. **Implementation dependencies**: open implementation slices whose completion is required by sequencing but which do not define architecture merely by blocking another issue.
4. **Release qualification evidence**: #305 qualifies one exact release candidate/tag/source/runtime matrix after implementation; passing it does not redefine architecture.

A semantic cross-reference does not imply a sequencing dependency. A historical prerequisite does not become current normative authority merely because current contracts cite it.

## Change policy

Do not edit a frozen snapshot in place and pretend the old evidence changed. A semantic change requires a new versioned snapshot set and manifest anchor. If the change affects canonical table/constraint/trigger/index/operation-kind semantics or any byte of the #344 DDL, the #344 relock discipline is mandatory before implementation continues.

Documentation-only authority cleanup that does not change the frozen semantics or #344 bytes has DDL impact: **NONE**.