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

	"github.com/atqamz/hand/internal/fakebin"
)

func irohName(goos, arch string) string { return "hand-iroh-" + goos + "-" + arch }

func fakeIroh(t *testing.T, version string) map[string][]byte {
	t.Helper()
	script := string(fakebin.Embed(t, "sidecar", map[string]string{"version": version}))
	archive, archiveZip := tarball(t, "hand-iroh", script), zipball(t, "hand-iroh.exe", script)
	files := map[string][]byte{}
	for _, name := range []string{irohName("linux", "amd64"), irohName("darwin", "arm64")} {
		files[name+".tar.gz"] = archive
		files[name+".sha256"] = []byte(sum(archive) + "  " + name + ".tar.gz\n")
	}
	name := irohName("windows", "amd64")
	files[name+".zip"] = archiveZip
	files[name+".sha256"] = []byte(sum(archiveZip) + "  " + name + ".zip\n")
	return files
}

func hostIroh() (asset, sums, exe string) {
	goos, arch := hostTarget()
	ext, exe := packaging(goos)
	return irohName(goos, arch) + ext, irohName(goos, arch) + ".sha256", exe
}

func newIrohFetch(t *testing.T, handIroh, assetIroh string) *fixture {
	t.Helper()
	f := newFetch(t)
	for name, body := range fakeHand(t, "0.9.0", "edge", "0123456789ab", "20", "0.14.4", handIroh) {
		f.srv.set(name, body)
	}
	if assetIroh != "" {
		for name, body := range fakeIroh(t, assetIroh) {
			f.srv.set(name, body)
		}
	}
	return f
}

func (f *fixture) sidecar() string {
	_, _, exe := hostIroh()
	return filepath.Join(filepath.Dir(f.target), "hand-iroh"+exe)
}

func (f *fixture) asksForIroh() bool {
	return slices.ContainsFunc(f.srv.asked(), func(p string) bool { return strings.Contains(p, "hand-iroh-") })
}

func installIroh(t *testing.T, f *fixture, version string) {
	t.Helper()
	if err := os.WriteFile(f.sidecar(), fakebin.Embed(t, "sidecar", map[string]string{"version": version}), 0o755); err != nil {
		t.Fatal(err)
	}
}

func currentBuild() Build {
	return Build{Version: "0.9.0", Channel: "edge", Commit: "0123456789ab", Schema: 18, Luvus: "0.14.4", Iroh: "0.1.0"}
}

func TestFetchIrohChecksItsSha256(t *testing.T) {
	srv := release(t, fakeIroh(t, "0.1.0"))
	goos, arch := hostTarget()
	_, exe := packaging(goos)
	asset, sums, _ := hostIroh()
	for channel, prefix := range map[string]string{"stable": "/latest/download/", "edge": "/download/edge/"} {
		dir := t.TempDir()
		bin, err := FetchIroh(context.Background(), srv.URL, channel, goos, arch, dir)
		if err != nil || bin != filepath.Join(dir, "hand-iroh"+exe) {
			t.Fatalf("%s: FetchIroh = %q, %v", channel, bin, err)
		}
		if fi, err := os.Stat(bin); err != nil || fi.Size() == 0 {
			t.Fatalf("%s: %v", channel, err)
		}
		if !slices.Contains(srv.asked(), prefix+asset) || !slices.Contains(srv.asked(), prefix+sums) {
			t.Fatalf("%s: asked %q", channel, srv.asked())
		}
	}
	srv.set(sums, []byte(strings.Repeat("0", 64)+"  "+asset+"\n"))
	dir := t.TempDir()
	if _, err := FetchIroh(context.Background(), srv.URL, "stable", goos, arch, dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch for "+asset) {
		t.Fatalf("a wrong checksum gave %v", err)
	}
	absent(t, filepath.Join(dir, "hand-iroh"+exe))
}

func TestFetchIrohSaysWhenTheReleaseHasNone(t *testing.T) {
	goos, arch := hostTarget()
	asset, sums, _ := hostIroh()
	srv := release(t, map[string][]byte{})
	if _, err := FetchIroh(context.Background(), srv.URL, "edge", goos, arch, t.TempDir()); !errors.Is(err, ErrMissing) {
		t.Fatalf("an absent asset gave %v, want ErrMissing", err)
	}
	files := fakeIroh(t, "0.1.0")
	delete(files, sums)
	srv = release(t, files)
	if _, err := FetchIroh(context.Background(), srv.URL, "edge", goos, arch, t.TempDir()); err == nil || errors.Is(err, ErrMissing) {
		t.Fatalf("an archive without its checksum gave %v, want a real error", err)
	}
	files = fakeIroh(t, "0.1.0")
	files[asset] = nil
	srv = release(t, files)
	if _, err := FetchIroh(context.Background(), srv.URL, "edge", goos, arch, t.TempDir()); err == nil || errors.Is(err, ErrMissing) {
		t.Fatalf("a server error gave %v, want a real error", err)
	}
}

