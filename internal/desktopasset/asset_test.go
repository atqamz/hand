package desktopasset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/fakebin"
)

func TestMain(m *testing.M) {
	fakebin.Main(map[string]func([]string) int{"desktop": fakeDesktop, "raw": func([]string) int {
		fmt.Print(fakebin.Params()["raw"])
		return 0
	}})
	os.Exit(m.Run())
}

func fakeDesktop(args []string) int {
	p := fakebin.Params()
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(Name + " " + p["version"] + " " + p["commit"])
		return 0
	}
	return 2
}

func install(t *testing.T, dir, behavior string, params map[string]string) string {
	t.Helper()
	return fakebin.Install(t, dir, Name, behavior, params)
}

func TestStampTrimsTheCommitTheWayHandVersionDoes(t *testing.T) {
	version, commit := Version, Commit
	t.Cleanup(func() { Version, Commit = version, commit })
	Version, Commit = "0.9.0", "0123456789abcdef0123"
	if got, want := Stamp(), Name+" 0.9.0 0123456789ab"; got != want {
		t.Fatalf("Stamp = %q, want %q", got, want)
	}
	Commit = ""
	if got := Stamp(); !strings.HasPrefix(got, Name+" 0.9.0 ") || strings.HasSuffix(got, " ") {
		t.Fatalf("Stamp without a stamped commit = %q", got)
	}
}

func TestBinaryVersionReadsTheStamp(t *testing.T) {
	bin := install(t, t.TempDir(), "desktop", map[string]string{"version": "0.9.0", "commit": "0123456789ab"})
	version, commit, err := BinaryVersion(context.Background(), bin)
	if err != nil || version != "0.9.0" || commit != "0123456789ab" {
		t.Fatalf("BinaryVersion = %q, %q, %v", version, commit, err)
	}
}

func TestBinaryVersionRefusesAnotherProgram(t *testing.T) {
	bin := install(t, t.TempDir(), "raw", map[string]string{"raw": "hand-iroh 0.1.0 iroh 1.4.1\n"})
	if _, _, err := BinaryVersion(context.Background(), bin); !errors.Is(err, ErrNotDesktop) || !strings.Contains(err.Error(), "not a hand-desktop binary") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRequiresTheVersionAndCommitOfThisHand(t *testing.T) {
	bin := install(t, t.TempDir(), "desktop", map[string]string{"version": "0.9.0", "commit": "0123456789ab"})
	if got, err := Verify(context.Background(), bin, "0.9.0", "0123456789ab"); err != nil || got != "0.9.0 0123456789ab" {
		t.Fatalf("Verify = %q, %v", got, err)
	}
	for _, c := range []struct{ version, commit string }{{"0.9.1", "0123456789ab"}, {"0.9.0", "abcdef012345"}} {
		if _, err := Verify(context.Background(), bin, c.version, c.commit); !errors.Is(err, ErrVersion) || !strings.Contains(err.Error(), "0.9.0 0123456789ab") {
			t.Fatalf("%s %s: err = %v", c.version, c.commit, err)
		}
	}
}

func TestSupportedIsLinuxOnly(t *testing.T) {
	for _, c := range []struct {
		goos, goarch string
		ok           bool
	}{
		{"linux", "amd64", true},
		{"linux", "arm64", true},
		{"linux", "386", false},
		{"darwin", "arm64", false},
		{"windows", "amd64", false},
	} {
		if got := Supported(c.goos, c.goarch); got != c.ok {
			t.Fatalf("Supported(%s, %s) = %v", c.goos, c.goarch, got)
		}
	}
	if why := Unsupported("darwin", "arm64"); !strings.Contains(why, "darwin/arm64") || !strings.Contains(why, "Linux") {
		t.Fatalf("Unsupported = %q", why)
	}
}

func TestLocatePrefersTheFolderOfHandThenPath(t *testing.T) {
	self := filepath.Join(t.TempDir(), "hand")
	next := install(t, filepath.Dir(self), "desktop", nil)
	onPath := install(t, t.TempDir(), "desktop", nil)
	got, err := Locate(self, filepath.Dir(onPath), runtime.GOOS)
	if err != nil || got != next {
		t.Fatalf("Locate next to hand = %q, %v", got, err)
	}
	os.Remove(next)
	got, err = Locate(self, filepath.Dir(onPath), runtime.GOOS)
	if err != nil || got != onPath {
		t.Fatalf("Locate on PATH = %q, %v", got, err)
	}
	if _, err := Locate(self, t.TempDir(), runtime.GOOS); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocateRefusesSomethingThatIsNotExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	self := filepath.Join(t.TempDir(), "hand")
	path := filepath.Join(filepath.Dir(self), Name)
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(self, t.TempDir(), runtime.GOOS); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestPathIsNextToHand(t *testing.T) {
	for goos, want := range map[string]string{"linux": "hand-desktop", "windows": "hand-desktop.exe"} {
		got := filepath.Base(Path(filepath.Join("bin", "hand"), goos))
		if got != want {
			t.Fatalf("%s: Path = %q, want %q", goos, got, want)
		}
	}
}

func TestLocateTakesTheSystemAsAnArgument(t *testing.T) {
	self := filepath.Join(t.TempDir(), "hand")
	exe := filepath.Join(filepath.Dir(self), Name+".exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Locate(self, t.TempDir(), "windows"); err != nil || got != exe {
		t.Fatalf("Locate for windows = %q, %v", got, err)
	}
	if _, err := Locate(self, t.TempDir(), "linux"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Locate for linux found the .exe: %v", err)
	}
}
