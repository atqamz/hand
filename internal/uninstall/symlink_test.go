//go:build unix

package uninstall

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestASymlinkOrAFolderInPlaceOfAFileIsKept(t *testing.T) {
	x := newInstall(t, "linux")
	outside := put(t, filepath.Join(t.TempDir(), "precious"), "keep")
	iroh := filepath.Join(x.bin, "hand-iroh")
	window := filepath.Join(x.bin, "hand-desktop")
	for _, p := range []string{iroh, window} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, iroh); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(window, "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	rep, err := x.run(false)
	if err != nil {
		t.Fatal(err)
	}
	there(t, iroh, outside, filepath.Join(window, "mine"))
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Fatalf("the file behind the symlink holds %q", b)
	}
	if kept := paths(rep.Kept); !slices.Contains(kept, iroh) || !slices.Contains(kept, window) {
		t.Fatalf("kept = %v", kept)
	}
	if removed := paths(rep.Removed); slices.Contains(removed, iroh) || slices.Contains(removed, window) {
		t.Fatalf("removed = %v", removed)
	}
}
