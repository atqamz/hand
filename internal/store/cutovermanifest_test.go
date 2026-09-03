package store

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWriteLegacyV18CutoverManifestPublishesDeterministicEvidence(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)

	artifact, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.MigrationID != archive.MigrationID || artifact.Path != legacyV18CutoverManifestPath(home, archive.MigrationID) || artifact.ImportedAt != input.ImportedAt {
		t.Fatalf("manifest artifact = %#v", artifact)
	}
	if _, err := os.Lstat(legacyV18CutoverManifestCandidatePath(home, archive.MigrationID)); !os.IsNotExist(err) {
		t.Fatalf("manifest candidate after publication stat error = %v, want not exist", err)
	}
	payload, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := canonicalV19SHA256(payload); got != artifact.SHA256 {
		t.Fatalf("manifest SHA-256 = %s, want %s", got, artifact.SHA256)
	}
	if bytes.Contains(payload, []byte("credential-like-secret")) || bytes.Contains(payload, []byte(input.Projects[0].LegacyURL)) {
		t.Fatalf("manifest leaked raw legacy URL/policy input: %s", payload)
	}

	var manifest legacyV18CutoverManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FormatVersion != legacyV18CutoverManifestVersion || manifest.MigrationID != archive.MigrationID || manifest.Fleet.FleetID != input.FleetID {
		t.Fatalf("manifest identity = %#v", manifest)
	}
	if manifest.Source.Contract != legacyV18CutoverSourceContract || manifest.Source.SemanticVersion != 18 || manifest.Source.SQLiteUserVersion != legacyV072SchemaVersion || manifest.Source.DBSHA256 != archive.SHA256 {
		t.Fatalf("manifest source evidence = %#v", manifest.Source)
	}
	if manifest.OriginalArchive.RelativePath != "hand.db" || manifest.OriginalArchive.DBSHA256 != archive.SHA256 {
		t.Fatalf("manifest original archive = %#v", manifest.OriginalArchive)
	}
	wantCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + archive.SHA256
	if manifest.Freeze.CertificateValue != wantCertificate || manifest.Freeze.CertificateSHA256 != canonicalV19SHA256([]byte(wantCertificate)) || manifest.Freeze.BridgeUserVersion != legacyV18CutoverFrozenUserVersion {
		t.Fatalf("manifest freeze evidence = %#v", manifest.Freeze)
	}
	if manifest.Target.AuthorityCommit != canonicalV19AuthorityCommit || manifest.Target.DDLSHA256 != canonicalV19DDLSHA256 || manifest.Target.SchemaFingerprint != canonicalV19SchemaFingerprint || manifest.Target.SQLiteUserVersion != canonicalV19SchemaVersion {
		t.Fatalf("manifest target authority = %#v", manifest.Target)
	}
	if len(manifest.Projects) != 2 || manifest.Projects[0].SourceProjectID != "project-a" || manifest.Projects[1].SourceProjectID != "project-b" {
		t.Fatalf("manifest Projects = %#v, want canonical locator order", manifest.Projects)
	}
	if manifest.Projects[0].CommonGitDir != "projects/alpha/.git" || manifest.Projects[0].PolicyInputSHA256 == "" || manifest.Projects[0].RepositoryIdentitySHA256 == "" || manifest.Projects[0].PhysicalIdentitySHA256 == "" {
		t.Fatalf("manifest Project evidence = %#v", manifest.Projects[0])
	}
	if len(manifest.Sidecars) != 0 {
		t.Fatalf("manifest sidecars = %#v, want none until positively evidenced", manifest.Sidecars)
	}
}

func TestWriteLegacyV18CutoverManifestReusesExactExistingFinal(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	first, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}

	second, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(second.Path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !os.SameFile(before, after) {
		t.Fatalf("exact final manifest was replaced: first=%#v second=%#v", first, second)
	}
}

