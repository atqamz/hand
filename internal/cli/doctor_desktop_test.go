package cli_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/cli"
	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/launcher"
)

func buildChannel(t *testing.T, channel string) {
	t.Helper()
	saved := cli.Channel
	t.Cleanup(func() { cli.Channel = saved })
	cli.Channel = channel
}

func installDesktop(t *testing.T, fx *doctorFixture, params map[string]string) {
	t.Helper()
	fakebin.Install(t, fx.tools, desktopasset.Name, "desktop", params)
}

func TestDoctorDesktopRowFollowsWhatIsInstalled(t *testing.T) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH, "edge") {
		t.Skip("hand-desktop assets are for Linux and Windows only")
	}
	commit, version := cli.Commit, cli.Version
	t.Cleanup(func() { cli.Commit, cli.Version = commit, version })
	cli.Commit = "0123456789abcdef0123"
	for _, c := range []struct {
		name, level, fix string
		commit           string
		set              func(t *testing.T, fx *doctorFixture)
	}{
		{"not installed", "info", "not installed; run `hand update`", "", func(*testing.T, *doctorFixture) {}},
		{"another program under the name", "warn", "not a hand-desktop binary", "", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": "x", "commit": "y"})
		}},
		{"another version", "warn", "hand-desktop 9.9.9 0123456789ab does not match this hand's " + cli.Version + " 0123456789ab", "", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": "9.9.9", "commit": "0123456789ab"})
		}},
		{"another commit", "warn", "hand-desktop " + cli.Version + " abcdef012345 does not match this hand's " + cli.Version + " 0123456789ab", "", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "abcdef012345"})
		}},
		{"unknown on both sides", "info", "identity unverifiable", "unknown", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "unknown"})
		}},
		{"this hand has no stamp", "info", "identity unverifiable", "unknown", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
		}},
		{"the desktop has no stamp", "info", "identity unverifiable", "", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "unknown"})
		}},
		{"installed and matching", "ok", `""`, "", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			saved := cli.Commit
			t.Cleanup(func() { cli.Commit = saved })
			if c.commit != "" {
				cli.Commit = c.commit
			}
			fx := newDoctorFixture(t)
			buildChannel(t, "edge")
			holdWatchLock(t, fx.h.home)
			c.set(t, fx)
			out := fx.h.ok("doctor")
			row := checkRows(t, out)["desktop"]
			if row[0] != c.level || !strings.Contains(row[1], c.fix) {
				t.Fatalf("desktop = %q, want %s with %q in:\n%s", row, c.level, c.fix, out)
			}
			got := field(out, "desktop")
			if c.level == "ok" && !strings.HasPrefix(got, cli.Version+" 0123456789ab ") {
				t.Fatalf("desktop field = %q", got)
			}
		})
	}
}

func TestDoctorSaysTheDesktopIsUnsupportedHere(t *testing.T) {
	if desktopasset.Supported(runtime.GOOS, runtime.GOARCH, "stable") {
		t.Skip("this platform supports hand-desktop on a stable build")
	}
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	out := fx.h.ok("doctor")
	row := checkRows(t, out)["desktop"]
	if row[0] != "info" || !strings.Contains(row[1], "unsupported on "+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("desktop = %q in:\n%s", row, out)
	}
}

func TestDoctorDesktopReadsTheBinaryNextToHand(t *testing.T) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH, "edge") {
		t.Skip("hand-desktop assets are for Linux and Windows only")
	}
	fx := newDoctorFixture(t)
	buildChannel(t, "edge")
	holdWatchLock(t, fx.h.home)
	next := filepath.Join(fx.self, desktopasset.Name)
	if runtime.GOOS == "windows" {
		next += ".exe"
	}
	b := fakebin.Embed(t, "desktop", map[string]string{"version": cli.Version, "commit": "0123456789ab"})
	if err := os.WriteFile(next, b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(next) })
	out := fx.h.ok("doctor")
	if !strings.Contains(field(out, "desktop"), next) {
		t.Fatalf("desktop field = %q, want %s", field(out, "desktop"), next)
	}
}

func TestDoctorDesktopRowNeedsNoSecondhandFolder(t *testing.T) {
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	delete(fx.h.vars, "HOME")
	delete(fx.h.vars, "SECONDHAND_HOME")
	out, _, code := fx.h.run("doctor")
	if code != 1 || !strings.Contains(out, "the fleet checks were skipped") {
		t.Fatalf("exit %d, out:\n%s", code, out)
	}
	row := checkRows(t, out)["desktop"]
	if row[0] == "" && row[1] == "" {
		t.Fatalf("doctor skipped the desktop row without a secondhand folder:\n%s", out)
	}
	if row[0] != "info" && row[0] != "warn" {
		t.Fatalf("desktop = %q in:\n%s", row, out)
	}
}

