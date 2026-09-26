package state

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed attempt.sql
var attemptSchema string

//go:embed report.sql
var reportSchema string

//go:embed fleet.sql
var fleetSchema string

var migrations = []string{schema, attemptSchema, reportSchema, fleetSchema}

const SchemaVersion = 4

var uriPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
)

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(path string, now func() time.Time) (*Store, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	dsn := "file:" + uriPath.Replace(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: now}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
			return err
		}
		switch {
		case v == SchemaVersion:
			return nil
		case v > SchemaVersion:
			return fmt.Errorf("%w: state schema %d is newer than this hand (%d)", ErrInvalid, v, SchemaVersion)
		}
		for _, m := range migrations[v:SchemaVersion] {
			if _, err := tx.Exec(m); err != nil {
				return err
			}
		}
		_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion))
		return err
	})
}

func (s *Store) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339Nano) }

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
