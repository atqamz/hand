# v19 semantic contract manifest — revision 3 deterministic input

This file is deterministic input for the permanent revision-3 manifest anchor follow-up. It is not the current manifest and does not claim a permanent `main` revision. Until that follow-up lands, `docs/architecture/v19-contracts/manifest-v2.md` and its permanent anchor remain current.

Immutable content revision containing every candidate snapshot and revision-2 #344 artifact:

```text
4f0bb2a0e4e3c7db62cc23dc2cdaee35fbafa3de
```

The follow-up must create a new versioned manifest, preserve every prior manifest/snapshot/artifact byte, and name the permanent merged `main` revision that contains this content revision. No placeholder anchor is permitted.

## Revision-2 #344 relational input

```text
content commit: 4f0bb2a0e4e3c7db62cc23dc2cdaee35fbafa3de
DDL: docs/architecture/v19-v2.sql.gz
Git blob: 10361016d6c6372e873d70b73ae13a9764297531
stored gzip bytes: 11604
stored gzip SHA-256: f4527132e7db93d0b527514b7418606be13d3d47565359dd5a6a754a56e2f7e2
reconstructed DDL bytes: 109107
reconstructed DDL SHA-256: d4a3d0adf3a84cc56002a1b558387d2169cb771b484addcb0fd8af743e19f16c
schema fingerprint: b3ba6d26db1f5aa520f248249b2e3679361283301484f7ea7c0f69ddd514e7f0
schema-defined objects: 56 tables / 38 explicit indexes / 172 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v2.py.gz
proof Git blob: 4a067c7df1d100a13e718091380cb310fb69c9de
proof stored gzip bytes: 10831
proof stored gzip SHA-256: 88b52de5581b4d41f4f554c709cfb365da447fc7da266960b0df63c55183e876
proof reconstructed bytes: 54900
proof reconstructed SHA-256: 7045446f4cbb71bc8a4faa92faf58db9329d059b38ce1aea1065f9e488dd202c
relock: docs/architecture/v19-relock-v2.md
relock Git blob: 33a1d7cab18565829eecad4e121b80f8f7229dec
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
