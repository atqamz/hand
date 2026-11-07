package state

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRewriteImportTextGrammar(t *testing.T) {
	ids := map[int64]bool{2: true, 29: true}
	cases := []struct {
		text string
		want string
		refs []string
		amb  []string
		left []string
	}{
		{"after t2.", "after t31.", []string{"t2"}, nil, nil},
		{"after t2. Next", "after t31. Next", []string{"t2"}, nil, nil},
		{"see t2.png", "see t2.png", nil, []string{"t2.png"}, nil},
		{"read path/t2", "read path/t2", nil, []string{"path/t2"}, nil},
		{"head d5fe72c now", "head d5fe72c now", nil, []string{"d5fe72c"}, nil},
		{"head 29a4fbd now", "head 29a4fbd now", nil, []string{"29a4fbd"}, nil},
		{"see d10 now", "see d10 now", nil, nil, []string{"d10"}},
		{"see t29, then", "see t58, then", []string{"t29"}, nil, nil},
		{"see t99", "see t99", nil, nil, nil},
		{"see t2t29", "see t2t29", nil, []string{"t2t29"}, nil},
		{"see ét2 here", "see ét2 here", nil, []string{"ét2"}, nil},
		{"see 中t2 here", "see 中t2 here", nil, []string{"中t2"}, nil},
		{"see e\u0301t2 here", "see e\u0301t2 here", nil, []string{"e\u0301t2"}, nil},
		{"see t2é here", "see t2é here", nil, []string{"t2é"}, nil},
		{"see t2中 here", "see t2中 here", nil, []string{"t2中"}, nil},
		{"see t2e\u0301 here", "see t2e\u0301 here", nil, []string{"t2e\u0301"}, nil},
		{"see ét2.", "see ét2.", nil, []string{"ét2."}, nil},
	}
	for _, c := range cases {
		got, refs, left, amb := rewriteImportText(c.text, ids, 29, "t1", "title")
		if got != c.want {
			t.Errorf("%q = %q, want %q", c.text, got, c.want)
			continue
		}
		var gotRefs, gotLeft, gotAmb []string
		for _, r := range refs {
			gotRefs = append(gotRefs, r.From)
		}
		for _, r := range left {
			gotLeft = append(gotLeft, r.From)
		}
		for _, a := range amb {
			gotAmb = append(gotAmb, a.Token)
		}
		if !slices.Equal(gotRefs, c.refs) || !slices.Equal(gotLeft, c.left) || !slices.Equal(gotAmb, c.amb) {
			t.Errorf("%q refs=%v left=%v ambiguous=%v, want %v %v %v", c.text, gotRefs, gotLeft, gotAmb, c.refs, c.left, c.amb)
		}
	}
}

type importStateFixture struct {
	srcHome, dstHome string
	srcID, dstID     string
}

func newImportStateFixture(t *testing.T) importStateFixture {
	t.Helper()
	ctx := context.Background()
	srcHome, dstHome, repo := t.TempDir(), t.TempDir(), t.TempDir()
	src, err := Open(filepath.Join(srcHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	source, err := src.CreateFleet(ctx, "source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddProject(ctx, "app", repo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := src.AddTask(ctx, "app", "one", ""); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	dst, err := Open(filepath.Join(dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	dest, err := dst.CreateFleet(ctx, "dest")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dst.AddProject(ctx, "app", repo, ""); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	return importStateFixture{srcHome: srcHome, dstHome: dstHome, srcID: source.ID, dstID: dest.ID}
}

func (fx importStateFixture) options() ImportOptions {
	return ImportOptions{SourceID: fx.srcID, SourceHome: fx.srcHome, DestID: fx.dstID, DestHome: fx.dstHome}
}

func countImport(t *testing.T, home, query string) int {
	t.Helper()
	st, err := Open(filepath.Join(home, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.db.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportFleetRechecksTheMarkerInsideTheTransaction(t *testing.T) {
	fx := newImportStateFixture(t)
	if err := os.WriteFile(ImportMarker(fx.srcHome), []byte("elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ImportFleet(context.Background(), fx.options())
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "marker") {
		t.Fatalf("marker recheck: %v", err)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task`); n != 0 {
		t.Fatalf("tasks imported: %d", n)
	}
}

func TestImportFleetRechecksTheEventInsideTheTransaction(t *testing.T) {
	fx := newImportStateFixture(t)
	st, err := Open(filepath.Join(fx.dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	err = st.tx(context.Background(), func(tx *sql.Tx) error {
		return emit(tx, st.stamp(), "fleet.imported", 0, fx.srcID+" projects 0 tasks 0 offset 0 text x")
	})
	if closeErr := st.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	_, err = ImportFleet(context.Background(), fx.options())
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "fleet.imported event") {
		t.Fatalf("event recheck: %v", err)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task`); n != 0 {
		t.Fatalf("tasks imported: %d", n)
	}
}

func TestImportFleetConcurrentCallsImportOnce(t *testing.T) {
	fx := newImportStateFixture(t)
	const runs = 4
	errs := make(chan error, runs)
	var wg sync.WaitGroup
	for range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := ImportFleet(context.Background(), fx.options())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	imported, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			imported++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent import: %v", err)
		}
	}
	if imported != 1 || conflicts != runs-1 {
		t.Fatalf("imported=%d conflicts=%d, want 1 and %d", imported, conflicts, runs-1)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task`); n != 1 {
		t.Fatalf("tasks = %d, want 1", n)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM event WHERE kind = 'fleet.imported'`); n != 1 {
		t.Fatalf("fleet.imported rows = %d, want 1", n)
	}
}

func TestLockImportRefusesASecondHolder(t *testing.T) {
	home := t.TempDir()
	first, err := LockImport(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockImport(home); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "another fleet import is running") {
		t.Fatalf("second holder: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := LockImport(home)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestImportFleetRefusesATitleOverTheLimitAfterRewrite(t *testing.T) {
	fx := newImportStateFixture(t)
	src, err := Open(filepath.Join(fx.srcHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	title := strings.Repeat("a", 197) + " t1"
	if _, err := src.db.Exec(`UPDATE task SET title = ? WHERE id = 1`, title); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	dst, err := Open(filepath.Join(fx.dstHome, "hand.db"), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 9 {
		if _, err := dst.AddTask(context.Background(), "app", "filler "+strconv.Itoa(i), ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = ImportFleet(context.Background(), fx.options())
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "t1") || !strings.Contains(err.Error(), "201") {
		t.Fatalf("overlong title: %v", err)
	}
	if n := countImport(t, fx.dstHome, `SELECT count(*) FROM task`); n != 9 {
		t.Fatalf("tasks = %d, want the 9 destination tasks only", n)
	}
}
