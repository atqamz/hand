package cli_test

import (
	"os/exec"
	"strconv"

	"github.com/atqamz/hand/internal/luvus"
)

func killGroup(cmd *exec.Cmd) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func alive(pid int) bool {
	_, err := luvus.ProcStartMarker(pid)
	return err == nil
}
