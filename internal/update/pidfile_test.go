package update

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

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
	if got, live := livePID(path); got != (pidEntry{os.Getpid(), marker, exe}) || !live {
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
