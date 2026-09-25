package store

import (
	"fmt"
	"strconv"
	"strings"
	"unsafe"

	"github.com/atqamz/hand/internal/osfacts"
	"golang.org/x/sys/windows"
)

func legacyV18CutoverBootToken() (string, error) {
	return strconv.FormatUint(osfacts.TickCount(), 10), nil
}

func legacyV18CutoverMachineID() (string, error) {
	subkey, err := windows.UTF16PtrFromString(`SOFTWARE\Microsoft\Cryptography`)
	if err != nil {
		return "", err
	}
	var key windows.Handle
	if err := windows.RegOpenKeyEx(windows.HKEY_LOCAL_MACHINE, subkey, 0, windows.KEY_QUERY_VALUE|windows.KEY_WOW64_64KEY, &key); err != nil {
		return "", fmt.Errorf("open Cryptography key: %w", err)
	}
	defer func() { _ = windows.RegCloseKey(key) }()
	name, err := windows.UTF16PtrFromString("MachineGuid")
	if err != nil {
		return "", err
	}
	buf := make([]uint16, 64)
	size := uint32(len(buf) * 2)
	var kind uint32
	if err := windows.RegQueryValueEx(key, name, nil, &kind, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return "", fmt.Errorf("read MachineGuid: %w", err)
	}
	if kind != windows.REG_SZ {
		return "", fmt.Errorf("MachineGuid has registry type %d, want REG_SZ", kind)
	}
	return strings.ToLower(windows.UTF16ToString(buf[:size/2])), nil
}

func classifyLegacyV18CutoverFilesystem(dir string) error {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(path, &root[0], uint32(len(root))); err != nil {
		return fmt.Errorf("volume of %s: %w", dir, err)
	}
	if kind := windows.GetDriveType(&root[0]); kind != windows.DRIVE_FIXED {
		return fmt.Errorf("volume %s has drive type %d, want a fixed local drive", windows.UTF16ToString(root), kind)
	}
	fsName := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(&root[0], nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		return fmt.Errorf("volume information of %s: %w", windows.UTF16ToString(root), err)
	}
	if name := windows.UTF16ToString(fsName); name != "NTFS" {
		return fmt.Errorf("filesystem %q is unclassified", name)
	}
	return nil
}
