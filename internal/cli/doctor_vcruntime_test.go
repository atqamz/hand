package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func vcRuntimeRows(t *testing.T, root string) [][]string {
	t.Helper()
	r := &runner{env: Env{Getenv: func(k string) string {
		if k == "SystemRoot" {
			return root
		}
		return ""
	}}}
	var c checks
	r.doctorVCRuntime(&c)
	if c.failed != 0 {
		t.Fatalf("failed = %d, rows = %q", c.failed, c.rows)
	}
	return c.rows
}

func TestDoctorVCRuntimeRow(t *testing.T) {
	t.Parallel()
	if rows := vcRuntimeRows(t, ""); len(rows) != 0 {
		t.Fatalf("no SystemRoot: %q", rows)
	}
	root := t.TempDir()
	sys := filepath.Join(root, "System32")
	if err := os.Mkdir(sys, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vcruntime140.dll", "msvcp140.dll"} {
		if err := os.WriteFile(filepath.Join(sys, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows := vcRuntimeRows(t, root)
	if len(rows) != 1 || rows[0][0] != "visual c++ runtime" || rows[0][1] != "warn" || !strings.Contains(rows[0][2], "vcruntime140_1.dll missing") || strings.Contains(rows[0][2], "msvcp140.dll") || !strings.Contains(rows[0][2], "winget install Microsoft.VCRedist.2015+.x64") || strings.Contains(rows[0][2], "hand luvus pin") {
		t.Fatalf("one DLL absent: %q", rows)
	}
	if err := os.WriteFile(filepath.Join(sys, "vcruntime140_1.dll"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if rows := vcRuntimeRows(t, root); !reflect.DeepEqual(rows, [][]string{{"visual c++ runtime", "ok", ""}}) {
		t.Fatalf("all present: %q", rows)
	}
}

func TestMissingDLLsNamesWhatIsAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if got := missingDLLs(dir); !reflect.DeepEqual(got, vcRuntimeDLLs) {
		t.Fatalf("empty folder: %v", got)
	}
	for _, name := range []string{"vcruntime140.dll", "msvcp140.dll"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := missingDLLs(dir); !reflect.DeepEqual(got, []string{"vcruntime140_1.dll"}) {
		t.Fatalf("one absent: %v", got)
	}
	os.WriteFile(filepath.Join(dir, "vcruntime140_1.dll"), nil, 0o644)
	if got := missingDLLs(dir); len(got) != 0 {
		t.Fatalf("all present: %v", got)
	}
}
