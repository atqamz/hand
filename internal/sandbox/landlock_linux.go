//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

const minABI = 9

const rulePathBeneath = unix.LANDLOCK_RULE_PATH_BENEATH

type rulesetAttr struct {
	fs     uint64
	net    uint64
	scoped uint64
}

type pathBeneath struct {
	allowed uint64
	parent  int32
}

const fileBits = uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
	unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE |
	unix.LANDLOCK_ACCESS_FS_IOCTL_DEV)

type Support struct {
	Backend     string
	ABI         int
	UnixSockets bool
	Reason      string
}

func Probe() Support {
	abi := ABIVersion()
	s := Support{Backend: "landlock", ABI: abi, UnixSockets: abi >= minABI}
	switch {
	case abi == 0:
		s.Backend = "none"
		s.Reason = "the kernel does not offer Landlock"
	case abi < minABI:
		s.Reason = fmt.Sprintf("Landlock ABI %d cannot deny Unix-socket connects; boxed needs ABI %d", abi, minABI)
	}
	return s
}

func ABIVersion() int {
	r, _, errno := unix.RawSyscall6(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION, 0, 0, 0)
	if errno != 0 {
		return 0
	}
	return int(r)
}

func Apply(p Policy) error {
	if p.Mode == Open {
		return nil
	}
	abi := ABIVersion()
	if abi < minABI {
		return fmt.Errorf("%w: %s", ErrUnsupported, Probe().Reason)
	}
	runtime.LockOSThread()
	attr := rulesetAttr{fs: handledMask(abi), scoped: unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL}
	fd, _, errno := unix.RawSyscall6(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0, 0, 0, 0)
	if errno != 0 {
		return fmt.Errorf("%w: landlock_create_ruleset: %v", ErrUnsupported, errno)
	}
	defer unix.Close(int(fd))
	for _, rule := range p.Rules {
		if err := addRule(int(fd), rule, abi); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("%w: set no_new_privs: %v", ErrUnsupported, err)
	}
	if _, _, errno := unix.RawSyscall6(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("%w: landlock_restrict_self: %v", ErrUnsupported, errno)
	}
	return nil
}

func addRule(ruleset int, rule Rule, abi int) error {
	fi, err := os.Lstat(rule.Path)
	if err != nil {
		return err
	}
	dir := fi.IsDir()
	allowed := ruleMask(rule.Write, dir, abi)
	if allowed == 0 {
		return nil
	}
	fd, err := unix.Open(rule.Path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: rule.Path, Err: err}
	}
	defer unix.Close(fd)
	attr := pathBeneath{allowed: allowed, parent: int32(fd)}
	_, _, errno := unix.RawSyscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), rulePathBeneath, uintptr(unsafe.Pointer(&attr)), 0, 0, 0)
	if errno != 0 {
		return &os.PathError{Op: "landlock_add_rule", Path: rule.Path, Err: errno}
	}
	return nil
}

func handledMask(abi int) uint64 {
	m := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
		unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
		unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		m |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		m |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		m |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	if abi >= 9 {
		m |= unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX
	}
	return m
}

func ruleMask(write, dir bool, abi int) uint64 {
	var m uint64
	if write {
		m = uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
			unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
			unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
			unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
			unix.LANDLOCK_ACCESS_FS_MAKE_REG |
			unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
			unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
			unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
		if abi >= 2 {
			m |= unix.LANDLOCK_ACCESS_FS_REFER
		}
		if abi >= 3 {
			m |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
		}
		if abi >= 5 {
			m |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
		}
		if dir && abi >= 9 {
			m |= unix.LANDLOCK_ACCESS_FS_RESOLVE_UNIX
		}
	} else {
		m = uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE |
			unix.LANDLOCK_ACCESS_FS_READ_FILE |
			unix.LANDLOCK_ACCESS_FS_READ_DIR)
	}
	if !dir {
		m &= fileBits
	}
	return m & handledMask(abi)
}
