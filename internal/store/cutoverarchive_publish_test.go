package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromoteLegacyV18CutoverArchiveCandidatePublishesExactOriginalArchive(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceBytes, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareLegacyV18CutoverArchiveCandidate(home, "f_"+strings.Repeat("0", 31)+"1", sourceDigest)
	if err != nil {
		t.Fatal(err)
	}

	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if archive.MigrationID != candidate.MigrationID || archive.SHA256 != sourceDigest {
		t.Fatalf("original archive = %#v, candidate=%#v", archive, candidate)
	}
	if archive.Directory != legacyV18CutoverOriginalArchiveDir(home, candidate.MigrationID) || archive.Path != legacyV18CutoverOriginalArchivePath(home, candidate.MigrationID) {
		t.Fatalf("original archive path = %#v, want deterministic archive directory/path", archive)
	}
	if _, err := os.Lstat(candidate.Path); !os.IsNotExist(err) {
		t.Fatalf("candidate after promotion stat error = %v, want not exist", err)
	}
	archiveBytes, err := os.ReadFile(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(archiveBytes, sourceBytes) {
		t.Fatal("published original archive bytes differ from exact source bytes")
	}
	publishedDigest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	if publishedDigest != sourceDigest {
		t.Fatalf("published original archive digest = %s, want %s", publishedDigest, sourceDigest)
	}
}

func TestPromoteLegacyV18CutoverArchiveCandidateReusesExactExistingArchive(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	fleetID := "f_" + strings.Repeat("0", 31) + "1"
	candidate, err := prepareLegacyV18CutoverArchiveCandidate(home, fleetID, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(archive.Path)
	if err != nil {
		t.Fatal(err)
	}

	candidate, err = prepareLegacyV18CutoverArchiveCandidate(home, fleetID, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	again, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(again.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("exact existing original archive was replaced")
	}
	if _, err := os.Stat(candidate.Path); err != nil {
		t.Fatalf("idempotent exact candidate was unexpectedly removed: %v", err)
	}
}

func TestPromoteLegacyV18CutoverArchiveCandidateRefusesMismatchedExistingArchive(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareLegacyV18CutoverArchiveCandidate(home, "f_"+strings.Repeat("0", 31)+"1", sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := legacyV18CutoverOriginalArchiveDir(home, candidate.MigrationID)
	if err := os.Mkdir(archiveDir, 0o700); err != nil {
		t.Fatal(err)
	}
	archivePath := legacyV18CutoverOriginalArchivePath(home, candidate.MigrationID)
	if err := os.WriteFile(archivePath, []byte("different evidence"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate); err == nil || !strings.Contains(err.Error(), "existing legacy v18 cutover original archive digest") {
		t.Fatalf("mismatched existing archive error = %v", err)
	}
	if _, err := os.Stat(candidate.Path); err != nil {
		t.Fatalf("candidate changed after mismatched archive refusal: %v", err)
	}
	got, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "different evidence" {
		t.Fatalf("mismatched archive was overwritten: %q", got)
	}
}

func TestPromoteLegacyV18CutoverArchiveCandidateRefusesNonCanonicalCandidatePath(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	migrationID, err := legacyV18CutoverMigrationIdentity("f_"+strings.Repeat("0", 31)+"1", sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(home, "state", "other.db")
	if err := os.WriteFile(candidatePath, []byte("not the candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := legacyV18CutoverArchiveCandidate{MigrationID: migrationID, Path: candidatePath, SHA256: sourceDigest}
	if _, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate); err == nil || !strings.Contains(err.Error(), "want deterministic") {
		t.Fatalf("non-canonical candidate path error = %v", err)
	}
}

func TestPromoteLegacyV18CutoverArchiveCandidateRefusesNonDirectoryArchivePath(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := prepareLegacyV18CutoverArchiveCandidate(home, "f_"+strings.Repeat("0", 31)+"1", sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	archiveDir := legacyV18CutoverOriginalArchiveDir(home, candidate.MigrationID)
	if err := os.WriteFile(archiveDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := promoteLegacyV18CutoverArchiveCandidate(home, candidate); err == nil || !strings.Contains(err.Error(), "not a direct directory") {
		t.Fatalf("non-directory archive path error = %v", err)
	}
}

func TestValidateLegacyV18CutoverMigrationIDRejectsPathLikeIdentity(t *testing.T) {
	for _, value := range []string{"../escape", "v1-../escape", "v2-" + strings.Repeat("a", 64), "v1-" + strings.Repeat("A", 64)} {
		if err := validateLegacyV18CutoverMigrationID(value); err == nil {
			t.Fatalf("migration identity %q was accepted", value)
		}
	}
}
