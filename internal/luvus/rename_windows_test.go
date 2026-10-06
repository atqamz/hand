package luvus

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameWaitsForAHandleToClose(t *testing.T) {
	for _, held := range []string{"source", "target"} {
		t.Run(held, func(t *testing.T) {
			dir := t.TempDir()
			paths := map[string]string{"source": filepath.Join(dir, "src"), "target": filepath.Join(dir, "dst")}
			for _, p := range paths {
				if err := os.WriteFile(p, []byte(filepath.Base(p)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			f, err := os.Open(paths[held])
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := os.Rename(paths["source"], paths["target"]); err == nil {
				t.Fatal("os.Rename succeeded over an open handle, so this test holds nothing")
			}
			time.AfterFunc(100*time.Millisecond, func() { f.Close() })
			if err := rename(paths["source"], paths["target"]); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(paths["target"]); err != nil || string(got) != "src" {
				t.Fatalf("target = %q, %v", got, err)
			}
		})
	}
}
