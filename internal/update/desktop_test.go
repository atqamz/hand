package update

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/desktopasset"
	"github.com/atqamz/hand/internal/fakebin"
	"github.com/atqamz/hand/internal/launcher"
	"github.com/atqamz/hand/internal/state"
)

func desktopName(goos, arch string) string { return desktopasset.Name + "-" + goos + "-" + arch }

func fakeDesktop(t *testing.T, version, commit string) map[string][]byte {
	t.Helper()
	script := string(fakebin.Embed(t, "desktop", map[string]string{"version": version, "commit": commit}))
	archive, archiveZip := tarball(t, desktopasset.Name, script), zipball(t, desktopasset.Name+".exe", script)
	files := map[string][]byte{}
	for _, name := range []string{desktopName("linux", "amd64"), desktopName("linux", "arm64"), desktopName("darwin", "arm64"), desktopName("darwin", "amd64")} {
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
	return desktopasset.Path(f.target, hostTargetOS(), f.o.Getenv("HOME"))
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
	if runtime.GOOS == "darwin" {
		t.Skip("the macOS bundle is tested in desktop_darwin_test.go")
	}
	if !desktopasset.Supported(runtime.GOOS, runtime.GOARCH, "edge") {
		t.Skip("hand-desktop assets are not built for this OS and architecture")
	}
}

func skipUnlessLauncher(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the launcher entry is written on Linux only")
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
		if fi, err := os.Stat(f.desktop()); err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0) {
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
	if runtime.GOOS == "windows" {
		archive = zipball(t, desktopasset.Name+".exe", "not a program")
	}
	f.srv.set(asset, archive)
	f.srv.set(sums, []byte(sum(archive)+"  "+asset+"\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "the downloaded hand-desktop does not run") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunDoesNotReplaceHandWhenTheDesktopCannotBePlaced(t *testing.T) {
	skipUnlessDesktop(t)
	if runtime.GOOS == "windows" {
		t.Skip("Windows moves a folder in the way aside as .old and places the file")
	}
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

func TestRunSaysWhenThePlatformHasNoDesktop(t *testing.T) {
	f := newFetch(t)
	if desktopasset.Supported(f.o.OS, f.o.Arch, f.o.Channel) {
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

func unsupportedTarget(t *testing.T) *fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the downloaded hand for another OS cannot run on a Windows host")
	}
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	files := fakeHand(t, "0.9.0", "edge", "0123456789ab", "26", "0.14.4")
	archive := files["hand-linux-amd64.tar.gz"]
	f.srv.set("hand-linux-386.tar.gz", archive)
	f.srv.set("checksums.txt", append(files["checksums.txt"], []byte(sum(archive)+"  hand-linux-386.tar.gz\n")...))
	f.o.OS, f.o.Arch = "linux", "386"
	if desktopasset.Supported(f.o.OS, f.o.Arch, f.o.Channel) {
		t.Fatal("linux/386 has a desktop")
	}
	return f
}

func TestRunSaysWhenTheTargetHasNoDesktop(t *testing.T) {
	f := unsupportedTarget(t)
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Desktop != "unsupported on linux/386" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("an unsupported target fetched a desktop: %q", f.srv.asked())
	}
	if out := Render(rep).String(); !strings.Contains(out, "desktop: unsupported on linux/386\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunNeverRemovesAnInstalledWindowOffWindows(t *testing.T) {
	for _, current := range []bool{false, true} {
		for _, check := range []bool{false, true} {
			f := unsupportedTarget(t)
			if current {
				f.o.From = Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 26, Luvus: "0.14.4"}
			}
			f.o.Check = check
			path := filepath.Join(filepath.Dir(f.target), desktopasset.Name)
			if err := os.WriteFile(path, []byte("the installed desktop"), 0o755); err != nil {
				t.Fatal(err)
			}
			rep, err := Run(context.Background(), f.o)
			if err != nil || rep.Failed || rep.Desktop != "unsupported on linux/386" {
				t.Fatalf("current %v, check %v: report = %+v, %v", current, check, rep, err)
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != "the installed desktop" {
				t.Fatalf("current %v, check %v: the installed window changed: %q, %v", current, check, b, err)
			}
		}
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
	skipUnlessLauncher(t)
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	entry := filepath.Join(f.data, "applications", "hand-desktop.desktop")
	pic := filepath.Join(f.data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png")
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
	if err := os.WriteFile(entry, []byte("[Desktop Entry]\nExec=/old\nX-Hand-Managed=true\n"), 0o644); err != nil {
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
	skipUnlessLauncher(t)
	f := newFetch(t)
	if _, err := launcher.Sync(f.o.Getenv, f.desktop()); err != nil {
		t.Fatal(err)
	}
	f.o.From = currentBuild()
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	absent(t, filepath.Join(f.data, "applications", "hand-desktop.desktop"))
	absent(t, filepath.Join(f.data, "icons", "hicolor", "512x512", "apps", "hand-desktop.png"))
}

func TestRunSaysAStableBuildHasNoDesktopOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the downloaded hand must run, so this needs a Windows host")
	}
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	for name, body := range fakeHand(t, "0.9.0", "stable", "0123456789ab", strconv.Itoa(state.SchemaVersion), "0.14.4") {
		f.srv.set(name, body)
	}
	f.o.Channel = "stable"
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Desktop != "unsupported on windows/amd64" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("a stable build on Windows fetched a desktop: %q", f.srv.asked())
	}
	absent(t, f.desktop())
}

func TestRunRemovesTheInstalledWindowOnAStableWindowsUpdate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the downloaded hand must run, so this needs a Windows host")
	}
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	for name, body := range fakeHand(t, "0.9.0", "stable", "0123456789ab", strconv.Itoa(state.SchemaVersion), "0.14.4") {
		f.srv.set(name, body)
	}
	f.o.Channel = "stable"
	if err := os.WriteFile(f.desktop(), []byte("the edge desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !strings.Contains(rep.Desktop, "would remove the installed hand-desktop.exe") {
		t.Fatalf("check report = %+v, %v", rep, err)
	}
	if b, _ := os.ReadFile(f.desktop()); string(b) != "the edge desktop" {
		t.Fatalf("a check touched the installed window: %q", b)
	}
	f.o.Check = false
	rep, err = Run(context.Background(), f.o)
	if err != nil || rep.Failed || !strings.Contains(rep.Desktop, "removed the installed hand-desktop.exe") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if f.asksForDesktop() {
		t.Fatalf("a stable build on Windows fetched a desktop: %q", f.srv.asked())
	}
	absent(t, f.desktop())
}

func TestRemoveDeletesTheInstalledWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hand-desktop")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := remove(path); err != nil {
		t.Fatal(err)
	}
	absent(t, path)
}

func windowFixture(t *testing.T, channel string) *fixture {
	t.Helper()
	f := newDesktopFetch(t, "0.9.0", "0123456789ab")
	for name, body := range fakeHand(t, "0.9.0", channel, "0123456789ab", strconv.Itoa(state.SchemaVersion), "0.14.4", "0.1.0") {
		f.srv.set(name, body)
	}
	f.o.OS, f.o.Arch, f.o.Channel = "windows", "amd64", channel
	if err := os.WriteFile(desktopasset.Path(f.target, "windows", ""), []byte("the edge desktop"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) windowKept(t *testing.T) {
	t.Helper()
	if b, err := os.ReadFile(desktopasset.Path(f.target, "windows", "")); err != nil || string(b) != "the edge desktop" {
		t.Fatalf("the installed window changed: %q, %v", b, err)
	}
}

func (f *fixture) isCurrent() {
	f.o.From = Build{Version: "0.9.0", Channel: f.o.Channel, Commit: "0123456789ab", Schema: state.SchemaVersion, Luvus: "0.14.4"}
}

func TestRunRemovesTheInstalledWindowOnAStableUpdateOfAWindowsTarget(t *testing.T) {
	f := windowFixture(t, "stable")
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !strings.Contains(rep.Desktop, "would remove the installed hand-desktop.exe") {
		t.Fatalf("check report = %+v, %v", rep, err)
	}
	f.windowKept(t)
	f.o.Check = false
	rep, err = Run(context.Background(), f.o)
	if err != nil || rep.Failed || rep.Desktop != "unsupported on windows/amd64; removed the installed hand-desktop.exe" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, desktopasset.Path(f.target, "windows", ""))
	if f.asksForDesktop() {
		t.Fatalf("a stable build on Windows fetched a desktop: %q", f.srv.asked())
	}
}

func TestRunRemovesTheInstalledWindowWhenHandIsAlreadyStable(t *testing.T) {
	f := windowFixture(t, "stable")
	f.isCurrent()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || !strings.Contains(rep.Desktop, "would remove the installed hand-desktop.exe") {
		t.Fatalf("check report = %+v, %v", rep, err)
	}
	f.windowKept(t)
	f.o.Check = false
	rep, err = Run(context.Background(), f.o)
	if err != nil || rep.Failed || !strings.Contains(rep.Desktop, "removed the installed hand-desktop.exe") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, desktopasset.Path(f.target, "windows", ""))
}

func TestRunKeepsTheInstalledWindowOnAnEdgeUpdate(t *testing.T) {
	f := windowFixture(t, "edge")
	rep, err := Run(context.Background(), f.o)
	if err != nil || strings.Contains(rep.Desktop, "removed") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	f.isCurrent()
	rep, err = Run(context.Background(), f.o)
	if err != nil || strings.Contains(rep.Desktop, "removed") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if _, err := os.Stat(desktopasset.Path(f.target, "windows", "")); err != nil {
		t.Fatalf("an edge update removed the window: %v", err)
	}
}

func TestRunKeepsTheInstalledWindowWhenADownloadFails(t *testing.T) {
	f := windowFixture(t, "stable")
	f.srv.set("hand-iroh-windows-amd64.zip", []byte("garbage"))
	f.srv.set("hand-iroh-windows-amd64.sha256", []byte(sum([]byte("other"))+"  hand-iroh-windows-amd64.zip\n"))
	if _, err := Run(context.Background(), f.o); err == nil {
		t.Fatal("a corrupt sidecar did not fail the update")
	}
	f.windowKept(t)
}

func TestRunKeepsTheInstalledWindowWhenTheLuvusDownloadFails(t *testing.T) {
	f := windowFixture(t, "stable")
	f.pin = keepLuvus(t, f.root, "0.14.3")
	newFleet(t, f.root, "alpha")
	name := "luvus-v0.14.4-x86_64-pc-windows-msvc"
	f.srv.set(name+".zip", []byte("garbage"))
	f.srv.set(name+".sha256", []byte(sum([]byte("other"))+"  "+name+".zip\n"))
	if _, err := Run(context.Background(), f.o); err == nil {
		t.Fatal("a corrupt Luvus did not fail the update")
	}
	f.windowKept(t)
}

func TestRunReportsAFailedRemovalOfTheInstalledWindowAndFinishes(t *testing.T) {
	f := windowFixture(t, "stable")
	f.isCurrent()
	was := removeWindow
	t.Cleanup(func() { removeWindow = was })
	removeWindow = func(string) error { return errors.New("access is denied") }
	rep, err := Run(context.Background(), f.o)
	if err != nil {
		t.Fatalf("a failed removal aborted the update: %v", err)
	}
	if !rep.Failed || !strings.Contains(rep.Desktop, "failed to remove the installed hand-desktop.exe: access is denied") || !slices.ContainsFunc(rep.Help, func(h string) bool { return strings.Contains(h, "was not removed") }) {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Status != "up to date" {
		t.Fatalf("status = %q", rep.Status)
	}
	f.windowKept(t)
}

func skipUnlessWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the Start Menu shortcut is written on Windows only, and the downloaded hand must run")
	}
}

