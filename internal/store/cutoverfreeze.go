package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	legacyV18CutoverFrozenUserVersion        = 22
	legacyV18CutoverFreezeCertificateKey     = "v19-cutover-freeze"
	legacyV18CutoverFreezeCertificateVersion = "v1"
	legacyV18CutoverFreezeAbortMessage       = "legacy source frozen for v19 cutover"
)

var legacyV18CutoverFreezeTables = []string{
	"task",
	"attempt",
	"fleet_identity",
	"meta",
	"hold",
	"project",
	"send_attempt",
}

var legacyV18CutoverFreezeOperations = []string{"INSERT", "UPDATE", "DELETE"}

type legacyV18CutoverFrozenBridge struct {
	MigrationID  string
	FleetID      string
	SourceSHA256 string
	BridgeSHA256 string
	Certificate  string
	Committed    bool
}

func freezeLegacyV18CutoverSource(ctx context.Context, homeDir string, gate *legacyV18CutoverGate, archive legacyV18CutoverOriginalArchive) (legacyV18CutoverFrozenBridge, error) {
	bridge := legacyV18CutoverFrozenBridge{}
	if gate == nil || gate.conn == nil || gate.db == nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: EXCLUSIVE gate is not held")
	}
	if err := validateLegacyV18CutoverMigrationID(archive.MigrationID); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(archive.SHA256); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: %w", err)
	}
	if archive.MigrationID != gate.archiveCandidate.MigrationID {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: archive migration identity=%s, gate=%s", archive.MigrationID, gate.archiveCandidate.MigrationID)
	}
	if archive.SHA256 != gate.sourceSHA256 || archive.SHA256 != gate.archiveCandidate.SHA256 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: archive digest=%s, gate source=%s, candidate=%s", archive.SHA256, gate.sourceSHA256, gate.archiveCandidate.SHA256)
	}
	expectedArchivePath := legacyV18CutoverOriginalArchivePath(homeDir, archive.MigrationID)
	if archive.Path != expectedArchivePath || archive.Directory != legacyV18CutoverOriginalArchiveDir(homeDir, archive.MigrationID) {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: original archive path is not deterministic")
	}
	if err := requireLegacyV18CutoverDirectRegularFile(archive.Path, "original archive"); err != nil {
		return bridge, err
	}
	archiveDigest, err := legacyV18CutoverFileSHA256(archive.Path)
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: hash original archive: %w", err)
	}
	if archiveDigest != archive.SHA256 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: original archive digest=%s, want %s", archiveDigest, archive.SHA256)
	}

	queryer := sqliteConnQueryer{ctx: ctx, conn: gate.conn}
	info, err := validateLegacyV18CutoverSource(queryer)
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: revalidate EXCLUSIVE source: %w", err)
	}
	if info != gate.info {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: EXCLUSIVE source identity changed: gate=%+v current=%+v", gate.info, info)
	}
	fleetID, err := legacyV18CutoverFleetID(queryer)
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: %w", err)
	}
	expectedMigrationID, err := legacyV18CutoverMigrationIdentity(fleetID, gate.sourceSHA256)
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: derive migration identity: %w", err)
	}
	if expectedMigrationID != archive.MigrationID {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: migration identity=%s, want %s from exact Fleet/source evidence", archive.MigrationID, expectedMigrationID)
	}
	activeDigest, err := legacyV18CutoverFileSHA256(Path(homeDir))
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: hash active source: %w", err)
	}
	if activeDigest != gate.sourceSHA256 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: active digest=%s, want %s", activeDigest, gate.sourceSHA256)
	}
	var queryOnly int
	if err := gate.conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: inspect query_only: %w", err)
	}
	if queryOnly != 1 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: query_only=%d, want 1 before freeze", queryOnly)
	}

	bridge = legacyV18CutoverFrozenBridge{
		MigrationID:  archive.MigrationID,
		FleetID:      fleetID,
		SourceSHA256: gate.sourceSHA256,
		Certificate:  legacyV18CutoverFreezeCertificateVersion + ":" + gate.sourceSHA256,
	}
	if _, err := gate.conn.ExecContext(ctx, `PRAGMA query_only = 0`); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: disable query_only: %w", err)
	}
	if err := gate.conn.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: verify query_only disabled: %w", err)
	}
	if queryOnly != 0 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: query_only=%d, want 0 at freeze boundary", queryOnly)
	}

	var existing int
	if err := gate.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM meta WHERE key = ?`, legacyV18CutoverFreezeCertificateKey).Scan(&existing); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: inspect freeze certificate: %w", err)
	}
	if existing != 0 {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: freeze certificate already exists")
	}
	if _, err := gate.conn.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)`, legacyV18CutoverFreezeCertificateKey, bridge.Certificate); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: write freeze certificate: %w", err)
	}
	for _, table := range legacyV18CutoverFreezeTables {
		for _, operation := range legacyV18CutoverFreezeOperations {
			if _, err := gate.conn.ExecContext(ctx, legacyV18CutoverFreezeTriggerSQL(table, operation)); err != nil {
				return bridge, fmt.Errorf("freeze legacy v18 cutover source: create %s %s guard: %w", table, operation, err)
			}
		}
	}
	if _, err := gate.conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, legacyV18CutoverFrozenUserVersion)); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: set frozen bridge user_version: %w", err)
	}
	if _, err := gate.conn.ExecContext(ctx, `COMMIT`); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: commit frozen bridge: %w", err)
	}
	bridge.Committed = true

	closeErr := closeCommittedLegacyV18CutoverGate(gate)
	if err := syncLegacyV18CutoverFile(Path(homeDir)); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; flush active source: %w", err)
	}
	if err := syncLegacyV18CutoverDirectoryParent(Path(homeDir)); err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; flush active source directory: %w", err)
	}
	bridgeDigest, err := legacyV18CutoverFileSHA256(Path(homeDir))
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; hash active bridge: %w", err)
	}
	bridge.BridgeSHA256 = bridgeDigest

	frozenDB, err := openLegacyV18CutoverSQLite(Path(homeDir), "ro", legacyV18CutoverGateTimeout, true)
	if err != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; reopen: %w", err)
	}
	validationErr := validateLegacyV18CutoverFrozenBridge(frozenDB, fleetID, gate.sourceSHA256)
	frozenCloseErr := frozenDB.Close()
	if validationErr != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; validate: %w", validationErr)
	}
	if frozenCloseErr != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; close validation DB: %w", frozenCloseErr)
	}
	if closeErr != nil {
		return bridge, fmt.Errorf("freeze legacy v18 cutover source: frozen bridge committed; close EXCLUSIVE gate: %w", closeErr)
	}
	return bridge, nil
}

