package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/atqamz/hand/internal/osfacts"
	"golang.org/x/sys/unix"
)

// Filesystems that survive the required restart and support hard links; everything else refuses.
var legacyV18CutoverLinuxLocalFilesystems = map[uint32]string{
	0xEF53:     "ext2/ext3/ext4",
	0x58465342: "xfs",
	0x9123683E: "btrfs",
	0x2FC12FC1: "zfs",
	0xF2F52010: "f2fs",
	0xCA451A4E: "bcachefs",
}

var legacyV18CutoverLinuxVolatileFilesystems = map[uint32]string{
	0x01021994: "tmpfs",
	0x858458F6: "ramfs",
}

func legacyV18CutoverBootToken() (string, error) {
	return osfacts.BootID()
}

func legacyV18CutoverMachineID() (string, error) {
	return readLegacyV18CutoverLinuxMachineID("/etc/machine-id", "/proc/self/mountinfo")
}

func readLegacyV18CutoverLinuxMachineID(path, mountinfoPath string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	id, err := parseLegacyV18CutoverLinuxMachineID(data)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(resolved, &stat); err != nil {
		return "", fmt.Errorf("statfs %s: %w", resolved, err)
	}
	if name, volatile := legacyV18CutoverLinuxVolatileFilesystems[uint32(stat.Type)]; volatile {
		return "", fmt.Errorf("%s is on %s and is regenerated at every boot", resolved, name)
	}
	mountinfo, err := os.ReadFile(mountinfoPath)
	if err != nil {
		return "", err
	}
	if legacyV18CutoverMountinfoHasMountPoint(string(mountinfo), resolved) {
		return "", fmt.Errorf("%s is a mount point and is regenerated at every boot", resolved)
	}
	return id, nil
}

func parseLegacyV18CutoverLinuxMachineID(data []byte) (string, error) {
	id := strings.TrimSuffix(string(data), "\n")
	switch {
	case id == "":
		return "", fmt.Errorf("/etc/machine-id is empty")
	case id == "uninitialized":
		return "", fmt.Errorf("/etc/machine-id is uninitialized")
	case !legacyV18CutoverLinuxMachineIDPattern.MatchString(id):
		return "", fmt.Errorf("/etc/machine-id is not 32 lowercase hex digits")
	}
	return id, nil
}

func legacyV18CutoverMountinfoHasMountPoint(mountinfo, path string) bool {
	for _, line := range strings.Split(mountinfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && unescapeLegacyV18CutoverMountinfo(fields[4]) == path {
			return true
		}
	}
	return false
}

func unescapeLegacyV18CutoverMountinfo(field string) string {
	var out strings.Builder
	for i := 0; i < len(field); i++ {
		if field[i] == '\\' && i+3 < len(field) {
			if value, err := strconv.ParseUint(field[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(value))
				i += 3
				continue
			}
		}
		out.WriteByte(field[i])
	}
	return out.String()
}

func classifyLegacyV18CutoverFilesystem(dir string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("statfs: %w", err)
	}
	return classifyLegacyV18CutoverLinuxFilesystemType(uint32(stat.Type))
}

func classifyLegacyV18CutoverLinuxFilesystemType(magic uint32) error {
	if _, ok := legacyV18CutoverLinuxLocalFilesystems[magic]; ok {
		return nil
	}
	if name, volatile := legacyV18CutoverLinuxVolatileFilesystems[magic]; volatile {
		return fmt.Errorf("%s does not survive the required restart", name)
	}
	return fmt.Errorf("filesystem type 0x%X is remote or unclassified", magic)
}
