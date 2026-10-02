package cli_test

import (
	"os/exec"

	"github.com/atqamz/hand/internal/luvus"
)

func killGroup(cmd *exec.Cmd, _ bool) {
	_ = cmd.Process.Kill()
}

func alive(pid int) bool {
	_, err := luvus.ProcStartMarker(pid)
	return err == nil
}
