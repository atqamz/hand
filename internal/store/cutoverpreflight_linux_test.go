package store

import "testing"

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
