package update

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/launcher"
)

func desktopName(goos, arch string) string { return desktopasset.Name + "-" + goos + "-" + arch }

func fakeDesktop(t *testing.T, version, commit string) map[string][]byte {
	t.Helper()
	script := string(fakebin.Embed(t, "desktop", map[string]string{"version": version, "commit": commit}))
	archive, archiveZip := tarball(t, desktopasset.Name, script), zipball(t, desktopasset.Name+".exe", script)
	files := map[string][]byte{}
	for _, name := range []string{desktopName("linux", "amd64"), desktopName("linux", "arm64")} {
		files[name+".tar.gz"] = archive
		files[name+".sha256"] = []byte(sum(archive) + "  " + name + ".tar.gz\n")
	}
	name := desktopName("windows", "amd64")
	files[name+".zip"] = archiveZip
	files[name+".sha256"] = []byte(sum(archiveZip) + "  " + name + ".zip\n")
	return files
}

func hostDesktop() (asset, sums string) {
	goos, arch := hostTarget()
	ext, _ := packaging(goos)
	return desktopName(goos, arch) + ext, desktopName(goos, arch) + ".sha256"
}

func newDesktopFetch(t *testing.T, version, commit string) *fixture {
	t.Helper()
	f := newFetch(t)
	for name, body := range fakeDesktop(t, version, commit) {
		f.srv.set(name, body)
	}
	return f
}

func (f *fixture) desktop() string {
	_, exe := packaging(hostTargetOS())
	return filepath.Join(filepath.Dir(f.target), desktopasset.Name+exe)
}

func hostTargetOS() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

func (f *fixture) asksForDesktop() bool {
	return slices.ContainsFunc(f.srv.asked(), func(p string) bool { return strings.Contains(p, desktopasset.Name+"-") })
}

func installDesktop(t *testing.T, f *fixture, version, commit string) {
	t.Helper()
	path := f.desktop()
	b := fakebin.Embed(t, "desktop", map[string]string{"version": version, "commit": commit})
	if err := os.WriteFile(path, b, 0o755); err != nil {
		t.Fatal(err)
	}
}

func skipUnlessDesktop(t *testing.T) {
	t.Helper()
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH) {
		t.Skip("hand-desktop assets are Linux only")
	}
}

func TestFetchDesktopChecksItsSha256(t *testing.T) {
	srv := release(t, fakeDesktop(t, "0.9.0", "0123456789ab"))
	for channel, prefix := range map[string]string{"stable": "/latest/download/", "edge": "/download/edge/"} {
		dir := t.TempDir()
		bin, err := FetchDesktop(context.Background(), srv.URL, channel, "linux", "amd64", dir)
		if err != nil || bin != filepath.Join(dir, desktopasset.Name) {
			t.Fatalf("%s: FetchDesktop = %q, %v", channel, bin, err)
		}
		if fi, err := os.Stat(bin); err != nil || fi.Size() == 0 {
			t.Fatalf("%s: %v", channel, err)
		}
		if !slices.Contains(srv.asked(), prefix+desktopName("linux", "amd64")+".tar.gz") || !slices.Contains(srv.asked(), prefix+desktopName("linux", "amd64")+".sha256") {
			t.Fatalf("%s: asked %q", channel, srv.asked())
		}
	}
	asset, sums := desktopName("linux", "amd64")+".tar.gz", desktopName("linux", "amd64")+".sha256"
	srv.set(sums, []byte(strings.Repeat("0", 64)+"  "+asset+"\n"))
	dir := t.TempDir()
	if _, err := FetchDesktop(context.Background(), srv.URL, "stable", "linux", "amd64", dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch for "+asset) {
		t.Fatalf("a wrong checksum gave %v", err)
	}
	absent(t, filepath.Join(dir, desktopasset.Name))
}

func TestFetchDesktopSaysWhenTheReleaseHasNone(t *testing.T) {
	srv := release(t, map[string][]byte{})
	if _, err := FetchDesktop(context.Background(), srv.URL, "edge", "linux", "amd64", t.TempDir()); !errors.Is(err, ErrMissing) {
		t.Fatalf("an absent asset gave %v, want ErrMissing", err)
	}
	files := fakeDesktop(t, "0.9.0", "0123456789ab")
	delete(files, desktopName("linux", "amd64")+".sha256")
	srv = release(t, files)
	if _, err := FetchDesktop(context.Background(), srv.URL, "edge", "linux", "amd64", t.TempDir()); err == nil || errors.Is(err, ErrMissing) {
		t.Fatalf("an archive without its checksum gave %v, want a real error", err)
	}
}

