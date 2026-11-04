package retention

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func tree(t *testing.T, files map[string]int) string {
	t.Helper()
	home := t.TempDir()
	for rel, days := range files {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("12345"), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := now.AddDate(0, 0, -days)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func paths(files []File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

func TestScanSeesOnlyReportsAndShots(t *testing.T) {
	home := tree(t, map[string]int{
		"reports/t1/a.md":   40,
		"reports/t1/b/c.md": 2,
		"shots/x.png":       31,
		"memory/m.md":       400,
		"briefs/b.md":       400,
		"inbox/i.png":       400,
	})
	files, err := Scan(home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"reports/t1/a.md", "reports/t1/b/c.md", "shots/x.png"}
	if got := paths(files); !slices.Equal(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
	if got := paths(Older(files, now, 30*24*time.Hour)); !slices.Equal(got, []string{"reports/t1/a.md", "shots/x.png"}) {
		t.Fatalf("older = %v", got)
	}
	if Bytes(files) != 15 {
		t.Fatalf("bytes = %d", Bytes(files))
	}
}

func TestScanIgnoresALinkInsideAFolder(t *testing.T) {
	home := tree(t, map[string]int{"reports/t1/a.md": 40})
	outside := tree(t, map[string]int{"reports/x.md": 90})
	if err := os.Symlink(outside, filepath.Join(home, "reports", "t1", "link")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	files, err := Scan(home)
	if err != nil || !slices.Equal(paths(files), []string{"reports/t1/a.md"}) {
		t.Fatalf("scan = %v, %v", paths(files), err)
	}
}

func TestScopeAndGroups(t *testing.T) {
	for _, bad := range []string{"memory", "briefs/b.md", "../reports", "reports/../memory", "/reports", ""} {
		if s, err := Scope(bad); err == nil {
			t.Fatalf("Scope(%q) = %q, want an error", bad, s)
		}
	}
	if s, err := Scope("reports/t1/"); err != nil || s != "reports/t1" {
		t.Fatalf("Scope = %q, %v", s, err)
	}
	files := []File{
		{"reports/t1/a.md", 5, now.AddDate(0, 0, -50)},
		{"reports/t1/b/c.md", 7, now.AddDate(0, 0, -40)},
		{"reports/t10/d.md", 1, now},
		{"shots/x.png", 100, now},
	}
	if got := paths(Within(files, []string{"reports/t1"})); !slices.Equal(got, []string{"reports/t1/a.md", "reports/t1/b/c.md"}) {
		t.Fatalf("within = %v", got)
	}
	g := Groups(files)
	if len(g) != 3 || g[0].Path != "shots/x.png" || g[1].Path != "reports/t1" || g[1].Files != 2 || g[1].Bytes != 12 || !g[1].Oldest.Equal(files[0].Mod) {
		t.Fatalf("groups = %+v", g)
	}
}

func TestRemoveDeletesOnlyTheListedFilesAndEmptiedFolders(t *testing.T) {
	home := tree(t, map[string]int{
		"reports/t1/old/a.md": 40,
		"reports/t2/old.md":   40,
		"reports/t2/new.md":   1,
		"memory/m.md":         400,
	})
	files, err := Scan(home)
	if err != nil {
		t.Fatal(err)
	}
	removed, failed := Remove(home, Older(files, now, 30*24*time.Hour))
	if len(failed) != 0 || len(removed) != 2 {
		t.Fatalf("removed = %v, failed = %v", removed, failed)
	}
	for _, gone := range []string{"reports/t1", "reports/t2/old.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(gone))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s still there: %v", gone, err)
		}
	}
	for _, kept := range []string{"reports", "reports/t2/new.md", "memory/m.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("%s lost: %v", kept, err)
		}
	}
}

func TestScanRefusesAFolderThatIsALink(t *testing.T) {
	home := tree(t, map[string]int{"shots/a.png": 40})
	outside := tree(t, map[string]int{"keep.md": 40})
	if err := os.RemoveAll(filepath.Join(home, "shots")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "shots")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if files, err := Scan(home); err == nil || len(files) != 0 {
		t.Fatalf("scan = %v, %v; want a refusal", paths(files), err)
	}
}

func TestRemoveSkipsAChangedFileAndNeverLeavesItsFolder(t *testing.T) {
	home := tree(t, map[string]int{
		"reports/t1/a.md": 40,
		"reports/t2/b.md": 40,
		"reports/t3/c.md": 40,
		"memory/c.md":     400,
	})
	files, err := Scan(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "reports", "t1", "a.md"), []byte("rewritten after the listing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "reports", "t3")); err != nil {
		t.Fatal(err)
	}
	linked := os.Symlink(filepath.Join(home, "memory"), filepath.Join(home, "reports", "t3")) == nil
	removed, failed := Remove(home, files)
	if got := paths(removed); !slices.Equal(got, []string{"reports/t2/b.md"}) && linked {
		t.Fatalf("removed = %v, failed = %v", got, failed)
	}
	for _, kept := range []string{"reports/t1/a.md", "memory/c.md"} {
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(kept))); err != nil {
			t.Fatalf("%s lost: %v", kept, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "reports", "t2")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("reports/t2 stayed: %v", err)
	}
}

func TestRemoveSkipsAFileRewrittenWithTheSameSize(t *testing.T) {
	home := tree(t, map[string]int{"reports/t1/a.md": 40})
	files, err := Scan(home)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, "reports", "t1", "a.md")
	if err := os.WriteFile(p, []byte("54321"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, now, now); err != nil {
		t.Fatal(err)
	}
	removed, failed := Remove(home, files)
	if len(removed) != 0 || len(failed) != 0 {
		t.Fatalf("removed = %v, failed = %v", paths(removed), failed)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("rewritten file lost: %v", err)
	}
}

func TestRemoveReportsAFolderThatVanishedAfterTheScan(t *testing.T) {
	home := tree(t, map[string]int{"reports/t1/a.md": 40, "shots/b.png": 40})
	files, err := Scan(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, "reports")); err != nil {
		t.Fatal(err)
	}
	removed, failed := Remove(home, files)
	if got := paths(removed); !slices.Equal(got, []string{"shots/b.png"}) || len(failed) != 1 || failed[0].Path != "reports/t1/a.md" {
		t.Fatalf("removed = %v, failed = %v", got, failed)
	}
}
