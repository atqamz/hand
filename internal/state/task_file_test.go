package state

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"
)

const (
	fileA = "20261101-050000-1-a.png"
	fileB = "20261101-050000-2-b.log"
)

func TestAVersion27DatabaseMigratesToTaskFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{schema}
	stmts = append(stmts, migrations[:len(migrations)-1]...)
	stmts = append(stmts,
		`PRAGMA user_version = 27`,
		`INSERT INTO project(name, repo, created_at) VALUES ('hand', '/home/me/hand', '2026-09-26T00:00:00Z')`,
		`INSERT INTO task(id, project, title, goal, status, created_at, updated_at) VALUES (1, 'hand', 'one', '', 'inbox', '2026-09-26T00:00:00Z', '2026-09-26T00:00:00Z')`)
	for _, m := range stmts {
		if _, err := db.Exec(m); err != nil {
			t.Fatalf("%.60s: %v", m, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := Open(path, clock)
		if err != nil {
			t.Fatal(err)
		}
		var v, n int
		if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
			t.Fatalf("user_version = %d, %v", v, err)
		}
		if err := s.db.QueryRow(`SELECT count(*) FROM task_file`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("task_file rows = %d, %v", n, err)
		}
		files, err := s.TaskFiles(context.Background(), 1)
		if err != nil || len(files) != 0 {
			t.Fatalf("files = %+v, %v", files, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAttachAndDetachWriteOneEventEach(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 2)
	added, err := s.AttachFiles(ctx, 1, []string{fileA, fileB})
	if err != nil || !slices.Equal(added, []string{fileA, fileB}) {
		t.Fatalf("added %v, %v", added, err)
	}
	if added, err = s.AttachFiles(ctx, 1, []string{fileB, fileA}); err != nil || len(added) != 0 {
		t.Fatalf("a second attach added %v, %v", added, err)
	}
	if ev := eventsOf(t, s, "task.attached"); !slices.Equal(ev, []string{"1 a.png", "1 b.log"}) {
		t.Fatalf("task.attached events = %q", ev)
	}
	files, err := s.TaskFiles(ctx, 1)
	if err != nil || len(files) != 2 || files[0].Name != fileA || files[1].Name != fileB {
		t.Fatalf("files %+v, %v", files, err)
	}
	if err := s.DetachFiles(ctx, 1, []string{fileA}); err != nil {
		t.Fatal(err)
	}
	if ev := eventsOf(t, s, "task.detached"); !slices.Equal(ev, []string{"1 a.png"}) {
		t.Fatalf("task.detached events = %q", ev)
	}
	if err := s.DetachFiles(ctx, 1, []string{fileB, fileA}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detaching a file that is not attached: %v", err)
	}
	if files, _ = s.TaskFiles(ctx, 1); len(files) != 1 || files[0].Name != fileB {
		t.Fatalf("a refused detach must change nothing: %+v", files)
	}
	if ev := eventsOf(t, s, "task.detached"); len(ev) != 1 {
		t.Fatalf("a refused detach left events %q", ev)
	}
	if _, err := s.AttachFiles(ctx, 9, []string{fileA}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("attach to a missing task: %v", err)
	}
	if err := s.DetachFiles(ctx, 9, []string{fileA}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detach from a missing task: %v", err)
	}
	if other, _ := s.TaskFiles(ctx, 2); len(other) != 0 {
		t.Fatalf("task t2 holds %+v", other)
	}
}

func TestOnlyFilesOfAnOpenTaskAreProtected(t *testing.T) {
	s, ctx := seedTree(t)
	addTasks(t, s, 4)
	for id, name := range map[int64]string{
		1: "20261101-050000-1-inbox.png", 2: "20261101-050000-1-active.png",
		3: "20261101-050000-1-done.png", 4: "20261101-050000-1-gone.png",
	} {
		if _, err := s.AttachFiles(ctx, id, []string{name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AttachFiles(ctx, 3, []string{"20261101-050000-1-inbox.png"}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		id int64
		to string
	}{{2, StatusActive}, {3, StatusActive}, {3, StatusDone}, {4, StatusAbandoned}} {
		if _, err := s.Transition(ctx, step.id, step.to); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ProtectedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"20261101-050000-1-inbox.png": true, "20261101-050000-1-active.png": true}
	if len(got) != len(want) || !got["20261101-050000-1-inbox.png"] || !got["20261101-050000-1-active.png"] {
		t.Fatalf("protected %v, want %v", got, want)
	}
	if _, err := s.Transition(ctx, 2, StatusDone); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.ProtectedFiles(ctx); got["20261101-050000-1-active.png"] || len(got) != 1 {
		t.Fatalf("a finished task still protects: %v", got)
	}
}

func TestImportCarriesTheTaskFilesWithTheOffset(t *testing.T) {
	fx := newImportStateFixture(t)
	ctx := context.Background()
	src, err := Open(filepath.Join(fx.srcHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AttachFiles(ctx, 1, []string{fileA, fileB}); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	dst, err := Open(filepath.Join(fx.dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		if _, err := dst.AddTask(ctx, "app", "filler", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	dry := fx.options()
	dry.DryRun = true
	if res, err := ImportFleet(ctx, dry); err != nil || res.TaskFiles != 2 {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task_file`); n != 0 {
		t.Fatalf("a dry run wrote %d rows", n)
	}
	if res, err := ImportFleet(ctx, fx.options()); err != nil || res.TaskFiles != 2 {
		t.Fatalf("import: %+v, %v", res, err)
	}
	offset := 4
	for _, name := range []string{fileA, fileB} {
		q := `SELECT count(*) FROM task_file WHERE task_id = ` + strconv.Itoa(offset+1) + ` AND name = '` + name + `'`
		if n := countImport(t, fx.dstHome, q); n != 1 {
			t.Fatalf("%s not linked to t%d", name, offset+1)
		}
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task_file`); n != 2 {
		t.Fatalf("destination holds %d links, want 2", n)
	}
}
