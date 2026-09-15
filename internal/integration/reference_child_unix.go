//go:build !windows

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

func startChildWithPayloadReference(cmd *exec.Cmd, lock, executable *os.File, _ string) error {
	executableFD := 3 + len(cmd.ExtraFiles)
	cmd.ExtraFiles = append(cmd.ExtraFiles, executable, lock)
	directory := "/dev/fd"
	if runtime.GOOS == "linux" {
		directory = "/proc/self/fd"
	}
	cmd.Path = fmt.Sprintf("%s/%d", directory, executableFD)
	return cmd.Start()
}
