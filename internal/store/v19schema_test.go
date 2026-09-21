package store

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalV19EmbeddedArtifactMatchesLockedAuthority(t *testing.T) {
	ddl, err := canonicalV19DDL()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(canonicalV19Gzip); got != canonicalV19GzipBytes {
		t.Fatalf("compressed bytes = %d, want %d", got, canonicalV19GzipBytes)
	}
	if got := canonicalV19SHA256(canonicalV19Gzip); got != canonicalV19GzipSHA256 {
		t.Fatalf("compressed SHA-256 = %s, want %s", got, canonicalV19GzipSHA256)
	}
	if got := len(ddl); got != canonicalV19DDLBytes {
		t.Fatalf("DDL bytes = %d, want %d", got, canonicalV19DDLBytes)
	}
	if got := canonicalV19SHA256(ddl); got != canonicalV19DDLSHA256 {
		t.Fatalf("DDL SHA-256 = %s, want %s", got, canonicalV19DDLSHA256)
	}
}

func TestCreateCanonicalV19SchemaMatchesLockedContract(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := createCanonicalV19Schema(sqlDB); err != nil {
		t.Fatal(err)
	}
	candidate, err := canonicalV19Candidate(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if !candidate {
		t.Fatal("exact canonical v19 schema was not recognized as canonical v19")
	}
	identity, err := inspectCanonicalV19Identity(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Fingerprint != canonicalV19SchemaFingerprint {
		t.Fatalf("schema fingerprint = %s, want %s", identity.Fingerprint, canonicalV19SchemaFingerprint)
	}
	if identity.Tables != canonicalV19TableCount || identity.Indexes != canonicalV19IndexCount || identity.Triggers != canonicalV19TriggerCount {
		t.Fatalf("schema objects = %d tables / %d indexes / %d triggers, want %d / %d / %d",
			identity.Tables, identity.Indexes, identity.Triggers,
			canonicalV19TableCount, canonicalV19IndexCount, canonicalV19TriggerCount)
	}
}

func TestCreateCanonicalV19SchemaRequiresEmptyTarget(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if _, err := sqlDB.Exec(`CREATE TABLE already_here (id TEXT PRIMARY KEY) STRICT`); err != nil {
		t.Fatal(err)
	}
	if err := createCanonicalV19Schema(sqlDB); !errors.Is(err, ErrCanonicalV19TargetNotEmpty) {
		t.Fatalf("createCanonicalV19Schema error = %v, want ErrCanonicalV19TargetNotEmpty", err)
	}
}

func TestValidateCanonicalV19SchemaRejectsDrift(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := createCanonicalV19Schema(sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`CREATE TABLE schema_drift (id TEXT PRIMARY KEY) STRICT`); err != nil {
		t.Fatal(err)
	}
	if err := validateCanonicalV19Schema(sqlDB); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("validateCanonicalV19Schema error = %v, want ErrCanonicalV19SchemaMismatch", err)
	}
}

func TestValidateCanonicalV19SchemaRejectsPriorExactFingerprint(t *testing.T) {
	for _, test := range []struct {
		name        string
		fingerprint string
		downgrade   string
	}{
		{
			name:        "revision 2",
			fingerprint: canonicalV19PriorSchemaFingerprintV2,
			downgrade:   `DROP INDEX worker_report_attempt_source_order`,
		},
		{
			name:        "revision 1",
			fingerprint: canonicalV19PriorSchemaFingerprintV1,
			downgrade: `DROP INDEX worker_report_attempt_source_order;
				DROP TRIGGER task_archive_insert_guard;
				DROP TRIGGER task_archive_no_update;
				DROP TRIGGER task_archive_no_delete;
				DROP TABLE task_archive`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			sqlDB, err := open(Path(home))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sqlDB.Close() }()

			if err := createCanonicalV19Schema(sqlDB); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlDB.Exec(test.downgrade); err != nil {
				t.Fatal(err)
			}
			identity, err := inspectCanonicalV19Identity(sqlDB)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Fingerprint != test.fingerprint {
				t.Fatalf("reconstructed prior fingerprint = %s, want %s", identity.Fingerprint, test.fingerprint)
			}
			err = validateCanonicalV19Schema(sqlDB)
			if !errors.Is(err, ErrCanonicalV19SchemaMismatch) || !strings.Contains(err.Error(), "prior v19 fingerprint") {
				t.Fatalf("prior v19 validation error = %v, want explicit fail-closed mismatch", err)
			}
		})
	}
}

func TestLegacyMigrationVersion19IsNotCanonicalV19(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	db := &DB{sql: sqlDB, home: home}
	if err := db.createSchema(true, len(migrations)); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 19`); err != nil {
		t.Fatal(err)
	}
	candidate, err := canonicalV19Candidate(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if candidate {
		t.Fatal("legacy v18 migration version 19 was misclassified as canonical v19")
	}
}

func TestCanonicalV19CandidateSurvivesWrongVersionForFailClosedValidation(t *testing.T) {
	home := t.TempDir()
	sqlDB, err := open(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := createCanonicalV19Schema(sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`PRAGMA user_version = 18`); err != nil {
		t.Fatal(err)
	}
	candidate, err := canonicalV19Candidate(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if !candidate {
		t.Fatal("canonical v19 relations with a corrupted version stamp were not recognized as a v19 candidate")
	}
	if err := validateCanonicalV19Schema(sqlDB); !errors.Is(err, ErrCanonicalV19SchemaMismatch) {
		t.Fatalf("validateCanonicalV19Schema error = %v, want ErrCanonicalV19SchemaMismatch", err)
	}
}
