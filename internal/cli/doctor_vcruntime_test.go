package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMissingDLLsNamesWhatIsAbsent(t *testing.T) {
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
