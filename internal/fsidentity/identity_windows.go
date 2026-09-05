//go:build windows

package fsidentity

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func rawIdentity(path string, expected os.FileInfo) (string, error) {
	if expected == nil {
		return "", fmt.Errorf("directory identity metadata is absent")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open directory identity handle: %w", err)
	}
	defer func() { _ = file.Close() }()
	current, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat directory identity handle: %w", err)
	}
	if !os.SameFile(expected, current) {
		return "", fmt.Errorf("directory identity changed before handle capture")
	}
	var handleInfo windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &handleInfo); err != nil {
		return "", fmt.Errorf("read directory identity handle: %w", err)
	}
	return fmt.Sprintf("windows-v1:volume=%08x:file=%08x%08x", handleInfo.VolumeSerialNumber, handleInfo.FileIndexHigh, handleInfo.FileIndexLow), nil
}
