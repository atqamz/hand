//go:build windows

package toolchain

import (
	"fmt"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func startChildWithLease(cmd *exec.Cmd, _ *os.File) error {
	return cmd.Start()
}

func leaseLockIdentity(file *os.File) (string, error) {
	var info struct {
		VolumeSerialNumber uint64
		FileId             [16]byte
	}
	if err := windows.GetFileInformationByHandleEx(windows.Handle(file.Fd()), windows.FileIdInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		return "", fmt.Errorf("query FileIdInfo; the runtime store volume must provide stable file IDs (NTFS or ReFS): %w", err)
	}
	return fmt.Sprintf("windows-v2:vol=%016x:id=%x", info.VolumeSerialNumber, info.FileId), nil
}
