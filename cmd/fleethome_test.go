package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/secondhand/internal/dashboard"
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
	marker := filepath.Join(dir, "data", "dashboard.md")
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := os.WriteFile(marker, []byte(dashboardSkeleton), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
}

// dashboard.Parse deliberately recovers only the append-only sections, so a
// test asserting on a derived one has to read the same text an operator reads.
func dashboardSection(t *testing.T, home, title string) []string {
	t.Helper()
	data, err := os.ReadFile(dashboard.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			in = strings.TrimPrefix(trimmed, "## ") == title
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			lines = append(lines, strings.TrimPrefix(trimmed, "- "))
		}
		if strings.HasPrefix(trimmed, "| ") && !strings.HasPrefix(trimmed, "| id |") {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
