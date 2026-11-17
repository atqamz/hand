package fsx_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atqamz/hand/internal/fsx"
)

func TestLinksCountThePathAndTheOpenHandle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	if err := os.WriteFile(a, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := func(when string, want uint64) {
		t.Helper()
		if n, err := fsx.Links(a); err != nil || n != want {
			t.Errorf("%s: Links = %d, %v, want %d", when, n, err, want)
		}
		if n, err := fsx.FileLinks(f); err != nil || n != want {
			t.Errorf("%s: FileLinks = %d, %v, want %d", when, n, err, want)
		}
	}
	count("one name", 1)
	if err := os.Link(a, filepath.Join(dir, "b")); err != nil {
		if runtime.GOOS == "windows" {
			t.Fatalf("hard link: %v", err)
		}
		t.Skipf("no hard links here: %v", err)
	}
	count("two names", 2)
	if err := os.Remove(filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	count("one name again", 1)
}