func TestDoctorDesktopEntryRow(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the launcher entry is written on Linux only")
	}
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	data := t.TempDir()
	fx.h.vars["XDG_DATA_HOME"] = data
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "" {
		t.Fatalf("a row without hand-desktop: %q", row)
	}
	stale := filepath.Join(data, "applications", "hand-desktop.desktop")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("[Desktop Entry]\nX-Hand-Managed=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "warn" || !strings.Contains(row[1], "left over") {
		t.Fatalf("stale = %q", row)
	}
	if err := os.Remove(stale); err != nil {
		t.Fatal(err)
	}
	installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "warn" || !strings.Contains(row[1], "is missing") {
		t.Fatalf("missing = %q", row)
	}
	entry := filepath.Join(data, "applications", "hand-desktop.desktop")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("[Desktop Entry]\nExec=/elsewhere\nX-Hand-Managed=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "warn" || !strings.Contains(row[1], "does not point at") {
		t.Fatalf("elsewhere = %q", row)
	}
	if err := os.WriteFile(entry, []byte("[Desktop Entry]\nExec=/mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "warn" || !strings.Contains(row[1], "not Hand's launcher entry") {
		t.Fatalf("foreign = %q", row)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	bin, err := desktopasset.Locate(filepath.Join(fx.self, "hand"), fx.h.vars["PATH"], runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.Sync(func(k string) string { return fx.h.vars[k] }, bin); err != nil {
		t.Fatal(err)
	}
	if row := checkRows(t, fx.h.ok("doctor"))["desktop entry"]; row[0] != "ok" {
		t.Fatalf("synced = %q", row)
	}
}

func TestDoctorSaysHowFarTheWindowWasTriedOnWindows(t *testing.T) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH, "edge") {
		t.Skip("hand-desktop assets are for Linux and Windows only")
	}
	fx := newDoctorFixture(t)
	buildChannel(t, "edge")
	holdWatchLock(t, fx.h.home)
	installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
	rows := checkRows(t, fx.h.ok("doctor"))
	window, entry, shortcut := rows["desktop window"], rows["desktop entry"], rows["start menu shortcut"]
	if runtime.GOOS != "windows" {
		if window[0] != "" {
			t.Fatalf("a desktop window row off Windows: %q", window)
		}
		return
	}
	if window[0] != "info" || !strings.Contains(window[1], "edge builds only") || !strings.Contains(window[1], "CI runner, not yet on a real Windows PC") {
		t.Fatalf("desktop window = %q", window)
	}
	if entry[0] != "" {
		t.Fatalf("a launcher entry row on Windows: %q", entry)
	}
	if shortcut[0] != "warn" || !strings.Contains(shortcut[1], "is missing") {
		t.Fatalf("start menu shortcut = %q", shortcut)
	}
}

func TestDoctorStartMenuShortcutRow(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Start Menu shortcut is written on Windows only")
	}
	fx := newDoctorFixture(t)
	buildChannel(t, "edge")
	holdWatchLock(t, fx.h.home)
	roaming := t.TempDir()
	fx.h.vars["APPDATA"] = roaming
	getenv := func(k string) string { return fx.h.vars[k] }
	lnk := filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")
	row := func() [2]string { return checkRows(t, fx.h.ok("doctor"))["start menu shortcut"] }
	if r := row(); r[0] != "" {
		t.Fatalf("a row without hand-desktop: %q", r)
	}
	installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
	if r := row(); r[0] != "warn" || !strings.Contains(r[1], "is missing") {
		t.Fatalf("missing = %q", r)
	}
	bin, err := desktopasset.Locate(filepath.Join(fx.self, "hand"), fx.h.vars["PATH"], runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := launcher.Sync(getenv, bin); err != nil {
		t.Fatal(err)
	}
	if r := row(); r[0] != "ok" {
		t.Fatalf("synced = %q", r)
	}
	if err := os.WriteFile(lnk, []byte("not a shortcut"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := row(); r[0] != "warn" || !strings.Contains(r[1], "not Hand's launcher entry") {
		t.Fatalf("foreign = %q", r)
	}
}

func TestDoctorStartMenuShortcutLeftOverRow(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Start Menu shortcut is written on Windows only")
	}
	fx := newDoctorFixture(t)
	buildChannel(t, "edge")
	holdWatchLock(t, fx.h.home)
	fx.h.vars["APPDATA"] = t.TempDir()
	getenv := func(k string) string { return fx.h.vars[k] }
	if _, err := launcher.Sync(getenv, filepath.Join(t.TempDir(), "hand-desktop.exe")); err != nil {
		t.Fatal(err)
	}
	row := checkRows(t, fx.h.ok("doctor"))["start menu shortcut"]
	if row[0] != "warn" || !strings.Contains(row[1], "left over") || !strings.Contains(row[1], "hand update") {
		t.Fatalf("left over = %q", row)
	}
}

func TestDoctorHasNoWindowRowBeforeTheWindowIsInstalled(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the desktop window row exists only on Windows")
	}
	fx := newDoctorFixture(t)
	buildChannel(t, "edge")
	holdWatchLock(t, fx.h.home)
	if row := checkRows(t, fx.h.ok("doctor"))["desktop window"]; row[0] != "" {
		t.Fatalf("a desktop window row without an installed window: %q", row)
	}
}
