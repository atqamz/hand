# v19 semantic contract manifest — revision 4 deterministic input

This file is deterministic input for the permanent revision-4 manifest anchor follow-up. It is not the current manifest and does not claim a permanent `main` revision. Until that follow-up lands, `docs/architecture/v19-contracts/manifest-v3.md` and its permanent anchor remain current.

Immutable content revision containing the revision-3 #347 snapshot and revision-3 #344 artifacts:

```text
baabdc4db0135f8e008372d6f96b6f86339cd2a8
```

The follow-up must create a new versioned manifest, preserve every prior manifest/snapshot/artifact byte, and name the permanent merged `main` revision that contains this content revision. No placeholder anchor is permitted.

## Revision-3 #344 relational input

```text
content commit: baabdc4db0135f8e008372d6f96b6f86339cd2a8
DDL: docs/architecture/v19-v3.sql.gz
Git blob: 3308ba8650f81703585a40eb1088b8e0dd54dfa1
stored gzip bytes: 11636
stored gzip SHA-256: 7899c0854766b3eb479e69a6c23b6061adbcb4ce02fcf38eae4dde0af6c06d28
reconstructed DDL bytes: 109332
reconstructed DDL SHA-256: 6d405f3a454ff92e91fdb5b574e52fedb3969d0021fb4c0d47e2f92a88d9114b
schema fingerprint: 47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e
schema-defined objects: 56 tables / 39 explicit indexes / 172 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v3.py.gz
proof Git blob: 2170cabc5a4ad409c71f2369a489964fde3bee4f
proof stored gzip bytes: 13350
proof stored gzip SHA-256: af7e8750a266f599d199ca315850229747b7a4edbdf0b4a2fef52246fcb706ef
proof reconstructed bytes: 68756
proof reconstructed SHA-256: 508c77b0c6ecec11736eada2bd1d4298ac0ea8196602b0cdea78240f93945990
relock: docs/architecture/v19-relock-v3.md
relock Git blob: 33786ce9e3cbddf82e3e12657ace99581eb3da53
```

## Revision-4 snapshot set input

Use this exact issue order:

| Issue | Title / contract | Path | Git blob SHA-1 |
| ---: | --- | --- | --- |
| #304 | Decision / Answer authority | `docs/architecture/v19-contracts/304-decision-answer-authority.md` | `d7aedff121a8ca81333ee61febf0618112f83b2a` |
| #323 | Worker Harness routing / typed fallback | `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| #324 | typed v1 configuration / role separation | `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| #343 | external effects / WorkerWake / reconciliation | `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| #345 | lifecycle / currentness / concurrency / Task archive — revision 2 | `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v2.md` | `50d6747ae140e68faddf15ed3d8337bfa85596c9` |
| #346 | capability / adapter boundaries | `docs/architecture/v19-contracts/346-capability-adapters.md` | `859b80207a625fb4be8f5ff1a5eaf336bb7e8c77` |
| #347 | read models / WorkerReport canonical tail / Task archive — revision 3 | `docs/architecture/v19-contracts/347-read-models-attention-orientation-v3.md` | `0b57e2e8f0bb480e0eac840fdeec8f909c5c5202` |
| #348 | v18→v19 cutover / archive / non-fabrication — revision 2 | `docs/architecture/v19-contracts/348-cutover-archive-v2.md` | `94c018d0bce8c2427d01b9af89e513cfb5f3ce96` |
| #497 | no first-class soft-turn-cancel | `docs/architecture/v19-contracts/497-no-soft-turn-cancel.md` | `5be1875efa61b4c4f68f988156fb3c4d746b0ebb` |
| #519 | user-global runtime generation ownership | `docs/architecture/v19-contracts/519-user-global-runtime-generations.md` | `c712c65dad085103dcc7752a8c09a31b62c82711` |

Git blob SHA-1 is the deterministic content digest for each repository object. The contract-set digest is SHA-256 over the UTF-8 concatenation, in the table's issue order, of:

```text
<basename> NUL <git-blob-sha1> LF
```

Contract-set SHA-256:

```text
afe7c61f34af416bda15c46c6edeacc35a4a43f36d85ee407cb55b690d4fb401
```

## Supersession input

- Revision-3 #345 remains unchanged at blob `50d6747ae140e68faddf15ed3d8337bfa85596c9`.
- Revision-4 #347 supersedes revision-2 #347 blob `bf793d5e3dca230f15e808f5e516db5a0c7cbcc3` without changing its bytes.
- Revision-2 #348 remains unchanged at blob `94c018d0bce8c2427d01b9af89e513cfb5f3ce96`.
- #304, #323, #324, #343, #346, #497, and #519 remain unchanged.
- Revision-3 #344 supersedes revision-2 DDL/proof/relock content without changing or removing any historical artifact.

The follow-up manifest must record that exact schema fingerprint, not numeric `user_version`, defines compatibility. Prior fingerprints `067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e` and `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` remain historical evidence but fail closed without upgrade or overwrite. Final native/release qualification remains outstanding after permanent anchor publication.
