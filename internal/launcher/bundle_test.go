package launcher

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type tree struct {
	home, hand string
	b          bundle
}

func newTree(t *testing.T) tree {
	t.Helper()
	home := t.TempDir()
	b, err := bundleAt(home)
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Symlink(home, probe); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	return tree{home, filepath.Join(home, ".local", "bin", "hand"), b}
}

func (x tree) sync(t *testing.T) {
	t.Helper()
	if skipped, err := bundleSync(x.home, x.b.window(), x.hand); skipped != "" || err != nil {
		t.Fatalf("bundleSync = %q, %v", skipped, err)
	}
	if err := os.WriteFile(x.b.window(), []byte("window"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestBundleSyncWritesTheTree(t *testing.T) {
	x := newTree(t)
	x.sync(t)
	for path, want := range map[string]string{x.b.info(): "APPL????", x.b.plist(): infoPlist} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", path, got, err)
		}
	}
	if target, err := os.Readlink(x.b.hand()); err != nil || target != x.hand {
		t.Fatalf("MacOS/hand -> %q, %v", target, err)
	}
	if got, err := os.ReadFile(x.b.icon()); err != nil || !bytes.Equal(got, icnsFile()) {
		t.Fatalf("Hand.icns differs: %v", err)
	}
	for _, want := range []string{"<key>CFBundleExecutable</key>\n\t<string>hand-desktop</string>", "<key>CFBundleIconFile</key>\n\t<string>Hand</string>", "<key>HandManaged</key>\n\t<true/>", "<key>CFBundlePackageType</key>\n\t<string>APPL</string>"} {
		if !strings.Contains(infoPlist, want) {
			t.Fatalf("Info.plist lacks %q", want)
		}
	}
	if state, where := bundleCheck(x.home, x.b.window(), x.hand); state != OK || where != x.b.app {
		t.Fatalf("Check = %q, %q", state, where)
	}
	before, _ := os.Stat(x.b.plist())
	x.sync(t)
	after, _ := os.Stat(x.b.plist())
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("a second Sync rewrote an unchanged Info.plist")
	}
}

func TestBundleSyncMovesTheHandLink(t *testing.T) {
	x := newTree(t)
	x.sync(t)
	moved := filepath.Join(x.home, "elsewhere", "hand")
	if state, where := bundleCheck(x.home, x.b.window(), moved); state != Elsewhere || where != x.b.hand() {
		t.Fatalf("Check with another hand = %q, %q", state, where)
	}
	if skipped, err := bundleSync(x.home, x.b.window(), moved); skipped != "" || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if target, _ := os.Readlink(x.b.hand()); target != moved {
		t.Fatalf("MacOS/hand -> %q", target)
	}
	if state, _ := bundleCheck(x.home, x.b.window(), moved); state != OK {
		t.Fatalf("Check = %q", state)
	}
}

func TestBundleSyncRefusesAWindowOutsideTheBundle(t *testing.T) {
	x := newTree(t)
	if _, err := bundleSync(x.home, filepath.Join(x.home, ".local", "bin", "hand-desktop"), x.hand); err == nil || !strings.Contains(err.Error(), "is not "+x.b.window()) {
		t.Fatalf("err = %v", err)
	}
	if _, err := bundleSync("relative", x.b.window(), x.hand); err == nil {
		t.Fatal("a relative HOME was accepted")
	}
	if _, err := bundleSync(x.home, x.b.window(), "hand"); err == nil {
		t.Fatal("a relative hand was accepted")
	}
	if _, err := os.Lstat(x.b.app); !os.IsNotExist(err) {
		t.Fatalf("a refused Sync left %s: %v", x.b.app, err)
	}
}

