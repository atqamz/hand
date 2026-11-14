package cli_test

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/atqamz/hand/internal/luvus"
)

func killGroup(cmd *exec.Cmd) { killTree(cmd.Process.Pid) }

func reapPID(pid int) { killTree(pid) }

func killTree(pid int) {
	ctx, cancel := context.WithTimeout(context.Background(), workerReap)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}

func alive(pid int) bool {
	_, err := luvus.ProcStartMarker(pid)
	return err == nil
}
