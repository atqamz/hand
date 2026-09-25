package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/launch"
)

// A minimal schema carrying only the columns verifyCanonicalV19WorkerInputCredential queries.
// It exists solely for row shapes the anchored schema's CHECKs and immutability triggers can
// never actually produce; every other case runs against the real DDL in the linux-tagged file.
func newWorkerInputCredentialTx(t *testing.T) *sql.Tx {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := `
		CREATE TABLE fleet(singleton INTEGER PRIMARY KEY, fleet_id TEXT NOT NULL);
		CREATE TABLE attempt(id TEXT PRIMARY KEY, lifecycle TEXT NOT NULL, terminal_at TEXT NOT NULL);
		CREATE TABLE executor_binding(id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL,
			launch_operation_id TEXT NOT NULL, provider_executor_key TEXT NOT NULL);
		CREATE TABLE executor_binding_termination(executor_binding_id TEXT PRIMARY KEY);
		CREATE TABLE launch_environment(operation_id TEXT NOT NULL, name TEXT NOT NULL,
			value_kind TEXT NOT NULL, value_material TEXT NOT NULL, value_digest TEXT NOT NULL,
			PRIMARY KEY(operation_id, name));`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

type workerInputCredentialRow struct {
	fleetID             string
	executorBindingID   string
	attemptID           string
	lifecycle           string
	terminalAt          string
	launchOperationID   string
	providerExecutorKey string
	valueKind           string
	valueMaterial       string
	credential          string
}

func defaultWorkerInputCredentialRow() workerInputCredentialRow {
	return workerInputCredentialRow{
		fleetID: "fleet-unit-a", executorBindingID: "binding-unit-1", attemptID: "attempt-unit-1",
		lifecycle: "active", launchOperationID: "operation-unit-1",
		providerExecutorKey: canonicalV19ExecGuardKeyPrefix + "assoc=unobserved",
		valueKind:           "secret-ref", valueMaterial: execguard.Protocol,
		credential: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("\x11", 32))),
	}
}

func seedWorkerInputCredentialRow(t *testing.T, tx *sql.Tx, row workerInputCredentialRow) {
	t.Helper()
	if _, err := tx.Exec(`INSERT INTO fleet(singleton,fleet_id) VALUES(1,?)`, row.fleetID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO attempt(id,lifecycle,terminal_at) VALUES(?,?,?)`,
		row.attemptID, row.lifecycle, row.terminalAt); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO executor_binding(id,attempt_id,launch_operation_id,provider_executor_key) VALUES(?,?,?,?)`,
		row.executorBindingID, row.attemptID, row.launchOperationID, row.providerExecutorKey); err != nil {
		t.Fatal(err)
	}
	digest := launch.ExecGuardCredentialVerifier(row.fleetID, row.executorBindingID, row.credential)
	if _, err := tx.Exec(`INSERT INTO launch_environment(operation_id,name,value_kind,value_material,value_digest) VALUES(?,?,?,?,?)`,
		row.launchOperationID, execguard.CredentialEnv, row.valueKind, row.valueMaterial, digest); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyCanonicalV19WorkerInputCredentialRefusesWrongValueKindOrMaterial(t *testing.T) {
	for _, mutate := range []func(*workerInputCredentialRow){
		func(row *workerInputCredentialRow) { row.valueKind = "literal" },
		func(row *workerInputCredentialRow) { row.valueMaterial = "not-" + execguard.Protocol },
	} {
		tx := newWorkerInputCredentialTx(t)
		row := defaultWorkerInputCredentialRow()
		mutate(&row)
		seedWorkerInputCredentialRow(t, tx, row)
		if _, err := verifyCanonicalV19WorkerInputCredential(context.Background(), tx, row.executorBindingID, row.credential); !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
			t.Fatalf("credential row value_kind=%q value_material=%q error = %v, want attestation refusal", row.valueKind, row.valueMaterial, err)
		}
	}
}

func TestVerifyCanonicalV19WorkerInputCredentialRefusesWrongKeyGrammar(t *testing.T) {
	tx := newWorkerInputCredentialTx(t)
	row := defaultWorkerInputCredentialRow()
	row.providerExecutorKey = "herdr-executor:v1?legacy=1"
	seedWorkerInputCredentialRow(t, tx, row)
	if _, err := verifyCanonicalV19WorkerInputCredential(context.Background(), tx, row.executorBindingID, row.credential); !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) {
		t.Fatalf("credential against a non-guard-grammar key error = %v, want attestation refusal", err)
	}
}
