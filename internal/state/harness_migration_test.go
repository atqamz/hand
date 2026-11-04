package state

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAForeignNewerDatabaseIsRefusedUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE legacy (x INTEGER); PRAGMA user_version = 21`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, clock); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "schema 21") {
		t.Fatalf("open = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the refused database changed: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("open left %d entries beside the database", len(entries))
	}
}

func TestAScreenIsTrimmedToItsEndAndMadeSafe(t *testing.T) {
	long := strings.Repeat("é", 3000) + "\x1b[31mend\x1b]0;title\x07 of\tscreen\u202e\u200b\x00\n"
	got := screenTail(long)
	if len(got) > screenMax || !utf8.ValidString(got) || !strings.HasSuffix(got, "end of screen") || strings.ContainsAny(got, "\x1b\x00\x07\r\u202e\u200b") {
		t.Fatalf("tail = %q", got)
	}
	if got := screenTail("\x1b[2J \n\t\x00"); got != "" {
		t.Fatalf("blank screen kept as %q", got)
	}
	if got := screenTail("a\r\nb\xffc"); got != "a\nbc" {
		t.Fatalf("tail = %q", got)
	}
}

func TestADatabaseOlderThanTheMinimumIsRefusedUntouched(t *testing.T) {
	for _, version := range []int{1, MinSchemaVersion - 1} {
		dir := t.TempDir()
		path := filepath.Join(dir, "hand.db")
		db, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(schema + fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Open(path, clock)
		want := fmt.Sprintf("schema %d, older than this hand reads (%d to %d); hand %s is the last release that migrates it", version, MinSchemaVersion, SchemaVersion, lastMigrating)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("version %d: open = %v", version, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("version %d: the refused database changed: %v", version, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Fatalf("version %d: open left %d entries beside the database", version, len(entries))
		}
	}
}

func TestMigrateRefusesAnOlderSchemaAndRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "hand.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, MinSchemaVersion-1)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := migrateTx(tx); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "last release that migrates it") {
		t.Fatalf("migrateTx = %v", err)
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the refusal created %d objects, %v", n, err)
	}
}

func TestAVersion16HomeKeepsItsRowsAndMigratesToTheCurrentSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		schema,
		fmt.Sprintf(`PRAGMA user_version = %d`, MinSchemaVersion),
		`INSERT INTO fleet(only, id, name) VALUES (1, 'f0123456789ab', 'old')`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/r', 'x')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'One', '', 'active', 'x', 'x')`,
		`INSERT INTO attempt(id, task_id, harness, model, effort, argv, worktree, branch, status, created_at) VALUES (1, 1, 'claude', 'sonnet', 'low', '[]', '/w', 'b', 'exited', 'x')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v, indexes int
	var kept string
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if err := s.db.QueryRow(`SELECT kept FROM attempt WHERE id = 1`).Scan(&kept); err != nil || kept != "" {
		t.Fatalf("kept after the migration = %q, %v", kept, err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'event_kind_task'`).Scan(&indexes); err != nil || indexes != 1 {
		t.Fatalf("event_kind_task indexes = %d, %v", indexes, err)
	}
	if ps, err := s.Projects(context.Background()); err != nil || len(ps) != 1 || ps[0].Name != "hand" {
		t.Fatalf("projects after the migration = %+v, %v", ps, err)
	}
}

func TestAPendingWriteAheadLogHidesTheHeaderVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 13`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if v, ok := headerVersion(path); !ok || v != 13 {
		t.Fatalf("headerVersion = %d, %v", v, ok)
	}
	if err := os.WriteFile(path+"-wal", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := headerVersion(path); ok {
		t.Fatal("a pending write-ahead log left the stale header trusted")
	}
}