func TestWriteLegacyV18CutoverManifestRefusesMismatchedExistingFinal(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	artifact, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact.Path, []byte("different final evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := writeLegacyV18CutoverManifest(home, archive, input); err == nil || !strings.Contains(err.Error(), "existing legacy v18 cutover manifest differs") {
		t.Fatalf("mismatched final manifest error = %v", err)
	}
	got, err := os.ReadFile(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "different final evidence\n" {
		t.Fatalf("mismatched final manifest was overwritten: %q", got)
	}
}

func TestWriteLegacyV18CutoverManifestRebuildsNonAuthoritativeCandidate(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	candidatePath := legacyV18CutoverManifestCandidatePath(home, archive.MigrationID)
	if err := os.WriteFile(candidatePath, []byte("stale candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	artifact, err := writeLegacyV18CutoverManifest(home, archive, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifact.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(candidatePath); !os.IsNotExist(err) {
		t.Fatalf("candidate after rebuild/publication stat error = %v, want not exist", err)
	}
}

func TestBuildLegacyV18CutoverManifestRefusesNonCanonicalImportedAt(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	input.ImportedAt = "2026-09-03T17:42:55+07:00"
	if _, err := buildLegacyV18CutoverManifest(home, archive, input); err == nil || !strings.Contains(err.Error(), "want exact canonical UTC") {
		t.Fatalf("non-canonical imported_at error = %v", err)
	}
}

func TestBuildLegacyV18CutoverManifestRefusesCrossProjectPhysicalAlias(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	input.Projects[1].CommonDirPhysicalID = input.Projects[0].RepositoryPhysicalID
	if _, err := buildLegacyV18CutoverManifest(home, archive, input); err == nil || !strings.Contains(err.Error(), "physical identity aliases") {
		t.Fatalf("cross-Project physical alias error = %v", err)
	}
}

func TestBuildLegacyV18CutoverManifestRefusesChangedOriginalArchive(t *testing.T) {
	home, archive, input := legacyV18CutoverManifestFixture(t)
	if err := os.WriteFile(archive.Path, []byte("changed original archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildLegacyV18CutoverManifest(home, archive, input); err == nil || !strings.Contains(err.Error(), "original archive digest") {
		t.Fatalf("changed original archive error = %v", err)
	}
}

func legacyV18CutoverManifestFixture(t *testing.T) (string, legacyV18CutoverOriginalArchive, LegacyV18CutoverManifestInput) {
	t.Helper()
	home := createLegacyV18CutoverTestSource(t)
	fleetID := "f_" + strings.Repeat("0", 31) + "1"
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareLegacyV18CutoverArchiveCandidate(home, fleetID, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate)
	if err != nil {
		t.Fatal(err)
	}
	input := LegacyV18CutoverManifestInput{
		FleetID:    fleetID,
		ImportedAt: "2026-09-03T10:42:55.123456789Z",
		Projects: []LegacyV18CutoverManifestProjectInput{
			{
				SourceProjectID:      "project-b",
				Locator:              "projects/beta",
				RepositoryPhysicalID: "unix-v1:dev=0000000000000002:ino=0000000000000002",
				CommonDirPhysicalID:  "unix-v1:dev=0000000000000002:ino=0000000000000003",
				Revision:             strings.Repeat("b", 40),
				LegacyName:           "beta",
				LegacyURL:            "https://example.invalid/beta.git",
				LegacyMode:           "clone",
				LegacyUpstream:       "origin/main",
			},
			{
				SourceProjectID:      "project-a",
				Locator:              "projects/alpha",
				RepositoryPhysicalID: "unix-v1:dev=0000000000000001:ino=0000000000000002",
				CommonDirPhysicalID:  "unix-v1:dev=0000000000000001:ino=0000000000000003",
				Revision:             strings.Repeat("a", 40),
				LegacyName:           "alpha",
				LegacyURL:            "https://user:credential-like-secret@example.invalid/alpha.git",
				LegacyMode:           "clone",
				LegacyUpstream:       "origin/main",
			},
		},
	}
	return home, archive, input
}
