# Canonical v19 routing relock candidate — revision 5

This review input corrects the revision-4 SQLite `length(TEXT)` NUL bypass in Attempt request overrides and AttemptFallbackStep references and observation digest. It does not replace accepted authority or activate routing writes. The revision-4 artifacts, permanent manifest, and embedded runtime DDL remain byte-identical.

## Exact candidate

- Source base: #601 `e85388d745363f8b64c4fdc63527c65cc7664322`, tree `0993364052b201f35dbeb88f9944613e1f95a71c`
- DDL: `v19-v5.sql.gz`
- Reconstructed bytes: 112635
- Reconstructed SHA-256: `e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66`
- Compressed bytes: 12431
- Compressed SHA-256: `c43095c11ef38c33243894d9b0cf2ad188c01a8101cf1deafc40d198fb19de96`
- Git blob SHA-1: `71b0a173f8838d86bf28c3e2d7ec999f1cf3b266`
- Schema fingerprint: `5ac7161276dbf829f60d00ab4d22e83f65a4f54016a753a1bf482beaba6b8c9a`
- Objects: 57 tables / 39 explicit indexes / 175 triggers; 271 SQL-bearing objects, 88 SQLite autoindexes
- `PRAGMA user_version = 19`

The fingerprint is SHA-256 of UTF-8 `type|name|tbl_name|sql` lines ordered by `(type,name)`, excluding `sqlite_%` and NULL SQL. The new SQL source derives from reconstructed revision 4. Four nullable Attempt override checks and four fallback reference checks retain their 512-character limits and reject embedded NUL with `instr(value, char(0)) = 0`. The fallback digest additionally requires exactly 64 BLOB bytes, as the Task archive digest already does. No other schema object changes.

## Mechanical proof

- Runner: `v19-proof-v5.py.gz`
- Reconstructed bytes: 77078
- Reconstructed SHA-256: `4546b5796cc0badd102e249e2c329fbfc98cd09237a3b3b257b1844a73d2462d`
- Compressed bytes: 14821
- Compressed SHA-256: `81e657e6f24efef646aa98caaf2aeaaf4ddadd078e6005c655d6b27c5dc2f3a6`
- Git blob SHA-1: `5a03e891459fd432718ba4dde98cc0cb5386d5da`

Reconstruct both files with `gzip -dc`, then run the Python proof with `--json`. Its SQLite 3.53.4 run passed 32 adversarial, 25 archive, 12 report-tail, 43 routing-provenance cases, 13 indexed query checks, and cutover non-fabrication. The nine new negative cases reject NUL-hidden overlong values in all four Attempt overrides and four fallback references, plus a suffix hidden after the 64-character digest. Two positive cases preserve the 512-character Unicode boundary. The pinned Go driver test runs the same nine negative cases against the exact revision-5 artifact. Both proofs failed against revision 4 before the constraint change.

The compressed artifacts were generated from uncompressed SQL and Python sources with Python `gzip.compress(data, compresslevel=9, mtime=0)`; decompression and identical recompression verify their stored bytes.

## Authority boundary

Revision 4 remains the current embedded runtime schema and the content named by commit `9bc350a99e0f13a70e2c55fa8a97da37d3d5a1c6`. Revision 5 has no content commit or independent #344 disposition yet. Its DDL SHA-256 and fingerprint differ from revision 4, so equal `user_version` does not permit upgrade or overwrite. A consumer switch must bind the exact revised content commit, DDL and proof artifacts, cutover target identity, and review-input manifest together after review. The permanent manifest must name the actual landed main anchor separately. No placeholder commit or revision-4 commit may stand in for revision-5 content.
