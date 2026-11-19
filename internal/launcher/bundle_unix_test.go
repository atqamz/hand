//go:build unix

package launcher

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleRemoveMovesHandAppAsideBeforeDeletingIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root deletes a read-only folder's files")
	}
	x := newTree(t)
	x.sync(t)
	locked := filepath.Join(x.b.app, "Contents", "Resources", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		entries, _ := os.ReadDir(filepath.Dir(x.b.app))
		for _, e := range entries {
			_ = filepath.WalkDir(filepath.Join(filepath.Dir(x.b.app), e.Name()), func(p string, d os.DirEntry, _ error) error {
				if d != nil && d.IsDir() {
					_ = os.Chmod(p, 0o755)
				}
				return nil
			})
		}
	})
	skipped, err := bundleRemove(x.home)
	if skipped != "" || err == nil || !strings.Contains(err.Error(), "was moved to") {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	if _, err := os.Lstat(x.b.app); !os.IsNotExist(err) {
		t.Fatalf("a Remove that failed part way left Hand.app in place: %v", err)
	}
}
