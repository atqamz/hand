package store

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

func legacyV18CutoverBootToken() (string, error) {
	token, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	return strings.ToLower(token), nil
}

func legacyV18CutoverMachineID() (string, error) {
	var id [16]byte
	wait := unix.Timespec{Sec: 5}
	if _, _, errno := unix.Syscall(unix.SYS_GETHOSTUUID, uintptr(unsafe.Pointer(&id[0])), uintptr(unsafe.Pointer(&wait)), 0); errno != 0 {
		return "", fmt.Errorf("gethostuuid: %w", errno)
	}
	raw := hex.EncodeToString(id[:])
	return raw[0:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:32], nil
}

func classifyLegacyV18CutoverFilesystem(dir string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("statfs: %w", err)
	}
	name := unix.ByteSliceToString(stat.Fstypename[:])
	if stat.Flags&unix.MNT_LOCAL == 0 {
		return fmt.Errorf("%s is not a local filesystem", name)
	}
	if name != "apfs" && name != "hfs" {
		return fmt.Errorf("filesystem type %q is unclassified", name)
	}
	return nil
}
