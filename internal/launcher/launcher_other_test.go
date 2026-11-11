//go:build !linux

package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoLauncherEntryOutsideLinux(t *testing.T) {
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_DATA_HOME" || k == "HOME" || k == "USERPROFILE" {
			return home
		}
		return ""
	}
	bin := filepath.Join(home, "hand-desktop")
	if skipped, err := Sync(getenv, bin); skipped != "" || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if skipped, err := Remove(getenv); skipped != "" || err != nil {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	if state, _ := Check(getenv, bin); state != Missing {
		t.Fatalf("Check = %q", state)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("left in the home: %v, %v", entries, err)
	}
}
