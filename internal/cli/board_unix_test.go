//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheBoardTokenIsPrivate(t *testing.T) {
	h := initWithProject(t)
	path := filepath.Join(h.home, "board.token")
	base, stop := startBoard(t, h, "127.0.0.1")
	fleetPage(t, base, h)
	stop()
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new token = %v, %v", info, err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 48)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	base, stop = startBoard(t, h, "127.0.0.1")
	fleetPage(t, base, h)
	stop()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, want 0600", info.Mode().Perm())
	}
}
