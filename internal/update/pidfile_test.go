package update

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/boot"
	"github.com/atqamz/hand/internal/luvus"
)

func TestAPIDFileRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	forget, err := WritePID(path)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = Target(exe)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got, live := livePID(path); got != (pidEntry{pid: os.Getpid(), marker: marker, exe: exe}) || !live {
		t.Fatalf("livePID = %+v %v, want %d %q %q true", got, live, os.Getpid(), marker, exe)
	}
	forget()
	absent(t, path)
}

func TestAPIDFileKeepsASpaceInTheBinaryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(strconv.Itoa(os.Getpid()) + " " + marker + " /opt/my tools/hand\n")
	if err := os.WriteFile(path, line, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, live := livePID(path); !live || got.exe != "/opt/my tools/hand" {
		t.Fatalf("livePID = %+v %v", got, live)
	}
}

func TestAPIDFileFromAnOlderBuildHasNoBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+" "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, live := livePID(path); !live || got.exe != "" || got.pid != os.Getpid() {
		t.Fatalf("livePID = %+v %v", got, live)
	}
}

func TestAPIDFileNeverNamesAProcessGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	for _, pid := range []string{"0", "-1"} {
		if err := os.WriteFile(path, []byte(pid+" marker /bin/hand\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, live := livePID(path); live {
			t.Fatalf("pid %s counted as live", pid)
		}
	}
}

func TestAPIDFileFromAnotherBootNamesNoLiveProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	marker, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	here := boot.ID()
	if here == "" {
		t.Skip("this system reports no boot id")
	}
	write := func(id string) {
		t.Helper()
		line := strconv.Itoa(os.Getpid()) + " " + marker + "@" + id + " /bin/hand\n"
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(here)
	if got, live := livePID(path); !live || got.foreign || got.marker != marker || got.pid != os.Getpid() {
		t.Fatalf("this boot: %+v %v", got, live)
	}
	if Foreign(path) {
		t.Fatal("this boot reads as foreign")
	}
	for _, other := range []string{"00000000-0000-4000-8000-000000000000", "ticks:1700000000"} {
		write(other)
		if got, live := livePID(path); live || !got.foreign || got.pid != os.Getpid() {
			t.Fatalf("boot %s: %+v %v, want a foreign entry that is not live", other, got, live)
		}
		if !Foreign(path) {
			t.Fatalf("boot %s: Foreign = false", other)
		}
	}
}

func TestAPIDFileRecordsTheBootItWasWrittenOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	forget, err := WritePID(path)
	if err != nil {
		t.Fatal(err)
	}
	defer forget()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if id := boot.ID(); id != "" && !strings.Contains(strings.Fields(string(b))[1], "@"+id) {
		t.Fatalf("pid file %q does not carry boot %q", b, id)
	}
}