func closeCommittedLegacyV18CutoverGate(gate *legacyV18CutoverGate) error {
	var firstErr error
	if gate.conn != nil {
		if err := gate.conn.Close(); err != nil {
			firstErr = err
		}
		gate.conn = nil
	}
	if gate.db != nil {
		if err := gate.db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		gate.db = nil
	}
	return firstErr
}

func legacyV18CutoverFreezeTriggerName(table, operation string) string {
	return "v19_freeze_" + table + "_" + operation
}

func legacyV18CutoverFreezeTriggerSQL(table, operation string) string {
	return fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, '%s'); END",
		legacyV18CutoverFreezeTriggerName(table, operation), operation, table, legacyV18CutoverFreezeAbortMessage)
}

func validateLegacyV18CutoverFrozenBridge(q sqliteQueryer, expectedFleetID, expectedSourceSHA256 string) error {
	if err := validateFleetID(expectedFleetID); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: expected Fleet ID: %w", err)
	}
	if err := validateLegacyV18CutoverSHA256(expectedSourceSHA256); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	version, err := schemaUserVersion(q)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	if version != legacyV18CutoverFrozenUserVersion {
		return fmt.Errorf("validate legacy v18 frozen bridge: user_version=%d, want %d", version, legacyV18CutoverFrozenUserVersion)
	}
	layoutFingerprint, err := legacyV18LayoutFingerprint(q)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	if layoutFingerprint != legacyV072LayoutFingerprint {
		return fmt.Errorf("validate legacy v18 frozen bridge: base layout fingerprint=%s, want %s", layoutFingerprint, legacyV072LayoutFingerprint)
	}
	if err := validateLegacyV18DDLFeatures(q); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	identity, err := inspectCanonicalV19Identity(q)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	if identity.Tables != legacyV072TableCount || identity.Indexes != legacyV072IndexCount || identity.Triggers != len(legacyV18CutoverFreezeTables)*len(legacyV18CutoverFreezeOperations) {
		return fmt.Errorf("validate legacy v18 frozen bridge: schema objects=%d tables/%d indexes/%d triggers, want %d/%d/%d",
			identity.Tables, identity.Indexes, identity.Triggers,
			legacyV072TableCount, legacyV072IndexCount, len(legacyV18CutoverFreezeTables)*len(legacyV18CutoverFreezeOperations))
	}
	fleetID, err := legacyV18CutoverFleetID(q)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: %w", err)
	}
	if fleetID != expectedFleetID {
		return fmt.Errorf("validate legacy v18 frozen bridge: Fleet ID=%s, want %s", fleetID, expectedFleetID)
	}
	var certificate string
	if err := q.QueryRow(`SELECT value FROM meta WHERE key = ?`, legacyV18CutoverFreezeCertificateKey).Scan(&certificate); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: read freeze certificate: %w", err)
	}
	expectedCertificate := legacyV18CutoverFreezeCertificateVersion + ":" + expectedSourceSHA256
	if certificate != expectedCertificate {
		return fmt.Errorf("validate legacy v18 frozen bridge: freeze certificate=%q, want %q", certificate, expectedCertificate)
	}
	if err := validateLegacyV18CutoverFreezeTriggers(q); err != nil {
		return err
	}
	var integrity string
	if err := q.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: integrity_check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("validate legacy v18 frozen bridge: integrity_check=%q, want ok", integrity)
	}
	rows, err := q.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: foreign_key_check: %w", err)
	}
	hasViolation := rows.Next()
	rowsErr := rows.Err()
	closeErr := rows.Close()
	if rowsErr != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: foreign_key_check: %w", rowsErr)
	}
	if closeErr != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: close foreign_key_check: %w", closeErr)
	}
	if hasViolation {
		return fmt.Errorf("validate legacy v18 frozen bridge: foreign_key_check returned at least one row")
	}
	return nil
}

