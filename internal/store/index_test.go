package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDoc(t *testing.T, home, rel, body string) string {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func openIndex(t *testing.T, home string) *Index {
	t.Helper()
	ix, err := OpenIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ix.Close() })
	return ix
}

func search(t *testing.T, ix *Index, query string) []Hit {
	t.Helper()
	hits, err := ix.Search(query, 10)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func TestRefreshIndexesTheCorpusAndSearchFindsIt(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/fix-login/brief.md", "# fix-login\n\nThe session token expires early.\n")
	writeDoc(t, home, "data/learnings.md", "# Fleet learnings\n\nNever force-push the default branch.\n")

	ix := openIndex(t, home)
	sync, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if sync.Indexed != 2 || sync.Total != 2 {
		t.Fatalf("Sync = %+v", sync)
	}

	hits := search(t, ix, "session token")
	if len(hits) != 1 || hits[0].Path != filepath.Join("data", "fix-login", "brief.md") {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Title != "fix-login" {
		t.Fatalf("Title = %q, want the first heading", hits[0].Title)
	}
	if !strings.Contains(hits[0].Snippet, "token") {
		t.Fatalf("Snippet = %q", hits[0].Snippet)
	}
}

func TestRefreshSkipsUnchangedFiles(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")

	ix := openIndex(t, home)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	sync, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if sync.Indexed != 0 || sync.Removed != 0 || sync.Total != 1 {
		t.Fatalf("second Refresh = %+v, want nothing re-read", sync)
	}
}

func TestRefreshPicksUpEditsAndDeletions(t *testing.T) {
	home := t.TempDir()
	path := writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")
	gone := writeDoc(t, home, "data/b.md", "# B\n\nbravo\n")

	ix := openIndex(t, home)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("# A\n\ncharlie\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	sync, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if sync.Indexed != 1 || sync.Removed != 1 {
		t.Fatalf("Sync = %+v", sync)
	}

	if hits := search(t, ix, "alpha"); len(hits) != 0 {
		t.Fatalf("stale body still matches: %+v", hits)
	}
	if hits := search(t, ix, "charlie"); len(hits) != 1 {
		t.Fatalf("edited body not indexed: %+v", hits)
	}
	if hits := search(t, ix, "bravo"); len(hits) != 0 {
		t.Fatalf("deleted file still in the index: %+v", hits)
	}
}

// The whole recovery story for a corrupt index is deleting it, so the path has to
// work on a file sqlite cannot even open - not just on a stale one.
func TestRebuildRecoversFromACorruptIndex(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")

	ix, err := OpenIndex(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := ix.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IndexPath(home), []byte("this is not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	sync, err := Rebuild(home)
	if err != nil {
		t.Fatal(err)
	}
	if sync.Total != 1 || sync.Indexed != 1 {
		t.Fatalf("Rebuild = %+v", sync)
	}

	rebuilt := openIndex(t, home)
	if hits := search(t, rebuilt, "alpha"); len(hits) != 1 {
		t.Fatalf("rebuilt index does not answer: %+v", hits)
	}
}

// Derived means deletable: nothing in machine state may depend on the index
// surviving, and nothing in the corpus may be lost with it.
func TestDeletingTheIndexCostsNeitherMachineStateNorCorpus(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")

	db, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTask(sampleTask()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Rebuild(home); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(IndexPath(home)); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if _, found, err := reopened.ReadTask("fix-login"); err != nil || !found {
		t.Fatalf("machine state did not survive index deletion: %v, %v", found, err)
	}
	if _, err := os.Stat(filepath.Join(home, "data", "a.md")); err != nil {
		t.Fatalf("corpus did not survive index deletion: %v", err)
	}

	if _, err := Rebuild(home); err != nil {
		t.Fatalf("rebuild after deletion failed: %v", err)
	}
	if hits := search(t, openIndex(t, home), "alpha"); len(hits) != 1 {
		t.Fatalf("rebuild did not restore the answer: %+v", hits)
	}
}

// A home initialized before the dashboard was deleted still carries its last
// render, which no command refreshes any more, so it must stay out of the
// corpus rather than answer searches out of a frozen snapshot.
func TestIndexSkipsTheStaleDashboard(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/dashboard.md", "# Dashboard\n\nzebra\n")
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")

	ix := openIndex(t, home)
	sync, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if sync.Total != 1 {
		t.Fatalf("Total = %d, want the dashboard skipped", sync.Total)
	}
	if hits := search(t, ix, "zebra"); len(hits) != 0 {
		t.Fatalf("indexed a generated file: %+v", hits)
	}
}

// Fleet vocabulary is full of hyphens, and FTS5 reads `no-mistakes gate` as a
// column filter unless every token is quoted first.
func TestSearchTreatsOperatorSyntaxAsLiteralText(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nThe no-mistakes gate refused the run.\n")

	ix := openIndex(t, home)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	for _, query := range []string{"no-mistakes gate", `gate"`, "gate: refused", "AND"} {
		if _, err := ix.Search(query, 10); err != nil {
			t.Errorf("Search(%q) = %v, want no syntax error", query, err)
		}
	}
	if hits := search(t, ix, "no-mistakes gate"); len(hits) != 1 {
		t.Fatalf("hyphenated query found nothing: %+v", hits)
	}
}

func TestSearchOnAnEmptyQueryReturnsNothing(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")
	ix := openIndex(t, home)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if hits := search(t, ix, "   "); len(hits) != 0 {
		t.Fatalf("hits = %+v", hits)
	}
}

func TestRefreshOnAHomeWithNoCorpus(t *testing.T) {
	home := t.TempDir()
	ix := openIndex(t, home)
	sync, err := ix.Refresh()
	if err != nil {
		t.Fatal(err)
	}
	if sync.Total != 0 {
		t.Fatalf("Sync = %+v", sync)
	}
}

// A doc row carries the size and mtime that let Refresh skip the file, so a row
// without matching text would be a file silently unsearchable until a rebuild.
func TestEveryIndexedDocHasItsText(t *testing.T) {
	home := t.TempDir()
	writeDoc(t, home, "data/a.md", "# A\n\nalpha\n")
	writeDoc(t, home, "data/b.md", "# B\n\nbravo\n")

	ix := openIndex(t, home)
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "data", "b.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Refresh(); err != nil {
		t.Fatal(err)
	}

	var orphans int
	row := ix.sql.QueryRow(`SELECT count(*) FROM doc WHERE path NOT IN (SELECT path FROM doc_fts)`)
	if err := row.Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("got %d doc rows with no text", orphans)
	}
	row = ix.sql.QueryRow(`SELECT count(*) FROM doc_fts WHERE path NOT IN (SELECT path FROM doc)`)
	if err := row.Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Fatalf("got %d indexed texts with no doc row", orphans)
	}
}
