package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathPathext(t *testing.T) {
	dir := t.TempDir()
	want := filepath.Join(dir, "foo.cmd")
	if err := os.WriteFile(want, []byte("@exit 0\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATHEXT", ".EXE;.CMD")
	got, err := LookPath("foo", dir)
	if err != nil || got != want {
		t.Fatalf("LookPath = %q, %v; want %q", got, err, want)
	}
}
