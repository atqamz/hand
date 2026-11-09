package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestATokenReadsWhenTheFilesystemCannotSetModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.token")
	token := strings.Repeat("a", 48)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := chmod
	chmod = func(string, os.FileMode) error { return errors.New("operation not supported") }
	t.Cleanup(func() { chmod = old })
	if got, err := readSecret(path, "board token"); err != nil || got != token {
		t.Fatalf("readSecret = %q, %v; want the token", got, err)
	}
	if enforced, err := modesEnforced(t.TempDir()); err != nil || enforced {
		t.Fatalf("modesEnforced = %v, %v; want false and no error", enforced, err)
	}
}