func TestRunWritesTheStartMenuShortcutOnAWindowsEdgeUpdate(t *testing.T) {
	skipUnlessWindows(t)
	f := windowFixture(t, "edge")
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || len(rep.Help) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if state, where := launcher.Check(f.o.Getenv, desktopasset.Path(f.target, "windows", "")); state != launcher.OK {
		t.Fatalf("the shortcut is %q at %s", state, where)
	}
	f.o.Check = true
	f.isCurrent()
	if err := os.Remove(filepath.Join(f.data, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if state, _ := launcher.Check(f.o.Getenv, desktopasset.Path(f.target, "windows", "")); state != launcher.Missing {
		t.Fatalf("--check wrote the shortcut: %q", state)
	}
}

func TestRunRemovesTheStartMenuShortcutWithTheWindowOnAStableWindowsUpdate(t *testing.T) {
	skipUnlessWindows(t)
	for _, current := range []bool{false, true} {
		f := windowFixture(t, "stable")
		bin := desktopasset.Path(f.target, "windows", "")
		if _, err := launcher.Sync(f.o.Getenv, bin); err != nil {
			t.Fatal(err)
		}
		lnk := filepath.Join(f.data, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")
		if current {
			f.isCurrent()
		}
		f.o.Check = true
		if _, err := Run(context.Background(), f.o); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(lnk); err != nil {
			t.Fatalf("--check removed the shortcut: %v", err)
		}
		f.o.Check = false
		if rep, err := Run(context.Background(), f.o); err != nil || rep.Failed || len(rep.Help) != 0 {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		absent(t, bin)
		absent(t, lnk)
		absent(t, filepath.Join(filepath.Dir(bin), "hand.ico"))
	}
}

func TestRunLeavesAUsersOwnStartMenuShortcutAloneOnAStableWindowsUpdate(t *testing.T) {
	skipUnlessWindows(t)
	f := windowFixture(t, "stable")
	lnk := filepath.Join(f.data, "Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")
	if err := os.MkdirAll(filepath.Dir(lnk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lnk, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Failed || len(rep.Help) != 1 || !strings.Contains(rep.Help[0], lnk+" is not Hand's launcher entry") {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if b, _ := os.ReadFile(lnk); string(b) != "mine" {
		t.Fatalf("the user's shortcut changed: %q", b)
	}
}

func TestRunRemovesALauncherEntryLeftWithoutAWindowOnAStableUpdate(t *testing.T) {
	var entry string
	switch runtime.GOOS {
	case "linux":
		entry = filepath.Join("applications", "hand-desktop.desktop")
	case "windows":
		entry = filepath.Join("Microsoft", "Windows", "Start Menu", "Programs", "Hand.lnk")
	default:
		t.Skip("the launcher entry is written on Linux and Windows only")
	}
	f := windowFixture(t, "stable")
	f.isCurrent()
	bin := desktopasset.Path(f.target, "windows", "")
	if _, err := launcher.Sync(f.o.Getenv, bin); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	f.o.Check = true
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.data, entry)); err != nil {
		t.Fatalf("--check removed the entry: %v", err)
	}
	f.o.Check = false
	if rep, err := Run(context.Background(), f.o); err != nil || rep.Failed || len(rep.Help) != 0 {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	absent(t, filepath.Join(f.data, entry))
}
