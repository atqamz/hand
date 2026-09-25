//go:build linux

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/osfacts"
)

type canonicalV19ExecGuardBinding struct {
	ID       string
	Key      string
	Parsed   canonicalV19ExecGuardKey
	Dir      string
	Launch   string
	Terminal string
}

type canonicalV19ExecGuardUnresolvedInterrupt struct {
	ID, State, ChangedAt string
}

func readCanonicalV19ExecGuardBinding(ctx context.Context, homeDir, bindingID string) (canonicalV19ExecGuardBinding, error) {
	binding := canonicalV19ExecGuardBinding{ID: bindingID}
	db, err := openReadOnly(homeDir)
	if err != nil {
		return binding, err
	}
	defer func() { _ = db.Close() }()
	if err := db.sql.QueryRowContext(ctx, `SELECT e.launch_operation_id,e.provider_executor_key,COALESCE(x.terminal_kind,'')
		FROM executor_binding e LEFT JOIN executor_binding_termination x ON x.executor_binding_id=e.id
		WHERE e.id=? AND e.adapter_ref=?`, bindingID, CanonicalV19HerdrSessionAdapterRef).Scan(
		&binding.Launch, &binding.Key, &binding.Terminal); err != nil {
		return binding, fmt.Errorf("read exec-guard ExecutorBinding %q: %w", bindingID, err)
	}
	if binding.Parsed, err = parseCanonicalV19ExecGuardKey(binding.Key); err != nil {
		return binding, fmt.Errorf("ExecutorBinding %q has no exec-guard key: %w", bindingID, err)
	}
	if binding.Parsed.Guard == nil || binding.Parsed.Root == nil {
		return binding, fmt.Errorf("ExecutorBinding %q names no guard incarnation and harness root", bindingID)
	}
	binding.Dir, err = canonicalV19ExecGuardDir(homeDir, binding.Launch)
	return binding, err
}

func ceaseCanonicalV19ExecGuardBinding(
	ctx context.Context,
	homeDir string,
	binding canonicalV19ExecGuardBinding,
	now func() time.Time,
) (kind string, liveness osfacts.Observation, err error) {
	if binding.Terminal != "" {
		return binding.Terminal, "", nil
	}
	ceased, liveness, err := observeCanonicalV19ExecGuardCessation(binding)
	if err != nil || (ceased == nil && liveness != osfacts.BootChanged) {
		return "", liveness, err
	}
	kind, err = terminateCanonicalV19ExecGuardBinding(ctx, homeDir, binding, ceased, now)
	return kind, liveness, err
}

func observeCanonicalV19ExecGuardCessation(binding canonicalV19ExecGuardBinding) (*execguard.Record, osfacts.Observation, error) {
	read := func() (*execguard.Record, error) {
		record, err := execguard.ReadRecord(binding.Dir, execguard.KindCeased)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil || record.LaunchOperationID != binding.Launch ||
			!canonicalV19ExecGuardCeasedTree(record, *binding.Parsed.Guard, *binding.Parsed.Root) {
			return nil, err
		}
		return &record, nil
	}
	ceased, err := read()
	if ceased != nil {
		return ceased, "", nil
	}
	liveness := osfacts.Observe(*binding.Parsed.Guard)
	if err == nil && liveness != osfacts.Alive {
		// G writes ceased before it exits, so a re-read after seeing it gone is complete.
		ceased, err = read()
	}
	if liveness == osfacts.BootChanged {
		return ceased, liveness, nil
	}
	return ceased, liveness, err
}

func canonicalV19ExecGuardCeasedTree(ceased execguard.Record, guard, root osfacts.Incarnation) bool {
	causes := []string{execguard.CauseHarnessExit, execguard.CauseInterruptRequest, execguard.CauseHangup, execguard.CauseExternalTermination}
	return ceased.Guard == guard && ceased.Root != nil && *ceased.Root == root &&
		ceased.Predicate == "wait4-echild" && ceased.Exit != nil && slices.Contains(causes, ceased.Cause)
}

