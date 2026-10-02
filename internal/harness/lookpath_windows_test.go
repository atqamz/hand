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
	if err := os.WriteFile(filepath.Join(dir, "foo"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATHEXT", "EXE;.CMD")
	got, err := LookPath("foo", dir)
	if err != nil || got != want {
		t.Fatalf("LookPath = %q, %v; want %q", got, err, want)
	}
	if got, err := LookPath("foo.cmd", dir); err != nil || got != want {
		t.Fatalf("LookPath with extension = %q, %v; want %q", got, err, want)
	}
}
