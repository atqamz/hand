//go:build windows

package launcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type winFixture struct {
	getenv func(string) string
	lnk    string
	bin    string
	writes int
}

func newWin(t *testing.T) *winFixture {
	t.Helper()
	roaming := t.TempDir()
	f := &winFixture{
		getenv: func(k string) string {
			if k == "APPDATA" {
				return roaming
			}
			return ""
		},
		bin: filepath.Join(t.TempDir(), "bin", "hand-desktop.exe"),
	}
	f.lnk = filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")
	real := writeLnk
	writeLnk = func(env []string) error { f.writes++; return real(env) }
	t.Cleanup(func() { writeLnk = real })
	return f
}

func (f *winFixture) foreign(t *testing.T) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.lnk), 0o755); err != nil {
		t.Fatal(err)
	}
	err := writeLnk([]string{"HAND_LNK=" + f.lnk, "HAND_TARGET=" + f.bin, "HAND_DIR=" + filepath.Dir(f.bin), "HAND_ICON=" + filepath.Join(filepath.Dir(f.bin), "hand.ico"), "HAND_DESC=my own shortcut"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.lnk)
	if err != nil {
		t.Fatal(err)
	}
	f.writes = 0
	return b
}

func TestSyncWritesTheShortcutAndTheIconAndCheckSeesThem(t *testing.T) {
	f := newWin(t)
	if state, _ := Check(f.getenv, f.bin); state != Missing {
		t.Fatalf("Check before = %q", state)
	}
	if skipped, err := Sync(f.getenv, f.bin); skipped != "" || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if state, where := Check(f.getenv, f.bin); state != OK {
		t.Fatalf("Check = %q at %s", state, where)
	}
	b, err := os.ReadFile(f.lnk)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := readLnk(b)
	if !ok || s.Name != description || !strings.EqualFold(s.WorkDir, filepath.Dir(f.bin)) {
		t.Fatalf("the shortcut Windows wrote reads as %+v, %v", s, ok)
	}
	if ico, err := os.ReadFile(icoPath(f.bin)); err != nil || string(ico) != string(iconFile()) {
		t.Fatalf("hand.ico = %d bytes, %v", len(ico), err)
	}
	if f.writes != 1 {
		t.Fatalf("PowerShell ran %d times", f.writes)
	}
	if _, err := Sync(f.getenv, f.bin); err != nil || f.writes != 1 {
		t.Fatalf("a repeat Sync ran PowerShell again: %d, %v", f.writes, err)
	}
}

func TestSyncRewritesAShortcutThatPointsElsewhere(t *testing.T) {
	f := newWin(t)
	if _, err := Sync(f.getenv, f.bin); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "hand-desktop.exe")
	if state, _ := Check(f.getenv, moved); state != Elsewhere {
		t.Fatalf("Check of a moved install = %q", state)
	}
	if _, err := Sync(f.getenv, moved); err != nil {
		t.Fatal(err)
	}
	if state, where := Check(f.getenv, moved); state != OK {
		t.Fatalf("Check after = %q at %s", state, where)
	}
}

func TestCheckSaysMissingWhenTheIconIsGone(t *testing.T) {
	f := newWin(t)
	if _, err := Sync(f.getenv, f.bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(icoPath(f.bin)); err != nil {
		t.Fatal(err)
	}
	if state, where := Check(f.getenv, f.bin); state != Missing || where != icoPath(f.bin) {
		t.Fatalf("Check = %q at %s", state, where)
	}
	if _, err := Sync(f.getenv, f.bin); err != nil {
		t.Fatal(err)
	}
	if state, _ := Check(f.getenv, f.bin); state != OK {
		t.Fatalf("Check after = %q", state)
	}
}

func TestSyncLeavesAUsersOwnShortcutAlone(t *testing.T) {
	f := newWin(t)
	before := f.foreign(t)
	skipped, err := Sync(f.getenv, f.bin)
	if skipped != f.lnk || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if f.writes != 0 {
		t.Fatalf("PowerShell ran %d times over a foreign shortcut", f.writes)
	}
	if after, _ := os.ReadFile(f.lnk); string(after) != string(before) {
		t.Fatal("Sync changed the user's shortcut")
	}
	if _, err := os.Stat(icoPath(f.bin)); err == nil {
		t.Fatal("Sync wrote hand.ico beside a shortcut it does not own")
	}
	if state, _ := Check(f.getenv, f.bin); state != Foreign {
		t.Fatalf("Check = %q", state)
	}
}

func TestRemoveLeavesAUsersOwnShortcutAlone(t *testing.T) {
	f := newWin(t)
	before := f.foreign(t)
	ico := icoPath(f.bin)
	if err := os.MkdirAll(filepath.Dir(ico), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ico, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	skipped, err := Remove(f.getenv)
	if skipped != f.lnk || err != nil {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	if after, _ := os.ReadFile(f.lnk); string(after) != string(before) {
		t.Fatal("Remove deleted or changed the user's shortcut")
	}
	if b, _ := os.ReadFile(ico); string(b) != "mine" {
		t.Fatal("Remove deleted a hand.ico beside a shortcut it does not own")
	}
}

func TestRemoveDeletesHandsShortcutAndIcon(t *testing.T) {
	f := newWin(t)
	if skipped, err := Remove(f.getenv); skipped != "" || err != nil {
		t.Fatalf("Remove with nothing written = %q, %v", skipped, err)
	}
	if _, err := Sync(f.getenv, f.bin); err != nil {
		t.Fatal(err)
	}
	if skipped, err := Remove(f.getenv); skipped != "" || err != nil {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	for _, p := range []string{f.lnk, icoPath(f.bin)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	if state, _ := Check(f.getenv, f.bin); state != Missing {
		t.Fatalf("Check after = %q", state)
	}
}

func TestRemoveOnlyDeletesAnIconNamedHandIco(t *testing.T) {
	f := newWin(t)
	other := filepath.Join(t.TempDir(), "precious.txt")
	if err := os.WriteFile(other, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.lnk), 0o755); err != nil {
		t.Fatal(err)
	}
	err := writeLnk([]string{"HAND_LNK=" + f.lnk, "HAND_TARGET=" + f.bin, "HAND_DIR=" + filepath.Dir(f.bin), "HAND_ICON=" + other, "HAND_DESC=" + description})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Remove(f.getenv); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.lnk); err == nil {
		t.Fatal("Hand's shortcut is still there")
	}
	if b, _ := os.ReadFile(other); string(b) != "keep" {
		t.Fatal("Remove deleted a file that is not hand.ico")
	}
}

func TestSyncReportsAFailedPowerShell(t *testing.T) {
	f := newWin(t)
	writeLnk = func([]string) error { return errors.New("boom") }
	if _, err := Sync(f.getenv, f.bin); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Sync = %v", err)
	}
}

func TestSyncRefusesUnusablePaths(t *testing.T) {
	f := newWin(t)
	if _, err := Sync(f.getenv, `hand-desktop.exe`); err == nil {
		t.Fatal("a relative bin")
	}
	if _, err := Sync(func(string) string { return "" }, f.bin); err == nil {
		t.Fatal("no APPDATA")
	}
}
