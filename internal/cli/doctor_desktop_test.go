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
)

func installDesktop(t *testing.T, fx *doctorFixture, params map[string]string) {
	t.Helper()
	fakebin.Install(t, fx.tools, desktopasset.Name, "desktop", params)
}

func TestDoctorDesktopRowFollowsWhatIsInstalled(t *testing.T) {
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		t.Skip("hand-desktop assets are Linux only")
	}
	commit, version := cli.Commit, cli.Version
	t.Cleanup(func() { cli.Commit, cli.Version = commit, version })
	cli.Commit = "0123456789abcdef0123"
	for _, c := range []struct {
		name, level, fix string
		set              func(t *testing.T, fx *doctorFixture)
	}{
		{"not installed", "info", "not installed; run `hand update`", func(*testing.T, *doctorFixture) {}},
		{"another program under the name", "warn", "not a hand-desktop binary", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": "x", "commit": "y"})
		}},
		{"another version", "warn", "hand-desktop 9.9.9 0123456789ab does not match this hand's " + cli.Version + " 0123456789ab", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": "9.9.9", "commit": "0123456789ab"})
		}},
		{"another commit", "warn", "hand-desktop " + cli.Version + " abcdef012345 does not match this hand's " + cli.Version + " 0123456789ab", func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "abcdef012345"})
		}},
		{"installed and matching", "ok", `""`, func(t *testing.T, fx *doctorFixture) {
			installDesktop(t, fx, map[string]string{"version": cli.Version, "commit": "0123456789ab"})
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fx := newDoctorFixture(t)
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
	if desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		t.Skip("this platform supports hand-desktop")
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
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		t.Skip("hand-desktop assets are Linux only")
	}
	fx := newDoctorFixture(t)
	holdWatchLock(t, fx.h.home)
	next := filepath.Join(fx.self, desktopasset.Name)
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
