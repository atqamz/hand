package board

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAPathSwappedAfterTheCheckIsNotOpened(t *testing.T) {
	home := t.TempDir()
	for rel, data := range map[string]string{"hand.db": "database", "reports/design.md": "checked", "reports/x.md": "report", "projects/x.md": "clone"} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(rel string, swap func()) (string, error) {
		t.Helper()
		root, real, _, err := Resolve(home, rel)
		if err != nil {
			t.Fatal(err)
		}
		swap()
		f, _, err := openSame(home, rel, root, real)
		if err != nil {
			return "", err
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		return string(b), err
	}
	if got, err := read("reports/design.md", func() {}); err != nil || got != "checked" {
		t.Fatalf("unchanged = %q, %v", got, err)
	}
	if got, err := read("reports/design.md", func() {
		if err := os.WriteFile(filepath.Join(home, "reports", "design.md.new"), []byte("saved"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(home, "reports", "design.md.new"), filepath.Join(home, "reports", "design.md")); err != nil {
			t.Fatal(err)
		}
	}); err != nil || got != "saved" {
		t.Fatalf("an editor's save by rename = %q, %v", got, err)
	}
	link := func(target, at string) {
		if err := os.Rename(at, at+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, at); err != nil {
			t.Skip("no symlinks here:", err)
		}
	}
	if got, err := read("reports/design.md", func() { link("../hand.db", filepath.Join(home, "reports", "design.md")) }); !errors.Is(err, errChanged) {
		t.Fatalf("a file swapped for a link to hand.db = %q, %v", got, err)
	}
	if got, err := read("reports/x.md", func() { link("projects", filepath.Join(home, "reports")) }); !errors.Is(err, errChanged) {
		t.Fatalf("a folder swapped for a link to projects/ = %q, %v", got, err)
	}
}
