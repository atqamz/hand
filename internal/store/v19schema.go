package store

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	canonicalV19SchemaVersion = 19

	canonicalV19GzipBytes  = 10841
	canonicalV19DDLBytes   = 103445
	canonicalV19GzipSHA256 = "6e92cb72ad52c135a0cb8ae8f6352f1ff2c938a289ae1e313a9ce1d6a9e42399"
	canonicalV19DDLSHA256  = "81118c2e982be7e08c0f8bf3bbb980e2ec4c5bffbbc7d419e9952732ad36c58a"

	canonicalV19SchemaFingerprint = "8726f0875845d610553928e6bb56fc5566019a6667d81e29a94ee3d3d45ef3b8"
	canonicalV19TableCount        = 55
	canonicalV19IndexCount        = 38
	canonicalV19TriggerCount      = 169
)

var ErrCanonicalV19SchemaMismatch = errors.New("canonical v19 schema does not match locked #344 contract")

var ErrCanonicalV19TargetNotEmpty = errors.New("canonical v19 target database is not empty")

// v19.sql.gz is an implementation mirror of the exact #344 authority at
// docs/architecture/v19.sql.gz. The two repository paths intentionally point at
// the same Git blob; the tests below the store package verify both compressed
// and reconstructed SHA-256 values before production code may execute it.
//
//go:embed v19.sql.gz
var canonicalV19Gzip []byte

type canonicalV19Identity struct {
	Fingerprint string
	Tables      int
	Indexes     int
	Triggers    int
}

func canonicalV19Mismatch(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCanonicalV19SchemaMismatch, fmt.Sprintf(format, args...))
}

func canonicalV19SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalV19DDL() ([]byte, error) {
	if len(canonicalV19Gzip) != canonicalV19GzipBytes {
		return nil, canonicalV19Mismatch("compressed byte count = %d, want %d", len(canonicalV19Gzip), canonicalV19GzipBytes)
	}
	if got := canonicalV19SHA256(canonicalV19Gzip); got != canonicalV19GzipSHA256 {
		return nil, canonicalV19Mismatch("compressed SHA-256 = %s, want %s", got, canonicalV19GzipSHA256)
	}

	reader, err := gzip.NewReader(bytes.NewReader(canonicalV19Gzip))
	if err != nil {
		return nil, canonicalV19Mismatch("open compressed DDL: %v", err)
	}
	ddl, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, canonicalV19Mismatch("read compressed DDL: %v", readErr)
	}
	if closeErr != nil {
		return nil, canonicalV19Mismatch("close compressed DDL: %v", closeErr)
	}
	if len(ddl) != canonicalV19DDLBytes {
		return nil, canonicalV19Mismatch("DDL byte count = %d, want %d", len(ddl), canonicalV19DDLBytes)
	}
	if got := canonicalV19SHA256(ddl); got != canonicalV19DDLSHA256 {
		return nil, canonicalV19Mismatch("DDL SHA-256 = %s, want %s", got, canonicalV19DDLSHA256)
	}
	return ddl, nil
}

// createCanonicalV19Schema executes the exact locked #344 DDL only against an
// empty database. Cutover will build this into a temporary sibling and publish
// that file only after its own archive/import/durability proof succeeds.
func createCanonicalV19Schema(sqlDB *sql.DB) error {
	var objects int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		return fmt.Errorf("inspect canonical v19 target: %w", err)
	}
	if objects != 0 {
		return fmt.Errorf("%w: found %d existing schema objects", ErrCanonicalV19TargetNotEmpty, objects)
	}

	ddl, err := canonicalV19DDL()
	if err != nil {
		return err
	}
	if _, err := sqlDB.Exec(string(ddl)); err != nil {
		return fmt.Errorf("create canonical v19 schema: %w", err)
	}
	return validateCanonicalV19Schema(sqlDB)
}

