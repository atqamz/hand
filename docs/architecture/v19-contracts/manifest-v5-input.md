# Routing relock manifest input — candidate revision 5

This is deterministic review input, not the permanent main anchor or an accepted replacement authority. Current landed authority remains the revision-4 content described by manifest-v4.md until this additive relock is independently reviewed and landed. Previous permanent main content anchor: 65556169e04808460ba252a677754f19b190b8ab.

The runtime on this isolated candidate branch selects the exact review bytes below so schema, cutover and production-bootstrap qualification can run together. No dependent routing writer is activated. Equal user_version must not upgrade prior development databases. Passing candidate checks does not permit release or waive independent review.

```text
content commit: 9bc350a99e0f13a70e2c55fa8a97da37d3d5a1c6
DDL: docs/architecture/v19-v4.sql.gz
Git blob: 716fd9c9b20ed9597ec382d70c8880dd3a7b82e2
stored gzip bytes: 12360
stored gzip SHA-256: b841d5cd68f66595ca3954a09a1f08a47752a8d57b1977916d956f40188fcd7b
reconstructed DDL bytes: 112243
reconstructed DDL SHA-256: 4b3f4bf99a241a728c01c0de71e92fade2decee75bb3671fcef80cf329abc19f
schema fingerprint: 77c31959a7b47fd8d4035d4fcf9f425b9712b16eb413572b4af9c46d373beae8
schema-defined objects: 57 tables / 39 explicit indexes / 175 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v4.py.gz
proof Git blob: c3e0ab68bdde1bcf8a790384383cbb32d68c7de2
proof stored gzip bytes: 14595
proof stored gzip SHA-256: 810546d999408cb4a4a725a88251ae0da66efe864068afde8f990b5560779bba
proof reconstructed bytes: 75043
proof reconstructed SHA-256: 49a54866e46aed2ffb1338c7b5171a0e97095ac30f02cf2c64c86f352c4725c1
relock: docs/architecture/v19-relock-v4.md
relock Git blob: dc840dae9d0e08e37a7047ec6fccd7233459d435
```

The new DDL supplies request/fallback storage already required by unchanged #323/#324. It does not amend those semantics or redefine provider capability. All immutable semantic snapshots below remain byte-identical.

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

Previous exact fingerprints 47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e, 067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e and 8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8 are incompatible. Preserve them; never silently migrate or overwrite. Legacy cutover source eligibility remains exact v0.7.2 and all archive/freeze/non-fabrication rules remain.

The permanent manifest follow-up must name the actual reviewed main landing and this content revision separately. Do not publish a guessed anchor. Required code/architecture review and exact native/cutover/provider/platform qualification remain pending. No 0.8.0 release/tag/publication is authorized.
