# v19 semantic contract manifest — revision 10 candidate input

This is candidate input for a permanent revision-10 manifest. It is not the current manifest and not release qualification. `manifest-v9.md` at permanent main anchor `44eb00b6f051d1c04e250f9952c652dbf0d6814d` remains current authority. Every earlier manifest, candidate input, snapshot, and artifact remains immutable historical evidence.

The follow-up permanent manifest must name the reviewed `main` landing that contains this input's content. No placeholder anchor is permitted.

```text
source base commit: 3cb1de16efb359d86945c019f472fc70ce525e9e
source base tree: 5d98e96d0b3669fd9f2a9d5eafd05f976246bca5
```

## #344 exact relational authority

Unchanged from `manifest-v9.md`.

```text
content commit: 3fa9e0a5c9829412ad5fcbec64f6e0fb174ddaac
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

| Issue | Path | Git blob SHA-1 |
| ---: | --- | --- |
| #304 | `docs/architecture/v19-contracts/304-decision-answer-authority.md` | `d7aedff121a8ca81333ee61febf0618112f83b2a` |
| #323 | `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| #324 | `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| #343 | `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| #345 | `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v3.md` | `8fadb9d6e829aabe2268a28474a1a4d54f1ca5f8` |
| #346 | `docs/architecture/v19-contracts/346-capability-adapters-v2.md` | `74771c189378f9d6d07729339020d5861214aa76` |
| #347 | `docs/architecture/v19-contracts/347-read-models-attention-orientation-v3.md` | `0b57e2e8f0bb480e0eac840fdeec8f909c5c5202` |
| #348 | `docs/architecture/v19-contracts/348-cutover-archive-v4.md` | `74a3b12b44999d86835c243ba89ea943c578d581` |
| #497 | `docs/architecture/v19-contracts/497-no-soft-turn-cancel.md` | `5be1875efa61b4c4f68f988156fb3c4d746b0ebb` |
| #519 | `docs/architecture/v19-contracts/519-user-global-runtime-generations.md` | `c712c65dad085103dcc7752a8c09a31b62c82711` |

The contract-set digest is SHA-256 over the UTF-8 concatenation, in table order, of `<basename> NUL <git-blob-sha1> LF`.

Contract-set SHA-256:

```text
e7522e79f784dc7421cd9ac94b972a0e0be9049f1c7682d6ce50749f94d379b3
```

## Supersession input

- #345 revision 3 supersedes revision 2 blob `50d6747ae140e68faddf15ed3d8337bfa85596c9` without changing its bytes. It removes the fresh external observation from the Task archive writer. A terminal lineage archives once every external operation is resolved and every Worktree, Session, and Executor binding has its durable release or termination fact.
- Every other row is unchanged from `manifest-v9.md`.
- DDL impact on #344: NONE.

This input adds no runtime behavior, dependency, or workflow. It does not change the archive writer, and it does not complete #345, #301, or #305. No release, tag, or publication is authorized by this input.
