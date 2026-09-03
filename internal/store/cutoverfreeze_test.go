package store

import (
	"context"
	"strings"
	"testing"
)

func TestFreezeLegacyV18CutoverSourceCommitsExactFrozenBridge(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")

	staleDB := openLegacyV18CutoverTestDB(t, home, true)
	staleInsert, err := staleDB.Prepare(`INSERT INTO meta(key, value) VALUES(?, ?)`)
	if err != nil {
		_ = staleDB.Close()
		t.Fatal(err)
	}
	defer func() {
		_ = staleInsert.Close()
		_ = staleDB.Close()
	}()

	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	originalDigest := gate.sourceSHA256
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
	if err != nil {
		t.Fatal(err)
	}
	archiveBefore, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}

	bridge, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bridge.Committed {
		t.Fatal("frozen bridge did not report committed state")
	}
	if bridge.SourceSHA256 != originalDigest || bridge.Certificate != "v1:"+originalDigest {
		t.Fatalf("frozen bridge provenance = %+v, want original digest %s", bridge, originalDigest)
	}
	if bridge.BridgeSHA256 == "" || bridge.BridgeSHA256 == originalDigest {
		t.Fatalf("frozen bridge digest = %q, want nonempty digest distinct from original %s", bridge.BridgeSHA256, originalDigest)
	}
	if gate.conn != nil || gate.db != nil {
		t.Fatal("committed freeze left the EXCLUSIVE SQLite gate open")
	}

	archiveAfter, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	if archiveAfter != archiveBefore || archiveAfter != originalDigest {
		t.Fatalf("original archive changed across freeze: before=%s after=%s source=%s", archiveBefore, archiveAfter, originalDigest)
	}

	frozenDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = frozenDB.Close() }()
	if err := validateLegacyV18CutoverFrozenBridge(frozenDB, bridge.FleetID, originalDigest); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := frozenDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != legacyV18CutoverFrozenUserVersion {
		t.Fatalf("frozen user_version = %d, want %d", version, legacyV18CutoverFrozenUserVersion)
	}
	var triggers int
	if err := frozenDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'trigger'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 21 {
		t.Fatalf("frozen bridge triggers = %d, want 21", triggers)
	}
	layout, err := legacyV18LayoutFingerprint(frozenDB)
	if err != nil {
		t.Fatal(err)
	}
	if layout != legacyV072LayoutFingerprint {
		t.Fatalf("frozen bridge base layout = %s, want %s", layout, legacyV072LayoutFingerprint)
	}

	if _, err := staleInsert.Exec("after-freeze", "forbidden"); err == nil || !strings.Contains(err.Error(), legacyV18CutoverFreezeAbortMessage) {
		t.Fatalf("stale prepared writer after freeze = %v, want exact freeze guard abort", err)
	}
	if db, err := Open(home); err == nil {
		_ = db.Close()
		t.Fatal("legacy Open accepted frozen bridge as a supported source")
	}

	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	release, err := Lock(home, MigrationLock, true)
	if err != nil {
		t.Fatalf("MigrationLock after frozen gate close: %v", err)
	}
	release()
}

func TestFreezeLegacyV18CutoverSourceRefusesArchiveMismatchWithoutMutation(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gate.Close() }()
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest := gate.sourceSHA256
	bad := archive
	bad.SHA256 = strings.Repeat("b", 64)
	if bad.SHA256 == beforeDigest {
		bad.SHA256 = strings.Repeat("c", 64)
	}

	bridge, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, bad)
	if err == nil {
		t.Fatal("freeze accepted mismatched original archive evidence")
	}
	if bridge.Committed {
		t.Fatalf("refused freeze reported committed bridge: %+v", bridge)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}
	afterDigest, err := legacyV18CutoverFileSHA256(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("refused freeze changed source bytes: before=%s after=%s", beforeDigest, afterDigest)
	}

	db := openLegacyV18CutoverTestDB(t, home, true)
	defer func() { _ = db.Close() }()
	var version, triggers, certificates int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'trigger'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM meta WHERE key = ?`, legacyV18CutoverFreezeCertificateKey).Scan(&certificates); err != nil {
		t.Fatal(err)
	}
	if version != legacyV072SchemaVersion || triggers != 0 || certificates != 0 {
		t.Fatalf("refused freeze residue: version=%d triggers=%d certificates=%d", version, triggers, certificates)
	}
}

func TestValidateLegacyV18CutoverFrozenBridgeRejectsTamperedGuard(t *testing.T) {
	home := createLegacyV18CutoverTestSource(t)
	setLegacyV18CutoverTestJournalMode(t, home, "DELETE")
	gate, err := acquireLegacyV18CutoverGate(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := promoteLegacyV18CutoverArchiveCandidate(home, gate.archiveCandidate)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	bridge, err := freezeLegacyV18CutoverSource(context.Background(), home, gate, archive)
	if err != nil {
		_ = gate.Close()
		t.Fatal(err)
	}
	if err := gate.Close(); err != nil {
		t.Fatal(err)
	}

	db := openLegacyV18CutoverTestDB(t, home, true)
	name := legacyV18CutoverFreezeTriggerName("task", "INSERT")
	if _, err := db.Exec(`DROP TRIGGER ` + name); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER ` + name + ` BEFORE INSERT ON task BEGIN SELECT RAISE(ABORT, 'tampered freeze guard'); END`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	frozenDB, err := openLegacyV18CutoverSQLite(Path(home), "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = frozenDB.Close() }()
	if err := validateLegacyV18CutoverFrozenBridge(frozenDB, bridge.FleetID, bridge.SourceSHA256); err == nil || !strings.Contains(err.Error(), "semantics do not match exact freeze guard") {
		t.Fatalf("tampered frozen bridge validation = %v, want exact trigger-semantic refusal", err)
	}
}
