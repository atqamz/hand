//go:build linux

package uninstall

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/launcher"
)

func TestTheRealLauncherEntryGoesOnlyWhenItIsHands(t *testing.T) {
	x := newInstall(t, "linux")
	t.Setenv("PATH", t.TempDir())
	data := filepath.Join(x.home, "data")
	plain := x.opts.Getenv
	x.opts.Getenv = func(k string) string {
		if k == "XDG_DATA_HOME" {
			return data
		}
		return plain(k)
	}
	x.opts.Owned, x.opts.RemoveLauncher = nil, nil
	entry := filepath.Join(data, "applications", "hand-desktop.desktop")
	pic := filepath.Join(data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")
	if _, err := launcher.Sync(x.opts.Getenv, filepath.Join(x.bin, "hand-desktop")); err != nil {
		t.Fatal(err)
	}
	dry, err := x.run(true)
	if err != nil || !slices.Contains(paths(dry.Removed), entry) || !slices.Contains(paths(dry.Removed), pic) {
		t.Fatalf("dry run removed %v, %v", paths(dry.Removed), err)
	}
	there(t, entry, pic)
	if _, err := x.run(false); err != nil {
		t.Fatal(err)
	}
	gone(t, entry, pic, x.target)

	y := newInstall(t, "linux")
	y.opts.Getenv = x.opts.Getenv
	y.opts.Owned, y.opts.RemoveLauncher = nil, nil
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "[Desktop Entry]\nExec=/mine\n"
	if err := os.WriteFile(entry, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := y.run(false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(entry); string(b) != mine {
		t.Fatalf("the user's entry now holds %q", b)
	}
	var note string
	for _, it := range rep.Kept {
		if it.Path == entry {
			note = it.Note
		}
	}
	if !strings.Contains(note, launcher.Marker) {
		t.Fatalf("kept %v", rep.Kept)
	}
}
