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
	if got, live := livePID(path); got.pid != os.Getpid() || got.marker != marker || got.exe != exe || got.foreign || !live {
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
	here := boot.Stable()
	if here == "" {
		t.Skip("this system writes no boot id into pid files")
	}
	write := func(id string) {
		t.Helper()
		line := strconv.Itoa(os.Getpid()) + " " + marker + " /bin/hand\nboot " + id + " desk\n"
		if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(here)
	if got, live := livePID(path); !live || got.foreign || got.marker != marker || got.exe != "/bin/hand" || got.pid != os.Getpid() {
		t.Fatalf("this boot: %+v %v", got, live)
	}
	if _, foreign := Foreign(path); foreign {
		t.Fatal("this boot reads as foreign")
	}
	write("00000000-0000-4000-8000-000000000000")
	if got, live := livePID(path); live || !got.foreign || got.host != "desk" || got.pid != os.Getpid() {
		t.Fatalf("another boot: %+v %v, want a foreign entry of host desk that is not live", got, live)
	}
	if host, foreign := Foreign(path); !foreign || host != "desk" {
		t.Fatalf("Foreign = %q %v", host, foreign)
	}
}

func TestAPIDFileWritesNoWindowsTickBoot(t *testing.T) {
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
	if strings.Contains(string(b), "ticks:") {
		t.Fatalf("pid file %q carries a clock-derived boot id", b)
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+" "+mustMarker(t)+" /bin/hand\nboot ticks:1700000000 desk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, foreign := Foreign(path); boot.Stable() == "" && foreign {
		t.Fatal("a tick boot id made a live process foreign")
	}
}

func mustMarker(t *testing.T) string {
	t.Helper()
	m, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func baseLivePID(path string) (pid int, marker, exe string, live bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	f := strings.SplitN(strings.TrimSuffix(string(b), "\n"), " ", 3)
	if len(f) < 2 {
		return
	}
	pid, err = strconv.Atoi(f[0])
	if err != nil || pid <= 0 {
		return 0, "", "", false
	}
	if len(f) == 3 {
		exe = f[2]
	}
	m, err := luvus.ProcStartMarker(pid)
	return pid, f[1], exe, err == nil && m == f[1]
}

func TestAPIDFileKeepsTheLineAnOlderHandReadsAsLive(t *testing.T) {
	if boot.Stable() == "" {
		t.Skip("this system writes no boot id into pid files")
	}
	path := filepath.Join(t.TempDir(), "watch.pid")
	forget, err := WritePID(path)
	if err != nil {
		t.Fatal(err)
	}
	defer forget()
	self, err := os.Executable()
	if err == nil {
		self, err = Target(self)
	}
	if err != nil {
		t.Fatal(err)
	}
	pid, marker, exe, live := baseLivePID(path)
	if pid != os.Getpid() || marker != mustMarker(t) || !live {
		t.Fatalf("an older hand reads %d %q live=%v, want this process live", pid, marker, live)
	}
	if exe == self {
		t.Fatalf("an older hand reads exe %q, which matches this binary and would let it stop or swap a process of this build", exe)
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
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if id := boot.Stable(); id != "" && (len(lines) != 2 || !strings.HasPrefix(lines[1], "boot "+id+" ")) {
		t.Fatalf("pid file %q does not carry boot %q on a second line", b, id)
	}
	if strings.Contains(lines[0], "@") {
		t.Fatalf("first line %q changed from the older format", lines[0])
	}
}
