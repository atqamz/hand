package board

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAFileSwappedAfterTheCheckIsNotOpened(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, "reports", "design.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("checked"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, real, fi, err := Resolve(home, "reports/design.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p, p+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("swapped"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := openSame(root, real, fi); !errors.Is(err, errChanged) {
		if f != nil {
			f.Close()
		}
		t.Fatalf("openSame after a swap = %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p+".old", p); err != nil {
		t.Fatal(err)
	}
	f, err := openSame(root, real, fi)
	if err != nil {
		t.Fatalf("openSame of the checked file = %v", err)
	}
	f.Close()
}
