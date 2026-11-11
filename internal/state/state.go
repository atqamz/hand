package state

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/flock"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

//go:embed kept.sql
var keptSchema string

//go:embed track.sql
var trackSchema string

//go:embed home.sql
var homeSchema string

//go:embed task_parent.sql
var taskParentSchema string

//go:embed attempt_status.sql
var attemptStatusSchema string

//go:embed alert.sql
var alertSchema string

//go:embed socket.sql
var socketSchema string

var migrations = []string{keptSchema, trackSchema, homeSchema, taskParentSchema, attemptStatusSchema, alertSchema, socketSchema}

const MinSchemaVersion = 16

const SchemaVersion = 23

const lastMigrating = "0.13.0"

var uriPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid")
)

type Store struct {
	db   *sql.DB
	now  func() time.Time
	home string
}

func Open(path string, now func() time.Time) (*Store, error) {
	if v, ok := headerVersion(path); ok && v > SchemaVersion {
		return nil, fmt.Errorf("%w: %s has schema %d, newer than this hand (%d)", ErrInvalid, path, v, SchemaVersion)
	} else if ok && v != 0 && v < MinSchemaVersion {
		return nil, tooOld(path+" has", v)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	defer flock.Release(lock)
	if _, err := flock.Lock(lock, true); err != nil {
		return nil, err
	}
	dsn := "file:" + uriPath.Replace(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: now, home: filepath.Dir(path)}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func headerVersion(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	b := make([]byte, 64)
	if _, err := io.ReadFull(f, b); err != nil || string(b[:16]) != "SQLite format 3\x00" {
		return 0, false
	}
	if w, err := os.Stat(path + "-wal"); err == nil && w.Size() > 0 {
		return 0, false
	}
	return int(binary.BigEndian.Uint32(b[60:64])), true
}

func tooOld(what string, v int) error {
	return fmt.Errorf("%w: %s schema %d, older than this hand reads (%d to %d); hand %s is the last release that migrates it", ErrInvalid, what, v, MinSchemaVersion, SchemaVersion, lastMigrating)
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := migrateTx(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	return err
}

func migrateTx(tx *sql.Tx) error {
	var v int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	switch {
	case v == SchemaVersion:
		return nil
	case v > SchemaVersion:
		return fmt.Errorf("%w: state schema %d is newer than this hand (%d)", ErrInvalid, v, SchemaVersion)
	case v != 0 && v < MinSchemaVersion:
		return tooOld("state", v)
	}
	if v == 0 {
		if _, err := tx.Exec(schema); err != nil {
			return err
		}
		v = MinSchemaVersion
	}
	for _, m := range migrations[v-MinSchemaVersion : SchemaVersion-MinSchemaVersion] {
		if _, err := tx.Exec(m); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	broken := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if broken {
		return fmt.Errorf("%w: migrating to schema %d would break a foreign key", ErrInvalid, SchemaVersion)
	}
	_, err = tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, SchemaVersion))
	return err
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
