package store

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #344: the isolated relock input must identify the exact bytes under review.
// The permanent main anchor remains a separate landing/release gate.
func TestCanonicalV19ManifestMatchesFrozenArtifacts(t *testing.T) {
	manifest := string(readV19ManifestArtifact(t, "docs/architecture/v19-contracts/manifest-v7-input.md"))
	for _, field := range []string{
		"source base commit: 2e13391968cbbab218da87e0325571ccf5925cad",
		"source base tree: 6623669ab41c7dcd9d076644121d6307c8f3e852",
		"candidate content commit: " + canonicalV19AuthorityCommit,
		"DDL: " + canonicalV19AuthorityDDLPath,
		"Git blob: " + canonicalV19AuthorityDDLGitBlobSHA1,
		fmt.Sprintf("stored gzip bytes: %d", canonicalV19GzipBytes),
		"stored gzip SHA-256: " + canonicalV19GzipSHA256,
		fmt.Sprintf("reconstructed DDL bytes: %d", canonicalV19DDLBytes),
		"reconstructed DDL SHA-256: " + canonicalV19DDLSHA256,
		"schema fingerprint: " + canonicalV19SchemaFingerprint,
		fmt.Sprintf("schema-defined objects: %d tables / %d explicit indexes / %d triggers", canonicalV19TableCount, canonicalV19IndexCount, canonicalV19TriggerCount),
		fmt.Sprintf("PRAGMA user_version: %d", canonicalV19SchemaVersion),
		canonicalV19PriorSchemaFingerprintV1,
		canonicalV19PriorSchemaFingerprintV2,
		canonicalV19PriorSchemaFingerprintV3,
		canonicalV19PriorSchemaFingerprintV4,
		canonicalV19PriorSchemaFingerprintV5,
	} {
		if !strings.Contains(manifest, field) {
			t.Errorf("manifest missing runtime/cutover identity %q", field)
		}
	}

	ddl := readV19ManifestArtifact(t, canonicalV19AuthorityDDLPath)
	if !bytes.Equal(ddl, canonicalV19Gzip) {
		t.Fatal("versioned manifest DDL differs from the embedded runtime DDL")
	}
	if got := v19ManifestGitBlob(ddl); got != canonicalV19AuthorityDDLGitBlobSHA1 {
		t.Fatalf("versioned DDL Git blob = %s, want %s", got, canonicalV19AuthorityDDLGitBlobSHA1)
	}
	if _, err := canonicalV19DDL(); err != nil {
		t.Fatal(err)
	}

	proof := readV19ManifestArtifact(t, "docs/architecture/v19-proof-v6.py.gz")
	for _, field := range []string{
		"proof: docs/architecture/v19-proof-v6.py.gz",
		"proof Git blob: " + v19ManifestGitBlob(proof),
		fmt.Sprintf("proof stored gzip bytes: %d", len(proof)),
		"proof stored gzip SHA-256: " + canonicalV19SHA256(proof),
	} {
		if !strings.Contains(manifest, field) {
			t.Errorf("manifest missing proof artifact identity %q", field)
		}
	}
	r, err := gzip.NewReader(bytes.NewReader(proof))
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, readErr := io.ReadAll(r)
	closeErr := r.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read proof: %v; close: %v", readErr, closeErr)
	}
	for _, field := range []string{
		fmt.Sprintf("proof reconstructed bytes: %d", len(reconstructed)),
		"proof reconstructed SHA-256: " + canonicalV19SHA256(reconstructed),
		"relock: docs/architecture/v19-relock-v6.md",
		"relock Git blob: " + v19ManifestGitBlob(readV19ManifestArtifact(t, "docs/architecture/v19-relock-v6.md")),
	} {
		if !strings.Contains(manifest, field) {
			t.Errorf("manifest missing reconstructed/relock identity %q", field)
		}
	}
}

