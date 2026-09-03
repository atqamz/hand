package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyV18CutoverMigrationIdentityIsVersionedAndDeterministic(t *testing.T) {
	fleetID := "f_" + strings.Repeat("0", 31) + "1"
	sourceSHA256 := strings.Repeat("a", 64)
	got, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	const want = "v1-84d789c3053f5ae2f0ab22460102d715abc74416726bb5ebd3119e1035904e7e"
	if got != want {
		t.Fatalf("migration identity = %q, want %q", got, want)
	}
	again, err := legacyV18CutoverMigrationIdentity(fleetID, sourceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if again != got {
		t.Fatalf("migration identity changed across identical inputs: first=%q second=%q", got, again)
	}
	if _, err := legacyV18CutoverMigrationIdentity(fleetID, strings.Repeat("A", 64)); err == nil {
		t.Fatal("uppercase source digest was accepted")
	}
}

func TestAcquireLegacyV18CutoverGateStagesExactNonAuthoritativeArchiveCandidate(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	sourceBytes, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	if gate.archiveCandidate.Path == "" || gate.archiveCandidate.MigrationID == "" {
		t.Fatalf("archive candidate = %#v, want deterministic identity and path", gate.archiveCandidate)
	}
	if gate.archiveCandidate.SHA256 != sourceDigest {
		t.Fatalf("archive candidate digest = %s, want %s", gate.archiveCandidate.SHA256, sourceDigest)
	}
	if filepath.Dir(gate.archiveCandidate.Path) != filepath.Dir(Path(home)) {
		t.Fatalf("archive candidate path = %q, want sibling of active DB %q", gate.archiveCandidate.Path, Path(home))
	}
	candidateBytes, err := os.ReadFile(gate.archiveCandidate.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(candidateBytes, sourceBytes) {
		t.Fatal("archive candidate bytes differ from exact pre-freeze source bytes")
	}
	candidateDigest, err := legacyV18CutoverFileSHA256(gate.archiveCandidate.Path)
	if err != nil {
		t.Fatal(err)
	}
	if candidateDigest != sourceDigest {
		t.Fatalf("archive candidate rehash = %s, want %s", candidateDigest, sourceDigest)
	}
	fleetID, err := legacyV18CutoverFleetID(sqliteConnQueryer{ctx: context.Background(), conn: gate.conn})
	if err != nil {
		t.Fatal(err)
	}
	migrationID, err := legacyV18CutoverMigrationIdentity(fleetID, sourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	if gate.archiveCandidate.MigrationID != migrationID || gate.archiveCandidate.Path != legacyV18CutoverArchiveCandidatePath(home, migrationID) {
		t.Fatalf("archive candidate = %#v, want migration_id=%q deterministic path", gate.archiveCandidate, migrationID)
	}
}

func TestAcquireLegacyV18CutoverGateReusesExactCandidateAfterFreshGateProof(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")

	first, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := first.archiveCandidate.Path
	before, err := os.Stat(candidatePath)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	if second.archiveCandidate.Path != candidatePath {
		t.Fatalf("candidate path changed across fresh gate proof: first=%q second=%q", candidatePath, second.archiveCandidate.Path)
	}
	after, err := os.Stat(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("exact archive candidate was replaced instead of reused")
	}
}

func TestWriteLegacyV18CutoverArchiveCandidateRefusesNonRegularExistingPath(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	sourceDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	candidatePath := filepath.Join(filepath.Dir(Path(home)), "candidate")
	if err := os.Mkdir(candidatePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeLegacyV18CutoverArchiveCandidate(Path(home), candidatePath, sourceDigest); err == nil || !strings.Contains(err.Error(), "direct regular file") {
		t.Fatalf("non-regular candidate error = %v, want direct regular file refusal", err)
	}
}