func TestRunInstallsTheDesktopNextToTheNewHand(t *testing.T) {
	skipUnlessDesktop(t)
	for _, had := range []bool{true, false} {
		f := newDesktopFetch(t, "0.9.0", "0123456789ab")
		if had {
			if err := os.WriteFile(f.desktop(), []byte("the old desktop"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Status != "updated" || rep.Desktop != "0.9.0 0123456789ab installed" {
			t.Fatalf("had %v: report = %+v, %v", had, rep, err)
		}
		if b, err := os.ReadFile(f.desktop()); err != nil || string(b) == "the old desktop" || len(b) == 0 {
			t.Fatalf("had %v: the desktop next to hand = %d bytes, %v", had, len(b), err)
		}
		if fi, err := os.Stat(f.desktop()); err != nil || fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("had %v: the desktop is not executable: %v, %v", had, fi, err)
		}
		if out := Render(rep).String(); !strings.Contains(out, "desktop: 0.9.0 0123456789ab installed\n") {
			t.Fatalf("had %v: render:\n%s", had, out)
		}
	}
}

func TestRunInstallsTheDesktopWhenTheHandIsCurrent(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	f.o.From = currentBuild()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Desktop != "0.9.0 0123456789ab installed" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if !f.asksForDesktop() {
		t.Fatalf("a current hand did not fetch the missing desktop: %q", f.srv.asked())
	}
	if b, err := os.ReadFile(f.desktop()); err != nil || len(b) == 0 {
		t.Fatalf("the desktop next to hand = %d bytes, %v", len(b), err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand changed: %v", err)
	}
}

func TestRunReplacesADesktopThatIsNotTheVersionTheCurrentHandNeeds(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	installDesktop(t, f, "0.9.0", "abcdef012345")
	f.o.From = currentBuild()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Desktop != "0.9.0 0123456789ab installed" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if b, err := os.ReadFile(f.desktop()); err != nil || !strings.Contains(string(b), `"0123456789ab"`) {
		t.Fatalf("the desktop next to hand was not replaced: %q, %v", b, err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand changed: %v", err)
	}
}

func TestRunKeepsADesktopThatMatchesTheCurrentHand(t *testing.T) {
	skipUnlessDesktop(t)
	f := newFetch(t)
	installDesktop(t, f, "0.9.0", "0123456789ab")
	f.o.From = currentBuild()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Desktop != "" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("a matching desktop was fetched: %q", f.srv.asked())
	}
	f.unchanged(t)
}

func TestRunCheckSaysItWouldInstallAMissingDesktop(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	f.o.From = currentBuild()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Desktop != "would install" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	asset, _ := hostDesktop()
	url := releaseURL("/", f.o.Channel) + "/" + asset
	if !slices.Contains(f.srv.asked(), url) || !slices.Contains(f.srv.askedMethods(), http.MethodHead) {
		t.Fatalf("--check did not probe the desktop asset: %q, %q", f.srv.asked(), f.srv.askedMethods())
	}
	absent(t, f.desktop())
	f.unchanged(t)
	if out := Render(rep).String(); !strings.Contains(out, "desktop: would install\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunCheckSaysTheReleaseHasNoDesktop(t *testing.T) {
	skipUnlessDesktop(t)
	f := newFetch(t)
	f.o.From = currentBuild()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Desktop != "none in this release" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, f.desktop())
	f.unchanged(t)
	if out := Render(rep).String(); !strings.Contains(out, "desktop: none in this release\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunKeepsTheOldBinariesOnADesktopChecksumFailure(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	asset, sums := hostDesktop()
	f.srv.set(sums, []byte(strings.Repeat("0", 64)+"  "+asset+"\n"))
	if err := os.WriteFile(f.desktop(), []byte("the old desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "checksum mismatch for "+asset) {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
	if b, _ := os.ReadFile(f.desktop()); string(b) != "the old desktop" {
		t.Fatalf("the old desktop was replaced: %q", b)
	}
}

func TestRunKeepsTheOldBinariesWhenTheDesktopIsNotTheVersionHandNeeds(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.8.0", "0123456789ab")
	if err := os.WriteFile(f.desktop(), []byte("the old desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), f.o)
	if err == nil || !strings.Contains(err.Error(), "the downloaded hand-desktop is 0.8.0 0123456789ab, but the downloaded hand is 0.9.0 0123456789ab") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
	if b, _ := os.ReadFile(f.desktop()); string(b) != "the old desktop" {
		t.Fatalf("the old desktop was replaced: %q", b)
	}
}

func TestRunKeepsTheOldBinariesWhenTheDesktopDoesNotRun(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	asset, sums := hostDesktop()
	archive := tarball(t, desktopasset.Name, "not a program")
	f.srv.set(asset, archive)
	f.srv.set(sums, []byte(sum(archive)+"  "+asset+"\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "the downloaded hand-desktop does not run") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunDoesNotReplaceHandWhenTheDesktopCannotBePlaced(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	if err := os.MkdirAll(filepath.Join(f.desktop(), "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "cannot replace "+f.desktop()) {
		t.Fatalf("err = %v", err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand was replaced although the desktop could not be: %v", err)
	}
	if _, err := os.Stat(journalPath(f.root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a journal is left: %v", err)
	}
}

func TestRunSaysWhenTheTargetHasNoDesktop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture's target script has no .exe")
	}
	f := newFetch(t)
	f.o.OS, f.o.Arch = "darwin", "arm64"
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "unsupported on darwin/arm64" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("an unsupported platform fetched a desktop: %q", f.srv.asked())
	}
	if out := Render(rep).String(); !strings.Contains(out, "desktop: unsupported on darwin/arm64\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunSaysWhenThePlatformHasNoDesktop(t *testing.T) {
	f := newFetch(t)
	if desktopasset.Supported(f.o.OS, f.o.Arch) {
		t.Skip("this update target supports hand-desktop")
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "unsupported on "+f.o.OS+"/"+f.o.Arch {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("an unsupported platform fetched a desktop: %q", f.srv.asked())
	}
	if out := Render(rep).String(); !strings.Contains(out, "desktop: unsupported on ") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunFinishesTheStaleFleetCycleWhenTheDesktopCannotBeVerified(t *testing.T) {
	skipUnlessDesktop(t)
	f := newCycle(t)
	f.current()
	f.stale()
	f.serverRuns(t)
	for name, body := range fakeDesktop(t, "0.9.0", "ffffffffffff") {
		f.srv.set(name, body)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatalf("Run = %+v, %v", rep, err)
	}
	if !rep.Failed || !strings.HasPrefix(rep.Desktop, "failed: ") || !strings.Contains(rep.Desktop, "0.9.0 ffffffffffff") {
		t.Fatalf("report = %+v", rep)
	}
	if !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "hand-desktop was not replaced") }) {
		t.Fatalf("help = %q", rep.Help)
	}
	if got := alphaLuvus(t, rep); got != "switched" {
		t.Fatalf("the stale fleet was not switched: %q in %+v", got, rep.Fleets)
	}
	absent(t, f.desktop())
	f.unchanged(t)
}

func TestRunKeepsTheLauncherEntryInStepWithTheDesktop(t *testing.T) {
	skipUnlessDesktop(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	entry := filepath.Join(f.data, "applications", "hand-desktop.desktop")
	pic := filepath.Join(f.data, "icons", "hicolor", "512x512", "apps", "hand.png")
	f.o.Check = true
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	absent(t, entry)
	f.o.Check = false
	rep, err := Run(context.Background(), f.o)
	if err != nil || len(rep.Help) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if b, err := os.ReadFile(entry); err != nil || !strings.Contains(string(b), "Exec="+f.desktop()+"\n") {
		t.Fatalf("entry = %q, %v", b, err)
	}
	if _, err := os.Stat(pic); err != nil {
		t.Fatal(err)
	}
	f.o.From = currentBuild()
	if err := os.WriteFile(entry, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(entry); !strings.Contains(string(b), "Exec="+f.desktop()+"\n") {
		t.Fatalf("a current hand did not refresh the entry: %q", b)
	}
}

func TestRunRemovesTheLauncherEntryWithoutADesktop(t *testing.T) {
	skipUnlessDesktop(t)
	f := newFetch(t)
	if err := launcher.Sync(f.o.Getenv, f.desktop()); err != nil {
		t.Fatal(err)
	}
	f.o.From = currentBuild()
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(f.data, "applications", "hand-desktop.desktop"))
	absent(t, filepath.Join(f.data, "icons", "hicolor", "512x512", "apps", "hand.png"))
}