func TestRunInstallsTheSidecarNextToTheNewHand(t *testing.T) {
	for _, had := range []bool{true, false} {
		f := newIrohFetch(t, "0.1.0", "0.1.0")
		if had {
			if err := os.WriteFile(f.sidecar(), []byte("the old sidecar"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		rep, err := Run(context.Background(), f.o)
		if err != nil || rep.Status != "updated" || rep.Iroh != "0.1.0 installed" {
			t.Fatalf("had %v: report = %+v, %v", had, rep, err)
		}
		if b, err := os.ReadFile(f.sidecar()); err != nil || string(b) == "the old sidecar" || len(b) == 0 {
			t.Fatalf("had %v: the sidecar next to hand = %d bytes, %v", had, len(b), err)
		}
		if runtime.GOOS != "windows" {
			if fi, err := os.Stat(f.sidecar()); err != nil || fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("had %v: the sidecar is not executable: %v, %v", had, fi, err)
			}
		}
		if out := Render(rep).String(); !strings.Contains(out, "iroh: 0.1.0 installed\n") {
			t.Fatalf("had %v: render:\n%s", had, out)
		}
		entries, _ := os.ReadDir(filepath.Dir(f.target))
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".hand.update-") || strings.HasPrefix(e.Name(), ".hand-update-") {
				t.Fatalf("had %v: a staging file is left: %s", had, e.Name())
			}
		}
	}
}

func TestRunInstallsTheSidecarWhenTheHandIsCurrent(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "0.1.0")
	f.o.From = currentBuild()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Iroh != "0.1.0 installed" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if !f.asksForIroh() {
		t.Fatalf("a current hand did not fetch the missing sidecar: %q", f.srv.asked())
	}
	if b, err := os.ReadFile(f.sidecar()); err != nil || len(b) == 0 {
		t.Fatalf("the sidecar next to hand = %d bytes, %v", len(b), err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand changed: %v", err)
	}
	if out := Render(rep).String(); !strings.Contains(out, "iroh: 0.1.0 installed\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunReplacesASidecarThatIsNotTheVersionTheCurrentHandNeeds(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "0.1.0")
	installIroh(t, f, "0.0.1")
	f.o.From = currentBuild()
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Iroh != "0.1.0 installed" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if b, err := os.ReadFile(f.sidecar()); err != nil || !strings.Contains(string(b), `"0.1.0"`) {
		t.Fatalf("the sidecar next to hand was not replaced: %q, %v", b, err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand changed: %v", err)
	}
}

func TestRunCheckSaysItWouldInstallAMissingSidecarForACurrentHand(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "0.1.0")
	asset, _, _ := hostIroh()
	f.o.From = currentBuild()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Iroh != "would install" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if !slices.Contains(f.srv.asked(), releaseURL("/", f.o.Channel)+"/"+asset) || !slices.Contains(f.srv.askedMethods(), http.MethodHead) {
		t.Fatalf("--check did not probe the sidecar asset: %q, %q", f.srv.asked(), f.srv.askedMethods())
	}
	absent(t, f.sidecar())
	f.unchanged(t)
	if out := Render(rep).String(); !strings.Contains(out, "iroh: would install\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunCheckSaysTheReleaseHasNoSidecarForACurrentHand(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "")
	asset, _, _ := hostIroh()
	f.o.From = currentBuild()
	f.o.Check = true
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "up to date" || rep.Iroh != "none in this release" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if !slices.Contains(f.srv.asked(), releaseURL("/", f.o.Channel)+"/"+asset) || !slices.Contains(f.srv.askedMethods(), http.MethodHead) {
		t.Fatalf("--check did not probe the sidecar asset: %q, %q", f.srv.asked(), f.srv.askedMethods())
	}
	absent(t, f.sidecar())
	f.unchanged(t)
	if out := Render(rep).String(); !strings.Contains(out, "iroh: none in this release\n") {
		t.Fatalf("render:\n%s", out)
	}
}

func TestRunKeepsTheOldBinariesOnASidecarChecksumFailure(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "0.1.0")
	asset, sums, _ := hostIroh()
	f.srv.set(sums, []byte(strings.Repeat("0", 64)+"  "+asset+"\n"))
	if err := os.WriteFile(f.sidecar(), []byte("the old sidecar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "checksum mismatch for "+asset) {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
	if b, _ := os.ReadFile(f.sidecar()); string(b) != "the old sidecar" {
		t.Fatalf("the old sidecar was replaced: %q", b)
	}
}

func TestRunKeepsTheOldBinariesWhenTheSidecarIsNotTheVersionHandNeeds(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "0.2.0")
	if err := os.WriteFile(f.sidecar(), []byte("the old sidecar"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), f.o)
	if err == nil || !strings.Contains(err.Error(), "the downloaded hand-iroh is 0.2.0, but the downloaded hand 0.9.0 needs 0.1.0") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
	if b, _ := os.ReadFile(f.sidecar()); string(b) != "the old sidecar" {
		t.Fatalf("the old sidecar was replaced: %q", b)
	}
}

func TestRunKeepsTheOldBinariesWhenTheSidecarDoesNotRun(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "")
	asset, sums, _ := hostIroh()
	goos, _ := hostTarget()
	var archive []byte
	if goos == "windows" {
		archive = zipball(t, "hand-iroh.exe", "not a program")
	} else {
		archive = tarball(t, "hand-iroh", "not a program")
	}
	f.srv.set(asset, archive)
	f.srv.set(sums, []byte(sum(archive)+"  "+asset+"\n"))
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "the downloaded hand-iroh does not run") {
		t.Fatalf("err = %v", err)
	}
	f.unchanged(t)
}

