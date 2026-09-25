package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #348 revision 4 C4-10: after abort, legacy writers see exactly the pre-freeze DB.
func TestAbortLegacyV18CutoverFreezeRestoresPreFreezeBytes(t *testing.T) {
	home, bridge, archive, artifact, _, materialized := canonicalV19CutoverRecoveryFixture(t)
	manifestBefore, err := legacyV18CutoverFileSHA256(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	stale := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = stale.Close() }()
	if _, err := stale.Exec(`SELECT COUNT(*) FROM meta`); err != nil {
		t.Fatal(err)
	}

	aborted, err := abortLegacyV18CutoverFreeze(home)
	if err != nil {
		t.Fatal(err)
	}
	if aborted.Disposition != legacyV18CutoverAbortedDisposition || aborted.MigrationID != bridge.MigrationID {
		t.Fatalf("abort = %#v", aborted)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.SourceSHA256 || got != archive.SHA256 {
		t.Fatalf("restored active digest = %s, %v; want pre-freeze %s", got, err, bridge.SourceSHA256)
	}
	if got, err := legacyV18CutoverFileSHA256(filepath.Join(aborted.Record, "frozen-bridge.db")); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("abort record bridge digest = %s, %v; want %s", got, err, bridge.BridgeSHA256)
	}
	if got, err := legacyV18CutoverFileSHA256(filepath.Join(aborted.Record, legacyV18CutoverManifestFileName)); err != nil || got != manifestBefore {
		t.Fatalf("abort record manifest digest = %s, %v; want %s", got, err, manifestBefore)
	}
	for _, gone := range []string{artifact.Path, materialized.Path, legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s remains after abort: %v", gone, err)
		}
	}
	if _, err := stale.Exec(`INSERT INTO meta(key, value) VALUES('stale', 'write')`); err == nil || !strings.Contains(err.Error(), legacyV18CutoverFreezeAbortMessage) {
		t.Fatalf("stale bridge connection write after abort = %v, want the freeze guard", err)
	}
	if _, err := os.Lstat(Path(home) + "-journal"); !os.IsNotExist(err) {
		t.Fatalf("stale bridge connection left a journal beside the restored DB: %v", err)
	}
	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil || state.Disposition != legacyV18CutoverRecoveryLegacySource {
		t.Fatalf("recovery after abort = %#v, %v; want legacy source", state, err)
	}

	again, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || again.Disposition != legacyV18CutoverNotFrozenDisposition {
		t.Fatalf("repeated abort = %#v, %v; want not-frozen", again, err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.SourceSHA256 {
		t.Fatalf("repeated abort changed the restored DB: %s, %v", got, err)
	}
}

func TestAbortLegacyV18CutoverFreezeBeforeFreezeChangesNothing(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	before, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	aborted, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || aborted.Disposition != legacyV18CutoverNotFrozenDisposition {
		t.Fatalf("abort before freeze = %#v, %v", aborted, err)
	}
	if after, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || after != before {
		t.Fatalf("abort before freeze changed the source: %s, %v", after, err)
	}
}

// #348 revision 4 C4-7: abort never runs after the first publish mutation.
func TestAbortLegacyV18CutoverFreezeRefusesAfterPublication(t *testing.T) {
	home, _, _, _, _, materialized := canonicalV19CutoverPublicationFixture(t)
	if _, err := publishCanonicalV19Cutover(home); err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); !errors.Is(err, errLegacyV18CutoverAbortUnsafe) {
		t.Fatalf("abort after publication = %v, want refusal", err)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != materialized.SHA256 {
		t.Fatalf("refused abort changed the canonical DB: %s, %v", got, err)
	}
}

// #348 revision 4 C4-8, C4-9: past the record link only abort continues, and it converges.
func TestAbortLegacyV18CutoverFreezeResumesAfterCommitPoint(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	archiveDir := legacyV18CutoverOriginalArchiveDir(home, bridge.MigrationID)
	empty := filepath.Join(archiveDir, legacyV18CutoverAbortRecordPrefix+"crashed-before-link")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	record, err := ensureLegacyV18CutoverAbortRecord(home, bridge.MigrationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(empty); !os.IsNotExist(err) {
		t.Fatalf("record directory without a bridge link survived: %v", err)
	}

	state, err := inspectLegacyV18CutoverRecovery(home)
	if err != nil || state.Disposition != legacyV18CutoverRecoveryRefuse || !strings.Contains(state.Reason, "only hand cutover offline --abort may continue") {
		t.Fatalf("recovery after abort commit point = %#v, %v", state, err)
	}
	if _, err := publishCanonicalV19Cutover(home); !errors.Is(err, errLegacyV18CutoverPublicationUnsafe) {
		t.Fatalf("publication after abort commit point = %v, want refusal", err)
	}
	if _, err := recoverCanonicalV19Cutover(home); !errors.Is(err, errLegacyV18CutoverRecoveryExecutionUnsafe) {
		t.Fatalf("recover after abort commit point = %v, want refusal", err)
	}
	if err := moveLegacyV18CutoverManifestIntoAbortRecord(home, bridge.MigrationID, record); err != nil {
		t.Fatal(err)
	}

	aborted, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || aborted.Record != record {
		t.Fatalf("resumed abort = %#v, %v; want record %s", aborted, err, record)
	}
	if got, err := legacyV18CutoverFileSHA256(Path(home)); err != nil || got != bridge.SourceSHA256 {
		t.Fatalf("resumed abort digest = %s, %v; want %s", got, err, bridge.SourceSHA256)
	}
}

func TestAbortLegacyV18CutoverFreezeRefusesAbsentActive(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	moved := filepath.Join(t.TempDir(), "hand.db")
	if err := os.Rename(Path(home), moved); err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); !errors.Is(err, errLegacyV18CutoverAbortUnsafe) {
		t.Fatalf("abort with absent active = %v, want refusal", err)
	}
	if _, err := os.Lstat(Path(home)); !os.IsNotExist(err) {
		t.Fatalf("refused abort created an active database: %v", err)
	}
	if got, err := legacyV18CutoverFileSHA256(moved); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("refused abort changed the moved bridge: %s, %v", got, err)
	}
}
