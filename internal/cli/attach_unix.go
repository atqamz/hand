//go:build unix

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

func stdinTerminal() bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), uintptr(getTermios), uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

func replaceProcess(argv, env []string) error {
	return syscall.Exec(argv[0], argv, env)
}
