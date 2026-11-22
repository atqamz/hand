package uninstall

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktop"
	"github.com/atqamz/hand/internal/desktop/remote"
	"github.com/atqamz/hand/internal/luvus"
	"github.com/atqamz/hand/internal/state"
)

const oldTag = "-ABCDEFGHIJKLMNOPQRSTUVWXYZ"

type install struct {
	t      *testing.T
	goos   string
	home   string
	bin    string
	target string
	root   string
	fleet  string
	state  string
	opts   Options
	entry  []string
	other  string
	calls  struct{ launcher, pathApply int }
	onPath bool

	elsewhere bool
}

func put(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func newInstall(t *testing.T, goos string) *install {
	t.Helper()
	x := &install{t: t, goos: goos, home: t.TempDir()}
	x.bin = filepath.Join(x.home, ".local", "bin")
	if goos == "windows" {
		x.bin = filepath.Join(x.home, "AppData", "Local", "hand", "bin")
	}
	x.target = put(t, filepath.Join(x.bin, withExt("hand", goos)), "hand")
	put(t, filepath.Join(x.bin, withExt("hand-iroh", goos)), "iroh")
	if goos != "darwin" {
		put(t, filepath.Join(x.bin, withExt("hand-desktop", goos)), "window")
	}
	x.root = filepath.Join(x.home, ".secondhand")
	x.fleet = filepath.Join(x.home, "work", "fleet")
	put(t, filepath.Join(x.fleet, "hand.db"), "db")
	put(t, filepath.Join(x.root, "index", "f0123456789ab"), x.fleet+"\n")
	x.state = filepath.Join(x.home, "state")
	put(t, filepath.Join(x.state, "hand-desktop", "drafts", "f0123456789ab.txt"), "draft")
	put(t, filepath.Join(x.state, "hand-desktop", "prefs.json"), "{}")
	put(t, filepath.Join(x.state, "hand-desktop", "remote", "hosts.json"), "{}")
	put(t, filepath.Join(x.state, "hand-desktop", "remote", "cookies.json"), "{}")
	x.other = put(t, filepath.Join(x.bin, "git"), "not Hand's")
	switch goos {
	case "windows":
		put(t, filepath.Join(x.bin, "hand.ico"), "ico")
		put(t, filepath.Join(x.bin, "hand.exe.old"), "old")
		put(t, filepath.Join(x.bin, "hand-desktop.exe.old"+oldTag), "old")
		put(t, filepath.Join(x.bin, "git.exe.old"), "not Hand's")
		x.entry = []string{put(t, filepath.Join(x.home, "AppData", "Roaming", "Hand.lnk"), "lnk")}
		x.onPath = true
	case "darwin":
		put(t, filepath.Join(x.home, "Applications", "Hand.app", "Contents", "Info.plist"), "plist")
		x.entry = []string{filepath.Join(x.home, "Applications", "Hand.app")}
	default:
		x.entry = []string{put(t, filepath.Join(x.home, "share", "hand-desktop.desktop"), "entry")}
	}
	x.opts = Options{
		Target: x.target, Root: x.root, OS: goos, Getenv: func(k string) string {
			if k == "XDG_STATE_HOME" {
				return x.state
			}
			return ""
		},
		Owned: func(func(string) string) ([]string, string, error) { return x.entry, "", nil },
		Mine:  func(func(string) string, string) bool { return !x.elsewhere },
		RemoveLauncher: func(func(string) string) (string, error) {
			x.calls.launcher++
			for _, p := range x.entry {
				if err := os.RemoveAll(p); err != nil {
					return "", err
				}
			}
			return "", nil
		},
		UserPath: func(dir string, apply bool) (bool, error) {
			if apply {
				x.calls.pathApply++
				x.onPath = false
			}
			return x.onPath, nil
		},
	}
	return x
}

func (x *install) run(dry bool) (Report, error) {
	x.opts.DryRun = dry
	return Run(x.opts)
}

func (x *install) snapshot() string {
	var lines []string
	_ = filepath.WalkDir(x.home, func(p string, d fs.DirEntry, err error) error {
		info, _ := d.Info()
		lines = append(lines, fmt.Sprintf("%s %v %d", p, info.Mode(), info.Size()))
		return nil
	})
	return strings.Join(lines, "\n")
}

func gone(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is still there (%v)", p, err)
		}
	}
}

func there(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s is gone (%v)", p, err)
		}
	}
}

func paths(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Path)
	}
	return out
}

func pidFile(t *testing.T, path, exe string) {
	t.Helper()
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Skipf("no process start marker: %v", err)
	}
	put(t, path, fmt.Sprintf("%d %s %s\n", os.Getpid(), marker, exe))
}

var systems = []string{"linux", "darwin", "windows"}

