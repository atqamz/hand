//go:build windows

package toolchain

import (
	"encoding/hex"
	"os"
	"os/exec"

	"golang.org/x/sys/windows"
)

func startChildWithLease(cmd *exec.Cmd, _ *os.File) error {
	return cmd.Start()
}

func leaseLockIdentity(file *os.File) (string, error) {
	var info [24]byte
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileIdInfo, &info[0], uint32(len(info))); err != nil {
		return "", err
	}
	return "windows-v2:" + hex.EncodeToString(info[:]), nil
}
