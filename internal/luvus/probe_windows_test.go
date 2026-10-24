package luvus_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLiveProbeCmdExit(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "codex.cmd")
	if err := os.WriteFile(shim, []byte("@echo got %1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{100, 8000, 8100, 8150, 8180, 8190, 8191, 8192, 8300, 16384} {
		arg := fill("Lorem ipsum dolor sit amet. ", n)
		cmd := exec.Command(shim)
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + shim + `" "` + arg + `"`}
		out, err := cmd.CombinedOutput()
		text := string(out)
		if len(text) > 160 {
			text = text[:60] + " ... " + text[len(text)-80:]
		}
		t.Logf("PROBE cmd exit arg %d line %d: err %v out %q", n, len(cmd.SysProcAttr.CmdLine), err, strings.TrimSpace(text))
	}
}
