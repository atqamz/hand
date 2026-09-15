package store

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func legacyV18CutoverSourceLinkCount(file *os.File, _ os.FileInfo) (uint64, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return 0, fmt.Errorf("read file identity handle: %w", err)
	}
	return uint64(info.NumberOfLinks), nil
}
