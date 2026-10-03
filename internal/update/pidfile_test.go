package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

func TestAPIDFileRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch.pid")
	forget, err := WritePID(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if pid, marker, live := livePID(path); pid != os.Getpid() || marker != want || !live {
		t.Fatalf("livePID = %d %q %v, want %d %q true", pid, marker, live, os.Getpid(), want)
	}
	forget()
	absent(t, path)
}
