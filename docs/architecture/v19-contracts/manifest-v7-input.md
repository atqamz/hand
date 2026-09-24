# Canonical v19 immutability relock manifest input — candidate revision 7

This is candidate consumer input, not the permanent main anchor or release qualification. The prior permanent anchor remains `manifest-v4.md` at main revision `65556169e04808460ba252a677754f19b190b8ab`. Revision-5 and revision-6 manifest inputs and their artifacts remain unchanged historical evidence. The content commit below pins the revision-6 DDL and proof; this consumer revision embeds those exact bytes for fresh canonical targets. A permanent manifest must name the actual reviewed main landing separately.

```text
source base commit: 2e13391968cbbab218da87e0325571ccf5925cad
source base tree: 6623669ab41c7dcd9d076644121d6307c8f3e852
candidate content commit: 3fa9e0a5c9829412ad5fcbec64f6e0fb174ddaac
DDL: docs/architecture/v19-v6.sql.gz
Git blob: b2cf787b749b6247c3bcbed8e75b82d6e439e3cd
stored gzip bytes: 15065
stored gzip SHA-256: 3e7c6e2acac267511b2a1cc746865796d5a3b84b2cc66a245cfe314f2eb8b777
reconstructed DDL bytes: 150312
reconstructed DDL SHA-256: ab04b86f04fffa18c1e058661813714bc3ffd70429b34746e533c2cbc20b31e6
schema fingerprint: ccb9debabf5e194a5d290b6593d1616043bc433561074f09941711de685851ef
schema-defined objects: 57 tables / 39 explicit indexes / 289 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v6.py.gz
proof Git blob: b66b4ee1aa59dccc7315e4ed4079715cc0e0fce5
proof stored gzip bytes: 3081
proof stored gzip SHA-256: 89ec90b7573a4a5d130556efb0e6574f9d6cd31e7a193e3f9848b32fe2244105
proof reconstructed bytes: 8407
proof reconstructed SHA-256: 52770c3a6f9dce971bfd391ef37ad22704705eb1dea3107880ad115ff82599ce
relock: docs/architecture/v19-relock-v6.md
relock Git blob: 765c1fad37685f98dbb953f15e7a7617916576ba
```

## Snapshot set

| Path | Git blob SHA-1 |
| --- | --- |
| `docs/architecture/v19-contracts/304-decision-answer-authority.md` | `d7aedff121a8ca81333ee61febf0618112f83b2a` |
| `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v2.md` | `50d6747ae140e68faddf15ed3d8337bfa85596c9` |
| `docs/architecture/v19-contracts/346-capability-adapters.md` | `859b80207a625fb4be8f5ff1a5eaf336bb7e8c77` |
| `docs/architecture/v19-contracts/347-read-models-attention-orientation-v3.md` | `0b57e2e8f0bb480e0eac840fdeec8f909c5c5202` |
| `docs/architecture/v19-contracts/348-cutover-archive-v2.md` | `94c018d0bce8c2427d01b9af89e513cfb5f3ce96` |
| `docs/architecture/v19-contracts/497-no-soft-turn-cancel.md` | `5be1875efa61b4c4f68f988156fb3c4d746b0ebb` |
| `docs/architecture/v19-contracts/519-user-global-runtime-generations.md` | `c712c65dad085103dcc7752a8c09a31b62c82711` |

Contract-set SHA-256:

```text
afe7c61f34af416bda15c46c6edeacc35a4a43f36d85ee407cb55b690d4fb401
```

Previous exact v19 fingerprints `5ac7161276dbf829f60d00ab4d22e83f65a4f54016a753a1bf482beaba6b8c9a`, `77c31959a7b47fd8d4035d4fcf9f425b9712b16eb413572b4af9c46d373beae8`, `47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e`, `067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e`, and `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` are incompatible. Preserve prior databases; do not silently migrate or overwrite them. This DDL relock does not establish exact v0.7.2 cross-process cutover safety, provider capability, typed routing/configuration, or release qualification. No release/tag/publication is authorized by this input.