func TestCanonicalV19PermanentManifestRevision4Artifacts(t *testing.T) {
	manifest := string(readV19ManifestArtifact(t, "docs/architecture/v19-contracts/manifest-v4.md"))
	for _, field := range []string{
		"65556169e04808460ba252a677754f19b190b8ab",
		"content commit: baabdc4db0135f8e008372d6f96b6f86339cd2a8",
		"schema fingerprint: 47b6d208b9fffb29e7b0d9f64d30d468c5c5f214b3e5e91313d81e66542df39e",
	} {
		if !strings.Contains(manifest, field) {
			t.Errorf("revision-4 manifest missing %q", field)
		}
	}
	for _, artifact := range []struct{ path, pathField, blobField string }{
		{"docs/architecture/v19-v3.sql.gz", "DDL: ", "Git blob: "},
		{"docs/architecture/v19-proof-v3.py.gz", "proof: ", "proof Git blob: "},
		{"docs/architecture/v19-relock-v3.md", "relock: ", "relock Git blob: "},
	} {
		data := readV19ManifestArtifact(t, artifact.path)
		if !strings.Contains(manifest, artifact.pathField+artifact.path) ||
			!strings.Contains(manifest, artifact.blobField+v19ManifestGitBlob(data)) {
			t.Errorf("revision-4 manifest does not identify %s", artifact.path)
		}
	}
}

// #344/#347: a permanent manifest must retain the exact ordered contract-set digest.
func TestCanonicalV19ManifestSnapshotSet(t *testing.T) {
	for _, name := range []string{"manifest-v4.md", "manifest-v5-input.md", "manifest-v6-input.md", "manifest-v7-input.md"} {
		t.Run(name, func(t *testing.T) { assertCanonicalV19ManifestSnapshotSet(t, name) })
	}
}

func TestCanonicalV19Revision5InputMatchesArtifacts(t *testing.T) {
	manifest := string(readV19ManifestArtifact(t, "docs/architecture/v19-contracts/manifest-v6-input.md"))
	for _, field := range []string{
		"source base commit: e85388d745363f8b64c4fdc63527c65cc7664322",
		"source base tree: 0993364052b201f35dbeb88f9944613e1f95a71c",
		"candidate content commit: 97d9f7c045a0060cac05a085221d91cb7fd661e8",
		"DDL: docs/architecture/v19-v5.sql.gz",
		"Git blob: 71b0a173f8838d86bf28c3e2d7ec999f1cf3b266",
		"stored gzip bytes: 12431",
		"stored gzip SHA-256: c43095c11ef38c33243894d9b0cf2ad188c01a8101cf1deafc40d198fb19de96",
		"reconstructed DDL bytes: 112635",
		"reconstructed DDL SHA-256: e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66",
		"schema fingerprint: 5ac7161276dbf829f60d00ab4d22e83f65a4f54016a753a1bf482beaba6b8c9a",
		"schema-defined objects: 57 tables / 39 explicit indexes / 175 triggers",
		"PRAGMA user_version: 19",
		"proof: docs/architecture/v19-proof-v5.py.gz",
		"proof Git blob: 5a03e891459fd432718ba4dde98cc0cb5386d5da",
		"proof stored gzip bytes: 14821",
		"proof stored gzip SHA-256: 81e657e6f24efef646aa98caaf2aeaaf4ddadd078e6005c655d6b27c5dc2f3a6",
		"proof reconstructed bytes: 77078",
		"proof reconstructed SHA-256: 4546b5796cc0badd102e249e2c329fbfc98cd09237a3b3b257b1844a73d2462d",
		"relock: docs/architecture/v19-relock-v5.md",
		"relock Git blob: " + v19ManifestGitBlob(readV19ManifestArtifact(t, "docs/architecture/v19-relock-v5.md")),
	} {
		if !strings.Contains(manifest, field) {
			t.Errorf("revision-5 input missing %q", field)
		}
	}
	for _, artifact := range []struct {
		path, blob, compressedSHA, reconstructedSHA string
		compressedBytes, reconstructedBytes         int
	}{
		{"docs/architecture/v19-v5.sql.gz", "71b0a173f8838d86bf28c3e2d7ec999f1cf3b266", "c43095c11ef38c33243894d9b0cf2ad188c01a8101cf1deafc40d198fb19de96", "e89280ddb3751142078d27e1f4203d45462cc5c9c03c04277948f090e4521a66", 12431, 112635},
		{"docs/architecture/v19-proof-v5.py.gz", "5a03e891459fd432718ba4dde98cc0cb5386d5da", "81e657e6f24efef646aa98caaf2aeaaf4ddadd078e6005c655d6b27c5dc2f3a6", "4546b5796cc0badd102e249e2c329fbfc98cd09237a3b3b257b1844a73d2462d", 14821, 77078},
	} {
		data := readV19ManifestArtifact(t, artifact.path)
		if len(data) != artifact.compressedBytes || canonicalV19SHA256(data) != artifact.compressedSHA || v19ManifestGitBlob(data) != artifact.blob {
			t.Errorf("revision-5 artifact %s compressed identity changed", artifact.path)
		}
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if len(raw) != artifact.reconstructedBytes || canonicalV19SHA256(raw) != artifact.reconstructedSHA {
			t.Errorf("revision-5 artifact %s reconstructed identity changed", artifact.path)
		}
	}
}

