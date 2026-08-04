package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// Lays down the markers home.Resolve requires to recognize dir as a fleet home, for fixtures that chdir
// into a bare temp directory without going through hand init.
func mkFleetDirs(t *testing.T, dir string) {
	t.Helper()
	// Neutralizes an ambient HAND_HOME, which would otherwise outrank the fixture and point the command
	// under test at the developer's real fleet.
	t.Setenv("HAND_HOME", "")
	// Both marker sets are written, not just state/hand.db: tests that fault the store by turning
	// state/hand.db into a directory would otherwise stop being homes at all, and the command would fail on
	// home resolution before ever reaching the fault under test.
	for _, sub := range []string{"data", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	projects := filepath.Join(dir, "data", "projects.md")
	if _, err := os.Stat(projects); os.IsNotExist(err) {
		if err := os.WriteFile(projects, []byte("# Projects\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "state", "hand.db")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		// Empty, not garbage bytes: sqlite treats a 0-byte file as a fresh
		// database to initialize, and every test using this fixture goes on
		// to open it for real via state.Write/state.Read.
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
}
