# v19 semantic contract manifest — revision 3

This manifest is the current repository-owned semantic contract anchor for the v0.8.0 / canonical v19 implementation. It supersedes `docs/architecture/v19-contracts/manifest-v2.md` only as the **current manifest**. The revision-1 and revision-2 manifests and every snapshot and artifact they anchor remain immutable historical evidence.

Permanent `main` revision containing the complete revision-3 contract set:

```text
b2e11b5e14007eb46bdc1e820d4e085afd4caa57
```

Immutable content revision containing the revision-3 snapshots and revision-2 #344 artifacts:

```text
47e4e30092a729451d3eec2b3b592cd1ea395725
```

These revisions have distinct roles. `b2e11b5e14007eb46bdc1e820d4e085afd4caa57` is the permanent repository anchor on `main`. `47e4e30092a729451d3eec2b3b592cd1ea395725` identifies the immutable content incorporated into that anchor; it is not the permanent `main` anchor.

For every snapshot listed below, the repository file at the permanent anchor is immutable semantic evidence. GitHub issue bodies remain tracker and review surfaces; later edits cannot retroactively change the anchored evidence. This manifest explicitly transfers current semantic authority for #304, #497, and #519 from their mutable issue bodies to their first repository snapshots. Comments are never normative architecture state.

## Revision-2 #344 relational authority

Exact canonical relational authority is the revision-2 artifact set incorporated from the immutable content revision:

```text
content commit: 47e4e30092a729451d3eec2b3b592cd1ea395725
DDL: docs/architecture/v19-v2.sql.gz
Git blob: 27080702c5efb9ec5366e3180ebe38d2fbee8c76
stored gzip bytes: 11622
stored gzip SHA-256: 465ebf6c1203a66558f2562660d67c3c40f44afc03982115177544c4bacb9f20
reconstructed DDL bytes: 109227
reconstructed DDL SHA-256: bd1081a14f3d5c52803aab40865dd489b3528c71d3ed801ea9560438ae5c8892
schema fingerprint: 067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e
schema-defined objects: 56 tables / 38 explicit indexes / 172 triggers
PRAGMA user_version: 19
proof: docs/architecture/v19-proof-v2.py.gz
proof Git blob: 518c398d39e29586a370ec4199d9c63b976986d0
proof stored gzip bytes: 10973
proof stored gzip SHA-256: ce8480c49d8acc5b9f3867df440092d1ad589ecdca2343cc13ca411f139aeba0
proof reconstructed bytes: 55937
proof reconstructed SHA-256: 582529e9777edb122bea18abf012a0b8e4ad52c2f4752c42b4183ffb99fdf2fa
relock: docs/architecture/v19-relock-v2.md
relock Git blob: 962db47fb45667c0c4e07ed3df7a2af69a7e4462
```

## Snapshot set

| Issue | Title / contract | Path | Git blob SHA-1 |
| ---: | --- | --- | --- |
| #304 | Decision / Answer authority | `docs/architecture/v19-contracts/304-decision-answer-authority.md` | `d7aedff121a8ca81333ee61febf0618112f83b2a` |
| #323 | Worker Harness routing / typed fallback | `docs/architecture/v19-contracts/323-worker-routing.md` | `6b2ce258a9e72412bcbb1cd625963806400e227b` |
| #324 | typed v1 configuration / role separation | `docs/architecture/v19-contracts/324-configuration.md` | `e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b` |
| #343 | external effects / WorkerWake / reconciliation | `docs/architecture/v19-contracts/343-external-effects-worker-wake.md` | `76be8f08e60ba1819df71669edf9cb3af3c34b14` |
| #345 | lifecycle / currentness / concurrency / Task archive — revision 2 | `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery-v2.md` | `50d6747ae140e68faddf15ed3d8337bfa85596c9` |
| #346 | capability / adapter boundaries | `docs/architecture/v19-contracts/346-capability-adapters.md` | `859b80207a625fb4be8f5ff1a5eaf336bb7e8c77` |
| #347 | read models / WorkerReport witness / Task archive — revision 2 | `docs/architecture/v19-contracts/347-read-models-attention-orientation-v2.md` | `bf793d5e3dca230f15e808f5e516db5a0c7cbcc3` |
| #348 | v18→v19 cutover / archive / non-fabrication — revision 2 | `docs/architecture/v19-contracts/348-cutover-archive-v2.md` | `94c018d0bce8c2427d01b9af89e513cfb5f3ce96` |
| #497 | no first-class soft-turn-cancel | `docs/architecture/v19-contracts/497-no-soft-turn-cancel.md` | `5be1875efa61b4c4f68f988156fb3c4d746b0ebb` |
| #519 | user-global runtime generation ownership | `docs/architecture/v19-contracts/519-user-global-runtime-generations.md` | `c712c65dad085103dcc7752a8c09a31b62c82711` |