func TestRemovesWhatHandPlacedAndKeepsTheRest(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			rep, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{filepath.Join(x.bin, withExt("hand-iroh", goos))}
			if goos != "darwin" {
				want = append([]string{filepath.Join(x.bin, withExt("hand-desktop", goos))}, want...)
			}
			if goos == "windows" {
				want = append(want, filepath.Join(x.bin, "hand.ico"), filepath.Join(x.bin, "hand.exe.old"), filepath.Join(x.bin, "hand-desktop.exe.old"+oldTag), x.bin)
			}
			want = append(want[:len(want):len(want)], x.entry...)
			got := paths(rep.Removed)
			if last := got[len(got)-1]; last != x.target {
				t.Fatalf("the last path removed is %s, want hand itself (%s)", last, x.target)
			}
			slices.Sort(got)
			want = append(want, x.target)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("removed\n%v\nwant\n%v", got, want)
			}
			kept := paths(rep.Kept)
			for _, p := range []string{x.fleet, x.root, filepath.Join(x.state, "hand-desktop")} {
				if !slices.Contains(kept, p) {
					t.Errorf("kept %v lacks %s", kept, p)
				}
			}
			there(t, x.other, x.fleet, filepath.Join(x.root, "index", "f0123456789ab"), filepath.Join(x.state, "hand-desktop", "drafts", "f0123456789ab.txt"), filepath.Join(x.state, "hand-desktop", "prefs.json"), filepath.Join(x.state, "hand-desktop", "remote", "hosts.json"), filepath.Join(x.state, "hand-desktop", "remote", "cookies.json"))
			for _, p := range paths(rep.Removed) {
				if p != x.bin && p != x.target+".old" {
					gone(t, p)
				}
			}
			if goos == "windows" {
				gone(t, x.target)
				there(t, x.target+".old", filepath.Join(x.bin, "git.exe.old"))
				if x.calls.pathApply != 1 {
					t.Errorf("the user PATH was edited %d times", x.calls.pathApply)
				}
				if got := strings.Join(rep.Help, "\n"); !strings.Contains(got, "Remove-Item -LiteralPath '"+x.target+".old'") {
					t.Errorf("help does not give the line that finishes the job:\n%s", got)
				}
			}
		})
	}
}

func TestHandGoesLastAndStaysWhenAStepFails(t *testing.T) {
	x := newInstall(t, "linux")
	x.opts.RemoveLauncher = func(func(string) string) (string, error) { return "", errors.New("disk on fire") }
	rep, err := x.run(false)
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
	there(t, x.target)
	if slices.Contains(paths(rep.Removed), x.target) {
		t.Fatal("hand is listed as removed")
	}
	gone(t, filepath.Join(x.bin, "hand-iroh"), filepath.Join(x.bin, "hand-desktop"))
	if _, err := x.run(false); err == nil {
		t.Fatal("the second run hid the failure")
	}
	x.opts.RemoveLauncher = func(func(string) string) (string, error) { return "", os.RemoveAll(x.entry[0]) }
	if _, err := x.run(false); err != nil {
		t.Fatal(err)
	}
	gone(t, x.target)
}

func TestAFailedStepDoesNotHeadlineRemoved(t *testing.T) {
	x := newInstall(t, "linux")
	x.opts.RemoveLauncher = func(func(string) string) (string, error) { return "", errors.New("disk on fire") }
	rep, err := x.run(false)
	if err == nil {
		t.Fatal("no error")
	}
	if out := Render(rep).String(); !strings.Contains(out, "status: failed") || strings.Contains(out, "status: removed") {
		t.Fatalf("headline of a failed run:\n%s", out)
	}
}

func TestADryRunWithABlockerListsNothingAsRemoved(t *testing.T) {
	x := newInstall(t, "linux")
	pidFile(t, filepath.Join(x.root, "board.pid"), x.target)
	rep, err := x.run(true)
	if err != nil || len(rep.Removed) != 0 {
		t.Fatalf("removed %v, %v", paths(rep.Removed), err)
	}
}

func TestALauncherEntryOfAnotherInstallIsKept(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			x.elsewhere = true
			rep, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			there(t, x.entry...)
			if x.calls.launcher != 0 {
				t.Fatal("the launcher was asked to remove an entry of another install")
			}
			kept := map[string]string{}
			for _, it := range rep.Kept {
				kept[it.Path] = it.Note
			}
			for _, p := range x.entry {
				if !strings.Contains(kept[p], "does not point at the window beside") {
					t.Errorf("%s kept with %q", p, kept[p])
				}
				if slices.Contains(paths(rep.Removed), p) {
					t.Errorf("%s is listed as removed", p)
				}
			}
			gone(t, x.target)
		})
	}
}

