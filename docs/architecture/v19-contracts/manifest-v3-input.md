# v19 semantic contract manifest — revision 3 deterministic input

This file is deterministic input for the permanent revision-3 manifest anchor follow-up. It is not the current manifest and does not claim a permanent `main` revision. Until that follow-up lands, `docs/architecture/v19-contracts/manifest-v2.md` and its permanent anchor remain current.

Immutable content revision containing every candidate snapshot and revision-2 #344 artifact:

```text
427738405f00215f5b3970463c482264c1c0e533
```

The follow-up must create a new versioned manifest, preserve every prior manifest/snapshot/artifact byte, and name the permanent merged `main` revision that contains this content revision. No placeholder anchor is permitted.

## Revision-2 #344 relational input

```text
content commit: 427738405f00215f5b3970463c482264c1c0e533
DDL: docs/architecture/v19-v2.sql.gz
Git blob: 6f2d81abddc1f565c3f6e9f7d48e2015ce4383a1
stored gzip bytes: 11612
stored gzip SHA-256: 19d03a1476ff6b7d3f6131d094a606ad0067f08f47541adf1d581dc6249d8bf4
reconstructed DDL bytes: 109144
reconstructed DDL SHA-256: 2af1fb976f0ef53fe067930773577e52a7cff66035c309d93c7e59972e9fa56e
schema fingerprint: 955afb83a0fd1703778728621ba658848af87990fa746f0de0013d163e9157e9
schema-defined objects: 56 tables / 38 explicit indexes / 172 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v2.py.gz
proof Git blob: 0a4832efa9d9d65182d2edd8e614b9dcbda6e38b
proof stored gzip bytes: 10873
proof stored gzip SHA-256: 7fa953c09a724348fb3e603d8c8d5148477acc05e4bc5c955df744402e05b9ad
proof reconstructed bytes: 55241
proof reconstructed SHA-256: 55900aa18843a21328afa035282edc7eee167f590d61c9a456fb939cd711f68f
relock: docs/architecture/v19-relock-v2.md
relock Git blob: 504c258c69d218b0420e0428e42dabbc951d0997
```

## Revision-3 snapshot set input

Use this exact issue order:

| Issue | Title / contract | Path | Git blob SHA-1 |
| ---: | --- | --- | --- |
| #304 | Decision / Answer authority | `docs/architecture/v19-contracts/304-decision-answer-authority.md` | `d7aedff121a8ca81333ee61febf0618112f83b2a` |
| #323 | Worker Harness routing / typed fallback | `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| #324 | typed v1 configuration / role separation | `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| #343 | external effects / WorkerWake / reconciliation | `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| #345 | lifecycle / currentness / concurrency / Task archive — revision 2 | `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v2.md` | `ba03c1d94f00f109f716bf86768f7cf26efbd36b` |
| #346 | capability / adapter boundaries | `docs/architecture/v19-contracts/346-capability-adapters.md` | `859b80207a625fb4be8f5ff1a5eaf336bb7e8c77` |
| #347 | read models / WorkerReport witness / Task archive — revision 2 | `docs/architecture/v19-contracts/347-read-models-attention-orientation-v2.md` | `04a8654c7fa6c2d898c28a8588de86f84f1b4b36` |
| #348 | v18→v19 cutover / archive / non-fabrication — revision 2 | `docs/architecture/v19-contracts/348-cutover-archive-v2.md` | `94c018d0bce8c2427d01b9af89e513cfb5f3ce96` |
| #497 | no first-class soft-turn-cancel | `docs/architecture/v19-contracts/497-no-soft-turn-cancel.md` | `5be1875efa61b4c4f68f988156fb3c4d746b0ebb` |
| #519 | user-global runtime generation ownership | `docs/architecture/v19-contracts/519-user-global-runtime-generations.md` | `c712c65dad085103dcc7752a8c09a31b62c82711` |

Git blob SHA-1 is the deterministic content digest for each repository object. The contract-set digest is SHA-256 over the UTF-8 concatenation, in the table's issue order, of:

```text
<basename> NUL <git-blob-sha1> LF
```

Contract-set SHA-256:

```text
7a2d496ef6c7e46950ab0d390d5dba06c1c46cf51bda88e33f90dade32b43e84
```

## Supersession input

- Revision-3 #345 supersedes the original #345 blob `1039bb9fe69fb84f4b2357c05e6dcbfc6dfb3254` without changing its bytes.
- Revision-3 #347 supersedes the original #347 blob `72a9905c84e376b8cd5a07ff2c0643b244e97d14` without changing its bytes.
- Revision-2 #348 remains unchanged at blob `94c018d0bce8c2427d01b9af89e513cfb5f3ce96`.
- #304, #497, and #519 receive their first repository semantic snapshots.
- Revision-2 #344 supersedes the original DDL/proof/relock content without changing or removing the historical artifacts.

The follow-up manifest must record that exact schema fingerprint, not numeric `user_version`, defines compatibility. It must also record that final native/release qualification remains outstanding after permanent anchor publication.