func assertCanonicalV19ManifestSnapshotSet(t *testing.T, name string) {
	t.Helper()
	manifest := string(readV19ManifestArtifact(t, "docs/architecture/v19-contracts/"+name))
	snapshots := []struct{ name, blob string }{
		{"304-decision-answer-authority.md", "d7aedff121a8ca81333ee61febf0618112f83b2a"},
		{"323-worker-routing.md", "6b2ce258a9e72412bcbb1cd625963806400e227b"},
		{"324-configuration.md", "e322eb6bf08b3648c1a298e13b6fc4b8a2e19f7b"},
		{"343-external-effects-worker-wake.md", "76be8f08e60ba1819df71669edf9cb3af3c34b14"},
		{"345-lifecycle-currentness-crash-recovery-v2.md", "50d6747ae140e68faddf15ed3d8337bfa85596c9"},
		{"346-capability-adapters.md", "859b80207a625fb4be8f5ff1a5eaf336bb7e8c77"},
		{"347-read-models-attention-orientation-v3.md", "0b57e2e8f0bb480e0eac840fdeec8f909c5c5202"},
		{"348-cutover-archive-v2.md", "94c018d0bce8c2427d01b9af89e513cfb5f3ce96"},
		{"497-no-soft-turn-cancel.md", "5be1875efa61b4c4f68f988156fb3c4d746b0ebb"},
		{"519-user-global-runtime-generations.md", "c712c65dad085103dcc7752a8c09a31b62c82711"},
	}
	if got := strings.Count(manifest, "| `docs/architecture/v19-contracts/"); got != len(snapshots) {
		t.Fatalf("manifest snapshot rows = %d, want %d", got, len(snapshots))
	}
	var ordered strings.Builder
	previous := -1
	for _, snapshot := range snapshots {
		path := "docs/architecture/v19-contracts/" + snapshot.name
		got := v19ManifestGitBlob(readV19ManifestArtifact(t, path))
		if got != snapshot.blob {
			t.Errorf("%s Git blob = %s, want frozen %s", path, got, snapshot.blob)
		}
		row := "| `" + path + "` | `" + snapshot.blob + "` |"
		position := strings.Index(manifest, row)
		if position <= previous || strings.Count(manifest, row) != 1 {
			t.Fatalf("missing, duplicate or out-of-order snapshot row: %s", row)
		}
		previous = position
		ordered.WriteString(snapshot.name + "\x00" + got + "\n")
	}
	const want = "afe7c61f34af416bda15c46c6edeacc35a4a43f36d85ee407cb55b690d4fb401"
	if got := canonicalV19SHA256([]byte(ordered.String())); got != want {
		t.Fatalf("contract-set SHA-256 = %s, want %s", got, want)
	}
	if !strings.Contains(manifest, "Contract-set SHA-256:\n\n```text\n"+want+"\n```") {
		t.Fatal("manifest does not publish the verified contract-set digest")
	}
}

func readV19ManifestArtifact(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func v19ManifestGitBlob(data []byte) string {
	// SHA-1 is Git's object format here, not a security or artifact-signature claim.
	return fmt.Sprintf("%x", sha1.Sum(append(fmt.Appendf(nil, "blob %d\x00", len(data)), data...)))
}