func TestACopyNamedDifferentlyBesideHandLeavesTheCompanionsAndTheEntry(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			next := put(t, filepath.Join(x.bin, withExt("hand-next", goos)), "next")
			x.opts.Target = next
			rep, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			beside := []string{x.target, filepath.Join(x.bin, withExt("hand-iroh", goos))}
			if goos != "darwin" {
				beside = append(beside, filepath.Join(x.bin, withExt("hand-desktop", goos)))
			}
			if goos == "windows" {
				beside = append(beside, filepath.Join(x.bin, "hand.ico"))
			}
			there(t, beside...)
			there(t, x.entry...)
			if x.calls.launcher != 0 || x.calls.pathApply != 0 {
				t.Fatalf("launcher=%d path=%d: the copy touched the install it sits beside", x.calls.launcher, x.calls.pathApply)
			}
			removed := paths(rep.Removed)
			for _, p := range append(beside[:len(beside):len(beside)], x.entry...) {
				if slices.Contains(removed, p) {
					t.Errorf("%s is listed as removed", p)
				}
			}
			kept := paths(rep.Kept)
			for _, p := range beside[1:] {
				if !slices.Contains(kept, p) {
					t.Errorf("kept %v lacks %s", kept, p)
				}
			}
			if goos == "windows" {
				gone(t, next)
				there(t, filepath.Join(x.bin, "hand-desktop.exe.old"+oldTag))
			} else {
				gone(t, next)
			}
		})
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			before := x.snapshot()
			rep, err := x.run(true)
			if err != nil {
				t.Fatal(err)
			}
			if after := x.snapshot(); after != before {
				t.Fatalf("the dry run changed the tree:\n%s\n--\n%s", before, after)
			}
			if x.calls.launcher+x.calls.pathApply != 0 {
				t.Fatalf("the dry run called the launcher %d and the PATH %d times", x.calls.launcher, x.calls.pathApply)
			}
			real := newInstall(t, goos)
			want, err := real.run(false)
			if err != nil {
				t.Fatal(err)
			}
			if len(rep.Removed) != len(want.Removed) || len(rep.Kept) != len(want.Kept) {
				t.Fatalf("the dry run lists %d removed and %d kept, the real run %d and %d", len(rep.Removed), len(rep.Kept), len(want.Removed), len(want.Kept))
			}
			if got := Render(rep).String(); !strings.Contains(got, "status: would remove") {
				t.Fatalf("status:\n%s", got)
			}
		})
	}
}

func TestAForeignLauncherEntryIsKeptAndListed(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			mine := x.entry[0]
			x.opts.Owned = func(func(string) string) ([]string, string, error) { return nil, mine, nil }
			rep, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			if x.calls.launcher != 0 {
				t.Fatal("the launcher was asked to remove an entry that is not Hand's")
			}
			there(t, mine)
			if !slices.Contains(paths(rep.Kept), mine) || slices.Contains(paths(rep.Removed), mine) {
				t.Fatalf("kept %v removed %v", paths(rep.Kept), paths(rep.Removed))
			}
			gone(t, x.target)
		})
	}
}

func TestRefusesWhileABoardOrAWatcherOfThisInstallRuns(t *testing.T) {
	cases := map[string]string{"board": "hand board stop", "watcher": "hand watch stop"}
	for name, stop := range cases {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s dry=%v", name, dry), func(t *testing.T) {
				x := newInstall(t, "linux")
				at := filepath.Join(x.root, "board.pid")
				if name == "watcher" {
					at = filepath.Join(x.fleet, "watch.pid")
				}
				pidFile(t, at, x.target)
				before := x.snapshot()
				rep, err := x.run(dry)
				if len(rep.Blocked) != 1 || !strings.Contains(rep.Blocked[0], stop) {
					t.Fatalf("blocked = %v", rep.Blocked)
				}
				if dry {
					if err != nil || !strings.Contains(Render(rep).String(), "status: would refuse") {
						t.Fatalf("err = %v\n%s", err, Render(rep))
					}
				} else if !errors.Is(err, state.ErrConflict) || !strings.Contains(err.Error(), stop) {
					t.Fatalf("err = %v", err)
				}
				if x.snapshot() != before || x.calls.launcher != 0 {
					t.Fatal("a refused uninstall changed something")
				}
			})
		}
	}
}

func TestABoardOfAnotherInstallDoesNotBlock(t *testing.T) {
	x := newInstall(t, "linux")
	pidFile(t, filepath.Join(x.root, "board.pid"), filepath.Join(t.TempDir(), "hand"))
	if rep, err := x.run(false); err != nil || len(rep.Blocked) != 0 {
		t.Fatalf("err = %v blocked = %v", err, rep.Blocked)
	}
	gone(t, x.target)
}

func TestAStalePidFileDoesNotBlock(t *testing.T) {
	x := newInstall(t, "linux")
	put(t, filepath.Join(x.root, "board.pid"), "999999 not-a-marker "+x.target+"\n")
	if _, err := x.run(false); err != nil {
		t.Fatal(err)
	}
	gone(t, x.target)
}

