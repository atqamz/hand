package store

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Serializes the one-way legacy import, whose readdir -> insert -> archive
// spans files sqlite cannot see. Distinct from the project registry lock,
// because project.Add and Remove already hold that one when they import.
const MigrationLock = "migration"

// These locks guard whole command sequences, not database writes: hand merge
// holds one across a network call. sqlite's own locking is per statement and
// cannot express that, so both exist and neither replaces the other.
func Lock(homeDir, name string, nonblock bool) (func(), error) {
	if err := os.MkdirAll(Dir(homeDir), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lockName := fmt.Sprintf(".%x.lock", sha256.Sum256([]byte(name)))
	file, err := os.OpenFile(filepath.Join(Dir(homeDir), lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	flags := syscall.LOCK_EX
	if nonblock {
		flags |= syscall.LOCK_NB
	}
	// Returned unwrapped: a nonblock caller distinguishes a busy lock from a
	// real fault by comparing against syscall.EWOULDBLOCK.
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}