func TestRunStillUpdatesWhenTheReleaseCarriesNoSidecar(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "")
	rep, err := Run(context.Background(), f.o)
	if err != nil || rep.Status != "updated" || rep.Iroh != "none in this release" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if _, err := os.Stat(f.sidecar()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a sidecar appeared: %v", err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) == f.old {
		t.Fatalf("hand was not replaced: %v", err)
	}
}

func TestRunLeavesAnInstalledSidecarWhenTheReleaseCarriesNone(t *testing.T) {
	f := newIrohFetch(t, "0.1.0", "")
	if err := os.WriteFile(f.sidecar(), []byte("the old sidecar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f.sidecar()); string(b) != "the old sidecar" {
		t.Fatalf("the sidecar was touched: %q", b)
	}
}

func TestRunAsksForNoSidecarFromAHandThatKnowsNoneOrWhenNothingChanges(t *testing.T) {
	f := newFetch(t)
	for name, body := range fakeIroh(t, "0.1.0") {
		f.srv.set(name, body)
	}
	if _, err := Run(context.Background(), f.o); err != nil {
		t.Fatal(err)
	}
	if f.asksForIroh() {
		t.Fatalf("a hand that reports no sidecar still fetched one: %q", f.srv.asked())
	}
	g := newIrohFetch(t, "0.1.0", "0.1.0")
	installIroh(t, g, "0.1.0")
	g.o.From = currentBuild()
	if rep, err := Run(context.Background(), g.o); err != nil || rep.Status != "up to date" || rep.Iroh != "" {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if g.asksForIroh() {
		t.Fatalf("a current hand with the right sidecar fetched one: %q", g.srv.asked())
	}
	g.unchanged(t)
	h := newIrohFetch(t, "0.1.0", "0.1.0")
	h.o.Check = true
	if _, err := Run(context.Background(), h.o); err != nil {
		t.Fatal(err)
	}
	if h.asksForIroh() {
		t.Fatalf("--check fetched a sidecar for a new hand: %q", h.srv.asked())
	}
	h.unchanged(t)
}

func TestRunDoesNotReplaceHandWhenTheSidecarCannotBePlaced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("renaming a file over a folder behaves differently on Windows")
	}
	f := newIrohFetch(t, "0.1.0", "0.1.0")
	if err := os.MkdirAll(filepath.Join(f.sidecar(), "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), f.o); err == nil || !strings.Contains(err.Error(), "cannot replace "+f.sidecar()) {
		t.Fatalf("err = %v", err)
	}
	if b, err := os.ReadFile(f.target); err != nil || string(b) != f.old {
		t.Fatalf("hand was replaced although the sidecar could not be: %v", err)
	}
	if _, err := os.Stat(journalPath(f.root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a journal is left: %v", err)
	}
}

func TestParseBuildReadsTheSidecarVersion(t *testing.T) {
	if b := parseBuild("version: 0.9.0\nluvus: 0.14.4\niroh: 0.1.0\n"); b.Iroh != "0.1.0" || b.Luvus != "0.14.4" {
		t.Fatalf("build = %+v", b)
	}
	if b := parseBuild("version: 0.9.0\nluvus: 0.14.4\n"); b.Iroh != "" {
		t.Fatalf("build = %+v", b)
	}
}