func validateLegacyV18CutoverFreezeTriggers(q sqliteQueryer) error {
	expected := make(map[string]string, len(legacyV18CutoverFreezeTables)*len(legacyV18CutoverFreezeOperations))
	for _, table := range legacyV18CutoverFreezeTables {
		for _, operation := range legacyV18CutoverFreezeOperations {
			name := legacyV18CutoverFreezeTriggerName(table, operation)
			expected[name] = compactLegacyV18DDL(legacyV18CutoverFreezeTriggerSQL(table, operation))
		}
	}
	rows, err := q.Query(`SELECT name, sql FROM sqlite_schema WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: read freeze triggers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	seen := make(map[string]bool, len(expected))
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			return fmt.Errorf("validate legacy v18 frozen bridge: read freeze triggers: %w", err)
		}
		want, ok := expected[name]
		if !ok {
			return fmt.Errorf("validate legacy v18 frozen bridge: unexpected trigger %q", name)
		}
		if !ddl.Valid || compactLegacyV18DDL(ddl.String) != want {
			return fmt.Errorf("validate legacy v18 frozen bridge: trigger %q semantics do not match exact freeze guard", name)
		}
		seen[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate legacy v18 frozen bridge: read freeze triggers: %w", err)
	}
	if len(seen) != len(expected) {
		var missing []string
		for name := range expected {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		return fmt.Errorf("validate legacy v18 frozen bridge: missing freeze triggers: %s", strings.Join(missing, ", "))
	}
	return nil
}