func TestAWizardInstallIsLeftToWindowsSettings(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint("dry=", dry), func(t *testing.T) {
			x := newInstall(t, "windows")
			un := put(t, filepath.Join(filepath.Dir(x.bin), "unins000.exe"), "wizard")
			before := x.snapshot()
			rep, err := x.run(dry)
			if dry != (err == nil) || (!dry && !errors.Is(err, state.ErrConflict)) {
				t.Fatalf("dry=%v err=%v", dry, err)
			}
			if len(rep.Blocked) != 1 || !strings.Contains(rep.Blocked[0], "Settings") || !strings.Contains(rep.Blocked[0], un) {
				t.Fatalf("blocked = %v", rep.Blocked)
			}
			if x.snapshot() != before || x.calls.launcher+x.calls.pathApply != 0 {
				t.Fatal("a wizard install was changed")
			}
		})
	}
}

func TestOnlyWindowsLooksForAWizard(t *testing.T) {
	x := newInstall(t, "linux")
	put(t, filepath.Join(filepath.Dir(x.bin), "unins000.exe"), "x")
	if _, err := x.run(false); err != nil {
		t.Fatal(err)
	}
}

func TestASecondRunHasNothingLeftToDo(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			if _, err := x.run(false); err != nil {
				t.Fatal(err)
			}
			x.entry = nil
			if goos == "windows" {
				gone(t, x.target)
				if err := os.Remove(x.target + ".old"); err != nil {
					t.Fatal(err)
				}
			}
			again, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			if len(again.Removed) != 0 {
				t.Fatalf("the second run removed %v", paths(again.Removed))
			}
			if got := Render(again).String(); !strings.Contains(got, "status: nothing to remove") {
				t.Fatalf("status:\n%s", got)
			}
		})
	}
}

func TestWindowsRenamesTheRunningHandAndOverwritesAnEarlierOldFile(t *testing.T) {
	x := newInstall(t, "windows")
	if _, err := x.run(false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(x.target + ".old"); string(b) != "hand" {
		t.Fatalf("hand.exe.old holds %q", b)
	}
}

func TestWindowsQuotesAnApostropheInTheDeleteLine(t *testing.T) {
	x := newInstall(t, "windows")
	dir := filepath.Join(x.home, "O'Brien", "hand", "bin")
	x.target = put(t, filepath.Join(dir, "hand.exe"), "hand")
	x.opts.Target = x.target
	rep, err := x.run(false)
	if err != nil {
		t.Fatal(err)
	}
	want := "Remove-Item -LiteralPath '" + strings.ReplaceAll(x.target+".old", "'", "''") + "'"
	if !strings.Contains(want, "O''Brien") || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, want) }) {
		t.Fatalf("help = %q, want a line with %s", rep.Help, want)
	}
}

func TestDraftFolderIsTheWindows(t *testing.T) {
	cases := []map[string]string{
		{"XDG_STATE_HOME": "/s"},
		{"LOCALAPPDATA": `C:\L`, "HOME": "/h"},
		{"HOME": "/h"},
		{"USERPROFILE": `C:\U`},
		{},
	}
	for _, env := range cases {
		getenv := func(k string) string { return env[k] }
		if got, want := draftDir(getenv, runtimeOS()), desktop.DraftDir(getenv); got != want {
			t.Errorf("draftDir(%v) = %q, the window uses %q", env, got, want)
		}
	}
}

func TestRemoteHostsFolderIsKeptAndNamedWithItsWarning(t *testing.T) {
	for _, goos := range systems {
		t.Run(goos, func(t *testing.T) {
			x := newInstall(t, goos)
			rep, err := x.run(false)
			if err != nil {
				t.Fatal(err)
			}
			dir := remote.Dir(desktop.DraftDir(func(k string) string {
				if k == "XDG_STATE_HOME" {
					return x.state
				}
				return ""
			}))
			if dir != filepath.Join(x.state, "hand-desktop", "remote") {
				t.Fatalf("remote.Dir = %s", dir)
			}
			var why string
			for _, it := range rep.Kept {
				if it.Path == dir {
					why = it.Note
				}
			}
			if !strings.Contains(why, "copy is a copy of this device") || !strings.Contains(why, "revoke") {
				t.Fatalf("kept %v: no remote folder with its warning (%q)", paths(rep.Kept), why)
			}
			if out := Render(rep).String(); !strings.Contains(out, strings.ReplaceAll(dir, `\`, `\\`)) {
				t.Fatalf("the report does not print %s:\n%s", dir, out)
			}
			gone := paths(rep.Removed)
			if slices.Contains(gone, dir) || slices.Contains(gone, filepath.Join(dir, "cookies.json")) {
				t.Fatalf("removed %v", gone)
			}
		})
	}
}
