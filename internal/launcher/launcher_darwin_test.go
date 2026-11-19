//go:build darwin

package launcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinLauncherWritesTheBundleForTheRunningHand(t *testing.T) {
	home := t.TempDir()
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	app := filepath.Join(home, "Applications", "Hand.app")
	bin := filepath.Join(app, "Contents", "MacOS", "hand-desktop")
	if state, where := Check(getenv, bin); state != Missing || where != app {
		t.Fatalf("Check = %q, %q", state, where)
	}
	if skipped, err := Sync(getenv, bin); skipped != "" || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(app, "Contents", "MacOS", "hand")); filepath.Base(target) != filepath.Base(self) {
		t.Fatalf("MacOS/hand -> %q, the running binary is %q", target, self)
	}
	if state, where := Check(getenv, bin); state != OK {
		t.Fatalf("Check = %q, %q", state, where)
	}
	if skipped, err := Remove(getenv); skipped != "" || err != nil {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "Applications")); len(entries) != 0 {
		t.Fatalf("left in Applications: %v", entries)
	}
}
