# Routing relock manifest input — candidate revision 6

This is deterministic review input, not the permanent main anchor or accepted replacement authority. The current landed authority remains `manifest-v4.md` at permanent main anchor `65556169e04808460ba252a677754f19b190b8ab`. The unchanged `manifest-v5-input.md` records the prior revision-4 DDL candidate. Its content commit `9bc350a99e0f13a70e2c55fa8a97da37d3d5a1c6` cannot identify these new revision-5 bytes.

The isolated runtime remains pinned to its existing revision-4 embedded DDL and cutover authority identity. This revision-5 artifact is review input only. No dependent routing writer is activated. A later consumer switch requires an exact revision-5 content commit and independent #344 review; equal `user_version` cannot upgrade prior development databases.

```text
source base commit: e85388d745363f8b64c4fdc63527c65cc7664322
source base tree: 0993364052b201f35dbeb88f9944613e1f95a71c
DDL: docs/architecture/v19-v5.sql.gz
Git blob: 71b0a173f8838d86bf28c3e2d7ec999f1cf3b266
stored gzip bytes: 12431
stored gzip SHA-256: c43095c11ef38c33243894d9b0cf2ad188c01a8101cf1deafc40d198fb19de96
reconstructed DDL bytes: 112635
reconstructed DDL SHA-256: e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66
schema fingerprint: 5ac7161276dbf829f60d00ab4d22e83f65a4f54016a753a1bf482beaba6b8c9a
schema-defined objects: 57 tables / 39 explicit indexes / 175 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v5.py.gz
proof Git blob: 5a03e891459fd432718ba4dde98cc0cb5386d5da
proof stored gzip bytes: 14821
proof stored gzip SHA-256: 81e657e6f24efef646aa98caaf2aeaaf4ddadd078e6005c655d6b27c5dc2f3a6
proof reconstructed bytes: 77078
proof reconstructed SHA-256: 4546b5796cc0badd102e249e2c329fbfc98cd09237a3b3b257b1844a73d2462d
relock: docs/architecture/v19-relock-v5.md
relock Git blob: 6005de33a3602f8e71c73cd9135fed4129461b86
```

The revision-5 DDL corrects only the NUL-hidden overflow in existing request/fallback fields. It does not amend the unchanged #323/#324 semantics or redefine provider capability. All immutable semantic snapshots below remain byte-identical.

## Snapshot set

For each entry, the repository file at the permanent anchor is immutable semantic evidence. Issue bodies are implementation trackers; comments are audit history, not normative architecture.

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

The contract-set digest is SHA-256 over the UTF-8 concatenation in the table's issue order:

```text
<basename> NUL <git-blob-sha1> LF
```

Contract-set SHA-256:

```text
afe7c61f34af416bda15c46c6edeacc35a4a43f36d85ee407cb55b690d4fb401
```

Git blob SHA-1 identifies repository objects; it does not replace the artifact SHA-256 or schema fingerprint.


## Exact compatibility and landing gate

Previous exact fingerprints `77c31959a7b47fd8d4035d4fcf9f425b9712b16eb413572b4af9c46d373beae8`, `47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e`, `067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e`, and `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` are incompatible. Preserve them; never silently migrate or overwrite. Legacy cutover source eligibility remains exact v0.7.2 and all archive/freeze/non-fabrication rules remain.

After an authorized content commit and independent review, the consumer switch must use its actual revision, artifact identities, and fingerprint together. The permanent manifest follow-up must then name the actual reviewed main landing separately. No guessed anchor or content commit is valid. Required code/architecture review and exact native/cutover/provider/platform qualification remain pending. No 0.8.0 release/tag/publication is authorized.
