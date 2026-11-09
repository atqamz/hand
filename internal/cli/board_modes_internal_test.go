package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestATokenReadsOnlyWhenModesCannotBeSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.token")
	token := strings.Repeat("a", 48)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldChmod, oldModes := chmod, modesEnforced
	t.Cleanup(func() { chmod, modesEnforced = oldChmod, oldModes })
	chmod = func(string, os.FileMode) error { return errors.New("operation not supported") }

	modesEnforced = func(string) (bool, error) { return false, nil }
	if got, err := readSecret(path, "board token"); err != nil || got != token {
		t.Fatalf("readSecret = %q, %v; want the token when modes are not enforced", got, err)
	}

	modesEnforced = func(string) (bool, error) { return true, nil }
	if _, err := readSecret(path, "board token"); err == nil {
		t.Fatal("readSecret accepted a token whose modes could not be set on a filesystem that enforces them")
	}
}