func TestBundleLeavesAForeignBundleAlone(t *testing.T) {
	for name, plist := range map[string]string{"another app": "<plist><dict><key>CFBundleName</key><string>Hand</string></dict></plist>", "no Info.plist": ""} {
		t.Run(name, func(t *testing.T) {
			x := newTree(t)
			if err := os.MkdirAll(filepath.Join(x.b.app, "Contents"), 0o755); err != nil {
				t.Fatal(err)
			}
			if plist != "" {
				if err := os.WriteFile(x.b.plist(), []byte(plist), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mine := filepath.Join(x.b.app, "Contents", "mine")
			if err := os.WriteFile(mine, []byte("mine"), 0o644); err != nil {
				t.Fatal(err)
			}
			if skipped, err := bundleSync(x.home, x.b.window(), x.hand); skipped != x.b.app || err != nil {
				t.Fatalf("Sync = %q, %v", skipped, err)
			}
			if skipped, err := bundleRemove(x.home); skipped != x.b.app || err != nil {
				t.Fatalf("Remove = %q, %v", skipped, err)
			}
			if state, where := bundleCheck(x.home, x.b.window(), x.hand); state != Foreign || where != x.b.app {
				t.Fatalf("Check = %q, %q", state, where)
			}
			var names []string
			_ = filepath.WalkDir(x.b.app, func(p string, _ os.DirEntry, _ error) error { names = append(names, filepath.Base(p)); return nil })
			want := "Hand.app Contents mine"
			if plist != "" {
				want = "Hand.app Contents Info.plist mine"
			}
			if got := strings.Join(names, " "); got != want {
				t.Fatalf("the foreign bundle now holds %q, want %q", got, want)
			}
			if b, _ := os.ReadFile(mine); string(b) != "mine" {
				t.Fatal("a file of the foreign bundle changed")
			}
		})
	}
}

func TestBundleRemoveTakesOnlyHands(t *testing.T) {
	x := newTree(t)
	neighbour := filepath.Join(x.home, "Applications", "Other.app", "Contents", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(neighbour), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighbour, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if skipped, err := bundleRemove(x.home); skipped != "" || err != nil {
		t.Fatalf("Remove with no bundle = %q, %v", skipped, err)
	}
	x.sync(t)
	if skipped, err := bundleRemove(x.home); skipped != "" || err != nil {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	if _, err := os.Lstat(x.b.app); !os.IsNotExist(err) {
		t.Fatalf("Hand.app is still there: %v", err)
	}
	if _, err := os.Stat(neighbour); err != nil {
		t.Fatalf("a neighbour went too: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(x.b.app)); len(entries) != 1 {
		t.Fatalf("Applications holds %v, want only the neighbour", entries)
	}
	if _, err := os.Stat(x.hand); !os.IsNotExist(err) {
		t.Fatalf("the hand behind the link: %v", err)
	}
	if state, _ := bundleCheck(x.home, x.b.window(), x.hand); state != Missing {
		t.Fatalf("Check after Remove = %q", state)
	}
}

func TestBundleRemoveKeepsABundleThatTurnsForeignBeforeTheMove(t *testing.T) {
	x := newTree(t)
	x.sync(t)
	real := moveAside
	moveAside = func(from, to string) error {
		if err := os.WriteFile(x.b.plist(), []byte("<plist>mine</plist>"), 0o644); err != nil {
			return err
		}
		return real(from, to)
	}
	t.Cleanup(func() { moveAside = real })
	if skipped, err := bundleRemove(x.home); skipped != "" || err == nil || !strings.Contains(err.Error(), "was not deleted") {
		t.Fatalf("Remove = %q, %v", skipped, err)
	}
	moved, _ := filepath.Glob(filepath.Join(filepath.Dir(x.b.app), ".hand-desktop.removed-*"))
	if len(moved) != 1 {
		t.Fatalf("the foreign bundle is at %v", moved)
	}
	if b, _ := os.ReadFile(filepath.Join(moved[0], "Contents", "Info.plist")); string(b) != "<plist>mine</plist>" {
		t.Fatalf("the foreign Info.plist: %q", b)
	}
}

func TestBundleCheckNamesWhatIsWrong(t *testing.T) {
	x := newTree(t)
	x.sync(t)
	if state, where := bundleCheck(x.home, filepath.Join(x.home, "hand-desktop"), x.hand); state != Elsewhere || where != x.b.app {
		t.Fatalf("a window elsewhere: %q, %q", state, where)
	}
	for _, p := range []string{x.b.icon(), x.b.info(), x.b.window()} {
		saved, _ := os.ReadFile(p)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		if state, where := bundleCheck(x.home, x.b.window(), x.hand); state != Missing || where != p {
			t.Fatalf("without %s: %q, %q", p, state, where)
		}
		if err := os.WriteFile(p, saved, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(x.b.plist(), []byte(infoPlist+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if state, _ := bundleCheck(x.home, x.b.window(), x.hand); state != Elsewhere {
		t.Fatalf("an Info.plist that differs: %q", state)
	}
	if skipped, err := bundleSync(x.home, x.b.window(), x.hand); skipped != "" || err != nil {
		t.Fatalf("Sync = %q, %v", skipped, err)
	}
	if state, _ := bundleCheck(x.home, x.b.window(), x.hand); state != OK {
		t.Fatalf("after Sync: %q", state)
	}
}

func TestIcnsHoldsEveryPNGSize(t *testing.T) {
	b := icnsFile()
	if string(b[:4]) != "icns" || int(binary.BigEndian.Uint32(b[4:])) != len(b) {
		t.Fatalf("header %q, length %d of %d", b[:4], binary.BigEndian.Uint32(b[4:]), len(b))
	}
	sizes := map[string]int{"icp4": 16, "icp5": 32, "icp6": 64, "ic07": 128, "ic08": 256, "ic09": 512, "ic11": 32, "ic12": 64, "ic13": 256, "ic14": 512}
	seen := 0
	for p := 8; p < len(b); {
		kind, n := string(b[p:p+4]), int(binary.BigEndian.Uint32(b[p+4:]))
		img, err := png.Decode(bytes.NewReader(b[p+8 : p+n]))
		if err != nil || img.Bounds().Dx() != sizes[kind] || img.Bounds().Dy() != sizes[kind] {
			t.Fatalf("%s: %v, %v", kind, img.Bounds(), err)
		}
		seen++
		p += n
	}
	if seen != len(sizes) {
		t.Fatalf("%d entries, want %d", seen, len(sizes))
	}
}

func TestOnlyATopLevelKeyMarksABundleAsHands(t *testing.T) {
	wrap := func(body string) []byte {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` + body + `</dict></plist>`)
	}
	for name, c := range map[string]struct {
		plist []byte
		want  bool
	}{
		"the plist Hand writes": {[]byte(infoPlist), true},
		"after another key":     {wrap(`<key>CFBundleName</key><string>Hand</string><key>HandManaged</key><true/>`), true},
		"in a comment":          {wrap(`<!-- <key>HandManaged</key> --><key>CFBundleName</key><string>x</string>`), false},
		"in a string":           {wrap(`<key>CFBundleName</key><string>HandManaged</string>`), false},
		"in a nested dict":      {wrap(`<key>Other</key><dict><key>HandManaged</key><true/></dict>`), false},
		"not XML":               {[]byte("bplist00 HandManaged"), false},
		"empty":                 {nil, false},
	} {
		if got := marked(c.plist); got != c.want {
			t.Errorf("%s: marked = %v, want %v", name, got, c.want)
		}
	}
}

func TestBundleOwnedNamesTheBundleOnlyWhenItIsHands(t *testing.T) {
	x := newTree(t)
	if found, foreign, err := bundleOwned(x.home); len(found) != 0 || foreign != "" || err != nil {
		t.Fatalf("bundleOwned with nothing there = %v, %q, %v", found, foreign, err)
	}
	x.sync(t)
	if found, foreign, err := bundleOwned(x.home); len(found) != 1 || found[0] != x.b.app || foreign != "" || err != nil {
		t.Fatalf("bundleOwned = %v, %q, %v", found, foreign, err)
	}
	if err := os.WriteFile(x.b.plist(), []byte("<plist><dict><key>CFBundleName</key><string>Hand</string></dict></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if found, foreign, err := bundleOwned(x.home); len(found) != 0 || foreign != x.b.app || err != nil {
		t.Fatalf("bundleOwned with a foreign bundle = %v, %q, %v", found, foreign, err)
	}
	if _, _, err := bundleOwned("relative"); err == nil {
		t.Fatal("bundleOwned accepted a relative home")
	}
}

func TestBundleMineWhenItsHandSymlinkPointsAtThisHand(t *testing.T) {
	x := newTree(t)
	if bundleMine(x.home, x.hand) {
		t.Fatal("bundleMine with nothing there")
	}
	x.sync(t)
	if !bundleMine(x.home, x.hand) {
		t.Fatal("bundleMine of its own hand = false")
	}
	if bundleMine(x.home, filepath.Join(t.TempDir(), "hand")) {
		t.Fatal("bundleMine of another hand = true")
	}
	if bundleMine("relative", x.hand) {
		t.Fatal("bundleMine accepted a relative home")
	}
	if err := os.WriteFile(x.b.plist(), []byte("<plist><dict><key>CFBundleName</key><string>Hand</string></dict></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if bundleMine(x.home, x.hand) {
		t.Fatal("bundleMine of a foreign bundle = true")
	}
}