Git blob SHA-1 is the deterministic content digest for each repository object. The contract-set digest is SHA-256 over the UTF-8 concatenation, in the table's issue order, of:

```text
<basename> NUL <git-blob-sha1> LF
```

Contract-set SHA-256:

```text
646de68dffad2f0227f4cd4ceb86d9b4e366fb48f35844ad836f7db8dd12d1e1
```

## Supersession record

- Revision-3 #345 supersedes the original #345 snapshot at `docs/architecture/v19-contracts/345-lifecycle-currentness-crash-recovery.md`, blob `1039bb9fe69fb84f4b2357c05e6dcbfc6dfb3254`, without changing its bytes.
- Revision-3 #347 supersedes the original #347 snapshot at `docs/architecture/v19-contracts/347-read-models-attention-orientation.md`, blob `72a9905c84e376b8cd5a07ff2c0643b244e97d14`, without changing its bytes.
- Revision-2 #348 remains current and unchanged at blob `94c018d0bce8c2427d01b9af89e513cfb5f3ce96`.
- #304, #497, and #519 receive their first repository semantic snapshots in revision 3.
- Revision-2 #344 supersedes the original DDL, proof, and relock content without changing or removing those historical artifacts.

## Authority graph

Use these categories precisely:

1. **Current normative architecture**: this revision-3 snapshot set and the exact revision-2 #344 DDL, proof, and relock artifacts above. Mutable issue bodies and comments do not compete with this repository-owned authority.
2. **Historical landed prerequisites and evidence**: the revision-1 and revision-2 manifests, superseded snapshots, original #344 artifacts, and closed work such as #338 LaunchSpec, #340 monitoring and stale-wake safety, #353/#355 Supervisor runtime integration, and diagnostic #525. They remain audit evidence and do not compete as current v19 authority.
3. **Implementation dependencies**: open implementation slices whose completion is required by sequencing but which do not define architecture merely by blocking another issue.
4. **Release qualification evidence**: #305 qualifies one exact tag, source, and runtime matrix after implementation; passing it does not redefine architecture.

A semantic cross-reference does not imply a sequencing dependency. A historical prerequisite does not become current normative authority merely because current contracts cite it.

## Exact-fingerprint compatibility

Compatibility is determined by the exact schema fingerprint `067f7ea28694f3dadf9cbc8e5b6e50fe42e77f7b36aaeb33fc121ef9772a1c8e`, not by numeric `PRAGMA user_version`. The numeric version remains `19` and is not sufficient evidence of compatibility.

The prior canonical v19 fingerprint `8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8` remains recognized historical evidence but is incompatible with the current schema. It fails closed with `ErrCanonicalV19SchemaMismatch`; Hand neither upgrades nor overwrites it. Unknown v19 fingerprints fail identically.

## Qualification boundary and change policy

This manifest publishes semantic authority only. Remaining implementation, native lock and crash behavior, optional Linux OFD locking, and release qualification are still pending. Standalone SQL proof and Go-driver schema tests do not satisfy that qualification boundary. This anchor does not by itself complete or accept the remaining work tracked by atqamz/hand#301, atqamz/hand#304, atqamz/hand#344, atqamz/hand#345, atqamz/hand#347, atqamz/hand#497, or atqamz/hand#519.

Do not edit a frozen snapshot or artifact in place. A semantic change requires a new versioned snapshot set and manifest anchor. If a change affects canonical table, constraint, trigger, index, operation-kind semantics, or any byte of the #344 DDL, the #344 relock discipline is mandatory before implementation continues.

DDL impact of this revision-3 manifest: **NONE**.