// canonicalV19Candidate distinguishes canonical v19 from the legacy migration
// ladder. This cannot use PRAGMA user_version alone: legacy v18 migration
// versions overlap the canonical value 19. Canonical-only relation names are a
// positive discriminator; migration-version 19 with the legacy meta table is
// still v18 and must stay on the legacy path.
func canonicalV19Candidate(sqlDB *sql.DB) (bool, error) {
	var canonicalMarkers int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema
		WHERE type = 'table' AND name IN ('plan', 'external_operation', 'worker_input', 'task_hold', 'attempt_backoff', 'repair')`).Scan(&canonicalMarkers); err != nil {
		return false, fmt.Errorf("detect canonical v19 relations: %w", err)
	}
	if canonicalMarkers != 0 {
		return true, nil
	}

	var version int
	if err := sqlDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return false, fmt.Errorf("detect canonical v19 version: %w", err)
	}
	if version != canonicalV19SchemaVersion {
		return false, nil
	}

	var userObjects int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&userObjects); err != nil {
		return false, fmt.Errorf("detect canonical v19 schema objects: %w", err)
	}
	if userObjects == 0 {
		return false, nil
	}

	var legacyMeta int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'meta'`).Scan(&legacyMeta); err != nil {
		return false, fmt.Errorf("detect legacy v18 meta table: %w", err)
	}
	return legacyMeta == 0, nil
}

func validateCanonicalV19Schema(sqlDB *sql.DB) error {
	var foreignKeys int
	if err := sqlDB.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("inspect canonical v19 foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		return canonicalV19Mismatch("PRAGMA foreign_keys = %d, want 1", foreignKeys)
	}

	var version int
	if err := sqlDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("inspect canonical v19 user_version: %w", err)
	}
	if version != canonicalV19SchemaVersion {
		return canonicalV19Mismatch("PRAGMA user_version = %d, want %d", version, canonicalV19SchemaVersion)
	}

	var integrity string
	if err := sqlDB.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("check canonical v19 integrity: %w", err)
	}
	if integrity != "ok" {
		return canonicalV19Mismatch("integrity_check = %q, want %q", integrity, "ok")
	}

	foreignKeyRows, err := sqlDB.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check canonical v19 foreign keys: %w", err)
	}
	hasForeignKeyViolation := foreignKeyRows.Next()
	rowsErr := foreignKeyRows.Err()
	closeErr := foreignKeyRows.Close()
	if rowsErr != nil {
		return fmt.Errorf("check canonical v19 foreign keys: %w", rowsErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close canonical v19 foreign-key check: %w", closeErr)
	}
	if hasForeignKeyViolation {
		return canonicalV19Mismatch("foreign_key_check returned at least one row")
	}

	identity, err := inspectCanonicalV19Identity(sqlDB)
	if err != nil {
		return err
	}
	if identity.Fingerprint != canonicalV19SchemaFingerprint {
		return canonicalV19Mismatch("schema fingerprint = %s, want %s", identity.Fingerprint, canonicalV19SchemaFingerprint)
	}
	if identity.Tables != canonicalV19TableCount || identity.Indexes != canonicalV19IndexCount || identity.Triggers != canonicalV19TriggerCount {
		return canonicalV19Mismatch("schema objects = %d tables / %d indexes / %d triggers, want %d / %d / %d",
			identity.Tables, identity.Indexes, identity.Triggers,
			canonicalV19TableCount, canonicalV19IndexCount, canonicalV19TriggerCount)
	}
	return nil
}

func inspectCanonicalV19Identity(sqlDB *sql.DB) (canonicalV19Identity, error) {
	rows, err := sqlDB.Query(`SELECT type, name, tbl_name, sql
		FROM sqlite_schema
		WHERE name NOT LIKE 'sqlite_%' AND sql IS NOT NULL
		ORDER BY type, name`)
	if err != nil {
		return canonicalV19Identity{}, fmt.Errorf("read canonical v19 schema identity: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hash := sha256.New()
	identity := canonicalV19Identity{}
	for rows.Next() {
		var typ, name, table, ddl string
		if err := rows.Scan(&typ, &name, &table, &ddl); err != nil {
			return canonicalV19Identity{}, fmt.Errorf("read canonical v19 schema identity: %w", err)
		}
		_, _ = hash.Write([]byte(typ + "|" + name + "|" + table + "|" + ddl + "\n"))
		switch typ {
		case "table":
			identity.Tables++
		case "index":
			identity.Indexes++
		case "trigger":
			identity.Triggers++
		}
	}
	if err := rows.Err(); err != nil {
		return canonicalV19Identity{}, fmt.Errorf("read canonical v19 schema identity: %w", err)
	}
	identity.Fingerprint = hex.EncodeToString(hash.Sum(nil))
	return identity, nil
}
