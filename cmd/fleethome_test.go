package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFleetDirs lays down the state/ directory and data/dashboard.md marker
// home.Resolve requires to recognize dir as a fleet home, for fixtures that
// chdir into a bare temp directory without going through hand init. A
// dashboard the fixture already wrote is left alone, since tests assert
// against their own skeleton. It also neutralizes an ambient HAND_HOME, which
// would otherwise outrank the fixture and point the command under test at the
// developer's real fleet.
func mkFleetDirs(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HAND_HOME", "")
	for _, sub := range []string{"data", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dashboard := filepath.Join(dir, "data", "dashboard.md")
	if _, err := os.Stat(dashboard); os.IsNotExist(err) {
		if err := os.WriteFile(dashboard, []byte(dashboardSkeleton), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
}
