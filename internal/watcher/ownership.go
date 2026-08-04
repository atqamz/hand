package watcher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/atqamz/secondhand/internal/state"
)

// ErrAttached is wrapped into Acquire's refusal so cmd can render it as a
// precondition failure rather than a general error.
var ErrAttached = errors.New("a watcher is already attached to this fleet home")

// How long Acquire waits for a signaled incumbent to release before reporting
// that it did not, and how often it re-tries the lock in the meantime.
const (
	takeoverGrace = 5 * time.Second
	takeoverPoll  = 50 * time.Millisecond
)

// OwnerPath names the file whose flock is the fleet home's watch ownership.
func OwnerPath(homeDir string) string {
	return filepath.Join(state.Dir(homeDir), "watch.pid")
}

// Acquire makes hand watch a singleton per fleet home, returning the release to defer. Ownership is
// the flock on state/watch.pid, never the pid the file holds: the kernel drops an flock when the
// holder dies, so nothing stale is left to clear and no liveness heuristic locks a home out.
func Acquire(homeDir string, takeover bool) (func(), error) {
	path := OwnerPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	if err := lockOwner(file); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		if err := contend(file, takeover); err != nil {
			_ = file.Close()
			return nil, err
		}
	}

	// The pid is recorded inside the lock only so a refusal can name the incumbent and takeover can
	// signal it.
	if err := recordOwner(file); err != nil {
		releaseOwner(file)
		return nil, err
	}
	return func() { releaseOwner(file) }, nil
}

// Decides what a lock another live watcher holds means. The pid is read after the lock attempt
// failed, so it belongs to a process that still held the lock a moment ago rather than to some
// long-dead predecessor.
func contend(file *os.File, takeover bool) error {
	pid := readOwner(file)
	if !takeover {
		return fmt.Errorf("%w (pid %s) - stop it, or re-run with --takeover to replace it", ErrAttached, ownerLabel(pid))
	}
	// Never this process: in production a watcher acquires once, but an
	// in-process caller that acquired already would otherwise signal itself.
	if pid > 0 && pid != os.Getpid() {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("signal watcher pid %d: %w", pid, err)
		}
	}

	deadline := time.Now().Add(takeoverGrace)
	for {
		err := lockOwner(file)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock %s: %w", file.Name(), err)
		}
		if time.Now().After(deadline) {
			// Deliberately not claiming SIGTERM was delivered: the pid may have been
			// unreadable, in which case there was nothing to signal and the honest
			// remedy is the same either way.
			return fmt.Errorf("%w (pid %s) and it still holds %s %s after --takeover - kill it and retry",
				ErrAttached, ownerLabel(pid), file.Name(), takeoverGrace)
		}
		time.Sleep(takeoverPoll)
	}
}

func lockOwner(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// Clears the pid before dropping the lock, so an operator reading state/watch.pid on an unwatched
// home finds nothing rather than the number of a process that has exited.
func releaseOwner(file *os.File) {
	_ = file.Truncate(0)
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func recordOwner(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("clear %s: %w", file.Name(), err)
	}
	if _, err := file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return fmt.Errorf("record watcher pid in %s: %w", file.Name(), err)
	}
	return nil
}

// Reports the recorded pid, or 0 for anything it cannot read as one - a wrong pid here would be
// handed to Kill.
func readOwner(file *os.File) int {
	buf := make([]byte, 32)
	n, _ := file.ReadAt(buf, 0)
	line, _, terminated := strings.Cut(string(buf[:n]), "\n")
	// The terminating newline is required: the incumbent truncates before it writes, so a read racing
	// that write sees an empty or partial value, and only a terminated line proves the whole pid
	// reached disk.
	if !terminated {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func ownerLabel(pid int) string {
	if pid <= 0 {
		return "unknown"
	}
	return strconv.Itoa(pid)
}
