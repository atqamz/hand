package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

const (
	legacyV072SchemaVersion     = 21
	legacyV072SchemaFingerprint = "discover-in-ci"
)

// SchemaFamily names the persistence family found at state/hand.db without
// mutating, migrating, or otherwise reconciling it.
type SchemaFamily string

const (
	// SchemaFamilyMissing means state/hand.db does not exist.
	SchemaFamilyMissing SchemaFamily = "missing"
	// SchemaFamilyEmpty means the SQLite file exists but has no user schema objects.
	SchemaFamilyEmpty SchemaFamily = "empty"
	// SchemaFamilyLegacyV18 means the database carries Hand's pre-v19 schema family.
	SchemaFamilyLegacyV18 SchemaFamily = "legacy-v18"
	// SchemaFamilyCanonicalV19 means the database carries canonical-v19 relations.
	SchemaFamilyCanonicalV19 SchemaFamily = "canonical-v19"
	// SchemaFamilyUnknown means the database is neither a recognized legacy nor v19 family.
	SchemaFamilyUnknown SchemaFamily = "unknown"
)

// SchemaInfo is the non-mutating persistence identity observed for one state database.
type SchemaInfo struct {
	Family      SchemaFamily
	UserVersion int
	Fingerprint string
	Tables      int
	Indexes     int
	Triggers    int
}

// ErrUnsupportedLegacyV18Schema marks a legacy-family database that is not the
// exact v0.7.2 source contract accepted by the v18-to-v19 cutover.
var ErrUnsupportedLegacyV18Schema = errors.New("legacy v18 schema is not the exact v0.7.2 cutover source")

// ErrUnknownSchema marks a non-empty database outside both recognized Hand schema families.
var ErrUnknownSchema = errors.New("state database schema family is unknown")

// InspectSchema opens an existing state database read-only and classifies its
// schema family. It never creates, migrates, checkpoints, or repairs state.
func InspectSchema(homeDir string) (SchemaInfo, error) {
	if _, err := os.Stat(Path(homeDir)); os.IsNotExist(err) {
		return SchemaInfo{Family: SchemaFamilyMissing}, nil
	} else if err != nil {
		return SchemaInfo{}, fmt.Errorf("check %s: %w", Path(homeDir), err)
	}

	db, err := openReadOnly(homeDir)
	if err != nil {
		return SchemaInfo{}, err
	}
	defer func() { _ = db.Close() }()
	return inspectSchemaFamily(db.sql)
}

func inspectSchemaFamily(sqlDB *sql.DB) (SchemaInfo, error) {
	version, err := schemaUserVersion(sqlDB)
	if err != nil {
		return SchemaInfo{}, err
	}
	identity, err := inspectCanonicalV19Identity(sqlDB)
	if err != nil {
		return SchemaInfo{}, err
	}
	info := SchemaInfo{
		UserVersion: version,
		Fingerprint: identity.Fingerprint,
		Tables:      identity.Tables,
		Indexes:     identity.Indexes,
		Triggers:    identity.Triggers,
	}

	var objects int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&objects); err != nil {
		return SchemaInfo{}, fmt.Errorf("inspect schema objects: %w", err)
	}
	if objects == 0 {
		info.Family = SchemaFamilyEmpty
		return info, nil
	}

	canonical, err := canonicalV19Candidate(sqlDB)
	if err != nil {
		return SchemaInfo{}, err
	}
	if canonical {
		info.Family = SchemaFamilyCanonicalV19
		if err := validateCanonicalV19Schema(sqlDB); err != nil {
			return info, err
		}
		return info, nil
	}

	legacy, err := legacyV18Candidate(sqlDB)
	if err != nil {
		return SchemaInfo{}, err
	}
	if legacy {
		info.Family = SchemaFamilyLegacyV18
		if version != legacyV072SchemaVersion {
			return info, fmt.Errorf("%w: PRAGMA user_version = %d, want %d", ErrUnsupportedLegacyV18Schema, version, legacyV072SchemaVersion)
		}
		if identity.Fingerprint != legacyV072SchemaFingerprint {
			return info, fmt.Errorf("%w: schema fingerprint = %s, want %s", ErrUnsupportedLegacyV18Schema, identity.Fingerprint, legacyV072SchemaFingerprint)
		}
		return info, nil
	}

	info.Family = SchemaFamilyUnknown
	return info, ErrUnknownSchema
}

func schemaUserVersion(sqlDB *sql.DB) (int, error) {
	var version int
	if err := sqlDB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema user_version: %w", err)
	}
	return version, nil
}

func legacyV18Candidate(sqlDB *sql.DB) (bool, error) {
	var meta int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'table' AND name = 'meta'`).Scan(&meta); err != nil {
		return false, fmt.Errorf("detect legacy v18 meta relation: %w", err)
	}
	return meta == 1, nil
}
