//go:build darwin

package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/launcher"
)

func macFixture(t *testing.T) *fixture {
	t.Helper()
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	f.o.OS, f.o.Arch = "darwin", "arm64"
	return f
}

func (f *fixture) bundle() (app, window string) {
	window = desktopasset.Path(f.target, "darwin", f.o.Getenv("HOME"))
	return filepath.Dir(filepath.Dir(filepath.Dir(window))), window
}

func TestRunBuildsTheBundleOnMacOS(t *testing.T) {
	for _, current := range []bool{false, true} {
		f := macFixture(t)
		if current {
			f.o.From = currentBuild()
		}
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Failed || rep.Desktop != "0.9.0 0123456789ab installed" || len(rep.Help) != 0 {
			t.Fatalf("current %v: report = %+v, %v", current, rep, err)
		}
		app, window := f.bundle()
		if fi, err := os.Stat(window); err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("current %v: the window in the bundle: %v, %v", current, fi, err)
		}
		for _, p := range []string{"Info.plist", "PkgInfo", "Resources/Hand.icns"} {
			if b, err := os.ReadFile(filepath.Join(app, "Contents", p)); err != nil || len(b) == 0 {
				t.Fatalf("current %v: %s: %v", current, p, err)
			}
		}
		if b, _ := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")); !strings.Contains(string(b), "<key>HandManaged</key>") {
			t.Fatalf("current %v: Info.plist has no HandManaged key", current)
		}
		if _, err := os.Readlink(filepath.Join(app, "Contents", "MacOS", "hand")); err != nil {
			t.Fatalf("current %v: MacOS/hand: %v", current, err)
		}
		if state, where := launcher.Check(f.o.Getenv, window); state != launcher.OK {
			t.Fatalf("current %v: Check = %q, %q", current, state, where)
		}
		absent(t, filepath.Join(filepath.Dir(f.target), desktopasset.Name))
		if out := Render(rep).String(); !strings.Contains(out, "desktop: 0.9.0 0123456789ab installed\n") {
			t.Fatalf("current %v: render:\n%s", current, out)
		}
	}
}

func TestRunKeepsTheBundleItBuilt(t *testing.T) {
	f := macFixture(t)
	f.o.From = currentBuild()
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	app, window := f.bundle()
	before, err := os.Stat(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	asked := len(f.srv.asked())
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "" || len(rep.Help) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktopSince(asked) {
		t.Fatalf("a matching window was fetched again: %q", f.srv.asked()[asked:])
	}
	after, err := os.Stat(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("Info.plist was rewritten: %v", err)
	}
	if state, _ := launcher.Check(f.o.Getenv, window); state != launcher.OK {
		t.Fatalf("Check = %q", state)
	}
}

func (f *fixture) asksForDesktopSince(n int) bool {
	for _, p := range f.srv.asked()[n:] {
		if strings.Contains(p, desktopasset.Name+"-") {
			return true
		}
	}
	return false
}

func TestRunLeavesAForeignHandAppAlone(t *testing.T) {
	for _, current := range []bool{false, true} {
		f := macFixture(t)
		if current {
			f.o.From = currentBuild()
		}
		app, window := f.bundle()
		plist := filepath.Join(app, "Contents", "Info.plist")
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(plist, []byte("<plist>mine</plist>"), 0o644); err != nil {
			t.Fatal(err)
		}
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Failed || rep.Desktop != "skipped: "+app+" is not Hand's" || len(rep.Help) != 1 || !strings.Contains(rep.Help[0], app+" is not Hand's launcher entry") {
			t.Fatalf("current %v: report = %+v, %v", current, rep, err)
		}
		if f.asksForDesktop() {
			t.Fatalf("current %v: a desktop was fetched for a bundle that is not Hand's: %q", current, f.srv.asked())
		}
		var names []string
		_ = filepath.WalkDir(app, func(p string, _ os.DirEntry, _ error) error { names = append(names, filepath.Base(p)); return nil })
		if got := strings.Join(names, " "); got != "Hand.app Contents Info.plist" {
			t.Fatalf("current %v: the foreign bundle now holds %q", current, got)
		}
		if b, _ := os.ReadFile(plist); string(b) != "<plist>mine</plist>" {
			t.Fatalf("current %v: the foreign Info.plist changed: %q", current, b)
		}
		absent(t, window)
	}
}

func TestRunCheckBuildsNoBundle(t *testing.T) {
	f := macFixture(t)
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "would install" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	app, _ := f.bundle()
	absent(t, app)
}

func TestRunRemovesTheBundleOfAWindowThatIsGone(t *testing.T) {
	f := macFixture(t)
	f.o.From = currentBuild()
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	app, window := f.bundle()
	if err := os.Remove(window); err != nil {
		t.Fatal(err)
	}
	f.srv.mu.Lock()
	delete(f.srv.files, desktopName("darwin", "arm64")+".tar.gz")
	f.srv.mu.Unlock()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "none in this release" || len(rep.Help) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, app)
}

func TestRunSkipsABundleThatTurnsForeignDuringTheUpdate(t *testing.T) {
	for _, current := range []bool{false, true} {
		f := macFixture(t)
		if current {
			f.o.From = currentBuild()
		}
		app, window := f.bundle()
		plist := filepath.Join(app, "Contents", "Info.plist")
		getenv, once := f.o.Getenv, false
		f.o.Getenv = func(k string) string {
			if k == "HOME" && !once && f.asksForDesktop() {
				once = true
				if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
					t.Error(err)
				}
				if err := os.WriteFile(plist, []byte("<plist>mine</plist>"), 0o644); err != nil {
					t.Error(err)
				}
			}
			return getenv(k)
		}
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Failed || rep.Desktop != "skipped: "+app+" is not Hand's" || len(rep.Help) != 1 || !strings.Contains(rep.Help[0], app+" is not Hand's launcher entry") {
			t.Fatalf("current %v: report = %+v, %v", current, rep, err)
		}
		if !once {
			t.Fatalf("current %v: the bundle never turned foreign", current)
		}
		absent(t, window)
		if b, _ := os.ReadFile(plist); string(b) != "<plist>mine</plist>" {
			t.Fatalf("current %v: the foreign Info.plist changed: %q", current, b)
		}
	}
}