// The first writer of B's termination fixes its kind. Every Interrupt still unresolved
// for B then settles succeeded in the same transaction, since B ceased whatever the cause.
func terminateCanonicalV19ExecGuardBinding(
	ctx context.Context,
	homeDir string,
	binding canonicalV19ExecGuardBinding,
	ceased *execguard.Record,
	now func() time.Time,
) (string, error) {
	sqlDB, err := openCanonicalV19Writer(homeDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = sqlDB.Close() }()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return "", canonicalV19ExecGuardCessationError("begin writer", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := validateCanonicalV19WriterTransaction(ctx, tx); err != nil {
		return "", canonicalV19ExecGuardCessationError("validate writer", err)
	}
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT terminal_kind FROM executor_binding_termination
		WHERE executor_binding_id=e.id),'') FROM executor_binding e WHERE e.id=? AND e.provider_executor_key=?`,
		binding.ID, binding.Key).Scan(&kind)
	if err != nil {
		return "", canonicalV19ExecGuardCessationError("read the exact observed ExecutorBinding", err)
	}
	unresolved, err := readCanonicalV19ExecGuardUnresolvedInterrupts(ctx, tx, binding.ID)
	if err != nil {
		return "", canonicalV19ExecGuardCessationError("read unresolved Interrupts", err)
	}
	evidence := canonicalV19ExecGuardCessationEvidence(binding, ceased)
	if kind == "" {
		pending := ""
		for _, operation := range unresolved {
			if operation.State != "prepared" {
				pending = operation.ID
			}
		}
		kind = "provider-gone"
		if ceased != nil {
			kind = execguard.TerminalKind(*ceased, pending)
		}
		var interrupt any
		if kind == "interrupted" {
			interrupt = pending
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO executor_binding_termination(
			executor_binding_id,terminal_kind,interrupt_operation_id,observed_at,evidence_digest
		) VALUES(?,?,?,?,?)`, binding.ID, kind, interrupt, now().UTC().Format(time.RFC3339Nano), evidence); err != nil {
			return "", canonicalV19ExecGuardCessationError("insert the observed termination", err)
		}
	}
	for _, operation := range unresolved {
		at := canonicalV19HerdrSessionTimestampAfter(now(), operation.ChangedAt)
		result, err := tx.ExecContext(ctx, `UPDATE external_operation
			SET state='succeeded',state_changed_at=?,state_evidence_digest=?,finalized_at=?
			WHERE id=? AND state=?`, at, evidence, at, operation.ID, operation.State)
		if err != nil {
			return "", canonicalV19ExecGuardCessationError("settle an unresolved Interrupt", err)
		}
		if err := requireCanonicalV19InterruptOneChanged(result, "settle", operation.ID); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", canonicalV19ExecGuardCessationError("commit writer", err)
	}
	committed = true
	return kind, nil
}

func readCanonicalV19ExecGuardUnresolvedInterrupts(ctx context.Context, tx *sql.Tx, bindingID string) ([]canonicalV19ExecGuardUnresolvedInterrupt, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.state,o.state_changed_at FROM interrupt_operation i
		JOIN external_operation o ON o.id=i.operation_id AND o.kind='interrupt'
		WHERE i.executor_binding_id=? AND o.state IN ('prepared','submitted','uncertain')`, bindingID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var unresolved []canonicalV19ExecGuardUnresolvedInterrupt
	for rows.Next() {
		var operation canonicalV19ExecGuardUnresolvedInterrupt
		if err := rows.Scan(&operation.ID, &operation.State, &operation.ChangedAt); err != nil {
			return nil, err
		}
		unresolved = append(unresolved, operation)
	}
	return unresolved, rows.Err()
}

func canonicalV19ExecGuardCessationEvidence(binding canonicalV19ExecGuardBinding, ceased *execguard.Record) string {
	record, _ := json.Marshal(ceased)
	hash := sha256.New()
	writeCanonicalV19DigestField(hash, "domain", "hand:v19:herdr-exec-guard-cessation-observation:v1")
	writeCanonicalV19DigestField(hash, "executor_binding_id", binding.ID)
	writeCanonicalV19DigestField(hash, "provider_executor_key", binding.Key)
	writeCanonicalV19DigestField(hash, "ceased", string(record))
	return hex.EncodeToString(hash.Sum(nil))
}

func canonicalV19ExecGuardCessationError(action string, err error) error {
	if isSQLiteBusy(err) {
		err = ErrContention
	}
	return fmt.Errorf("close exec-guard ExecutorBinding: %s: %w", action, err)
}
