package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLookPathUsesPathext(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	cmd, exe := filepath.Join(first, "foo.cmd"), filepath.Join(second, "foo.exe")
	for _, p := range []string{cmd, filepath.Join(first, "foo"), exe} {
		if err := os.WriteFile(p, []byte("@exit 0\r\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := first + string(os.PathListSeparator) + second
	t.Setenv("PATHEXT", "EXE;.CMD")
	if got, err := LookPath("foo", path); err != nil || got != exe {
		t.Fatalf("LookPath = %q, %v; want %q", got, err, exe)
	}
	var be *BatchFileError
	if got, err := LookPath("foo.cmd", path); !errors.As(err, &be) || got != "" || be.Path != cmd {
		t.Fatalf("LookPath with extension = %q, %v; want a batch file error for %s", got, err, cmd)
	}
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	if got, err := LookPath("foo", path); !errors.As(err, &be) || got != "" || be.Path != cmd {
		t.Fatalf("LookPath with only a shim = %q, %v; want a batch file error for %s", got, err, cmd)
	}
}
