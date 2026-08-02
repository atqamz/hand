package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// mkFleetDirs creates the data/ and state/ directories home.Resolve requires
// to recognize dir as a fleet home, for fixtures that chdir into a bare temp
// directory without going through hand init.
func mkFleetDirs(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"data", "state"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
