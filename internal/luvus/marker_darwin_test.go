package luvus_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/luvus"
)

var markerShape = regexp.MustCompile(`^\d+\.\d{6}$`)

func startSleep(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestProcStartMarkerReadsTheStartTime(t *testing.T) {
	own, err := luvus.ProcStartMarker(os.Getpid())
	if err != nil || !markerShape.MatchString(own) {
		t.Fatalf("own marker = %q, %v", own, err)
	}
	if again, err := luvus.ProcStartMarker(os.Getpid()); err != nil || again != own {
		t.Fatalf("second read = %q, %v; first %q", again, err, own)
	}
	first, err := luvus.ProcStartMarker(startSleep(t))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	second, err := luvus.ProcStartMarker(startSleep(t))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := strconv.ParseFloat(first, 64)
	b, _ := strconv.ParseFloat(second, 64)
	if !markerShape.MatchString(first) || !markerShape.MatchString(second) || b <= a {
		t.Fatalf("markers %q then %q", first, second)
	}
}

func TestProcStartMarkerOfAnExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if m, err := luvus.ProcStartMarker(cmd.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("marker of an exited process = %q, %v", m, err)
	}
}

func TestProcStartMarkerRefusesAPidOutsideTheRange(t *testing.T) {
	for _, pid := range []int{0, -1, 1<<32 + os.Getpid()} {
		if m, err := luvus.ProcStartMarker(pid); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("pid %d: marker %q, %v", pid, m, err)
		}
	}
}
