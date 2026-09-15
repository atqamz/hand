//go:build !windows

package integration

import (
	"os"
	"os/exec"
)

func startChildWithPayloadReference(cmd *exec.Cmd, lock *os.File) error {
	cmd.ExtraFiles = append(cmd.ExtraFiles, lock)
	return cmd.Start()
}
