package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// #348 revision 4 "Freeze evidence": an identity that systemd regenerates at every boot is refused.
func TestParseLegacyV18CutoverLinuxMachineID(t *testing.T) {
	for input, wantErr := range map[string]bool{
		"":                                     true,
		"\n":                                   true,
		"uninitialized\n":                      true,
		"0123456789ABCDEF0123456789ABCDEF\n":   true,
		"0123456789abcdef0123456789abcde\n":    true,
		"0123456789abcdef0123456789abcdef\n":   false,
		"0123456789abcdef0123456789abcdef":     false,
		"0123456789abcdef0123456789abcdef\n\n": true,
	} {
		if _, err := parseLegacyV18CutoverLinuxMachineID([]byte(input)); (err != nil) != wantErr {
			t.Errorf("machine-id %q: err = %v, want refusal %v", input, err, wantErr)
		}
	}
}

func TestLegacyV18CutoverMountinfoHasMountPoint(t *testing.T) {
	const mountinfo = "22 1 259:2 / / rw,relatime - ext4 /dev/root rw\n" +
		"40 22 0:25 /machine-id /etc/machine-id ro,relatime - tmpfs tmpfs rw\n" +
		"41 22 0:26 / /mnt/a\\040b rw - ext4 /dev/sdb1 rw\n"
	for path, want := range map[string]bool{"/etc/machine-id": true, "/mnt/a b": true, "/etc": false, "/etc/machine-id.old": false} {
		if got := legacyV18CutoverMountinfoHasMountPoint(mountinfo, path); got != want {
			t.Errorf("mount point %q = %v, want %v", path, got, want)
		}
	}
}

// #348 revision 4 "Kernel scope": only local filesystems that survive the restart are frozen.
func TestClassifyLegacyV18CutoverLinuxFilesystemType(t *testing.T) {
	for magic, wantErr := range map[uint32]bool{
		0xEF53:     false,
		0x9123683E: false,
		0x58465342: false,
		0x01021994: true,
		0x6969:     true,
		0xFF534D42: true,
		0x65735546: true,
		0x4D44:     true,
		0x794C7630: true,
	} {
		if err := classifyLegacyV18CutoverLinuxFilesystemType(magic); (err != nil) != wantErr {
			t.Errorf("filesystem 0x%X: err = %v, want refusal %v", magic, err, wantErr)
		}
	}
}

func TestReadLegacyV18CutoverLinuxMachineIDRefusesRegeneratedIdentity(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef\n"
	persistent := filepath.Join(t.TempDir(), "machine-id")
	mountinfo := filepath.Join(t.TempDir(), "mountinfo")
	for path, body := range map[string]string{persistent: id, mountinfo: "22 1 259:2 / / rw - ext4 /dev/root rw\n"} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(persistent, &stat); err == nil && classifyLegacyV18CutoverLinuxFilesystemType(uint32(stat.Type)) == nil {
		if _, err := readLegacyV18CutoverLinuxMachineID(persistent, mountinfo); err != nil {
			t.Fatalf("persistent machine-id refused: %v", err)
		}
		bound := "40 22 0:25 /machine-id " + persistent + " ro - tmpfs tmpfs rw\n"
		if err := os.WriteFile(mountinfo, []byte(bound), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readLegacyV18CutoverLinuxMachineID(persistent, mountinfo); err == nil || !strings.Contains(err.Error(), "mount point") {
			t.Fatalf("bind-mounted machine-id = %v, want mount-point refusal", err)
		}
	}
	shm, err := os.MkdirTemp("/dev/shm", "machine-id-")
	if err != nil {
		t.Skipf("no /dev/shm: %v", err)
	}
	defer func() { _ = os.RemoveAll(shm) }()
	volatile := filepath.Join(shm, "machine-id")
	if err := os.WriteFile(volatile, []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLegacyV18CutoverLinuxMachineID(volatile, mountinfo); err == nil || !strings.Contains(err.Error(), "tmpfs") {
		t.Fatalf("tmpfs machine-id = %v, want tmpfs refusal", err)
	}
}
