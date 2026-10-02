package luvus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/luvus"
)

func TestProcStartMarkerReadsFieldTwentyTwo(t *testing.T) {
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "x) y")
	if err := os.WriteFile(bin, src, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	got, err := luvus.ProcStartMarker(pid)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Fields(strings.Replace(string(raw), "(x) y)", "(c)", 1))[21]; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if _, err := luvus.ProcStartMarker(pid); err == nil {
		t.Fatal("marker for a reaped process")
	}
}
