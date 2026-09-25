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

// A byte-identical cp copy at the retired path, after the commit point, must not strand the home frozen.
func TestAbortLegacyV18CutoverFreezeLeavesCopiedRetiredBridge(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	if _, err := ensureLegacyV18CutoverAbortRecord(home, bridge.MigrationID); err != nil {
		t.Fatal(err)
	}
	retired := legacyV18CutoverRetiredBridgePath(home, bridge.MigrationID)
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(retired, data, 0o600); err != nil {
		t.Fatal(err)
	}
	aborted, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || aborted.Disposition != legacyV18CutoverAbortedDisposition {
		t.Fatalf("abort with a copied retired bridge = %#v, %v", aborted, err)
	}
	if got, err := legacyV18CutoverFileSHA256(retired); err != nil || got != bridge.BridgeSHA256 {
		t.Fatalf("copied retired bridge after abort = %s, %v; want it left in place", got, err)
	}
	again, err := abortLegacyV18CutoverFreeze(home)
	if err != nil || again.Disposition != legacyV18CutoverNotFrozenDisposition || again.Record != aborted.Record {
		t.Fatalf("not-frozen report = %#v, %v; want it to name record %s", again, err, aborted.Record)
	}
}

func TestAbortLegacyV18CutoverFreezeNamesStaleLeftovers(t *testing.T) {
	home, bridge, _, _, _, _ := canonicalV19CutoverRecoveryFixture(t)
	journal := Path(home) + "-journal"
	if err := os.WriteFile(journal, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(Dir(home), ".v19-cutover-"+bridge.MigrationID+"-restore.db.candidate")
	if err := os.Mkdir(candidate, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); err == nil || !strings.Contains(err.Error(), "restore candidate path "+candidate+" is not a regular file") {
		t.Fatalf("abort with a directory at the restore candidate = %v", err)
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(home)+"-wal", []byte("wal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := abortLegacyV18CutoverFreeze(home); !errors.Is(err, errLegacyV18CutoverAbortUnsafe) || !strings.Contains(err.Error(), "remove "+Path(home)+"-wal") {
		t.Fatalf("abort with a WAL sidecar = %v, want a refusal naming it", err)
	}
	for _, sidecar := range []string{"-wal", "-shm"} {
		if err := os.Remove(Path(home) + sidecar); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if aborted, err := abortLegacyV18CutoverFreeze(home); err != nil || aborted.Disposition != legacyV18CutoverAbortedDisposition {
		t.Fatalf("abort with only an empty journal = %#v, %v", aborted, err)
	}
}
