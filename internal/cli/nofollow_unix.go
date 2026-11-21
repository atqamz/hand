//go:build linux || darwin || freebsd || netbsd || openbsd

package cli

import "syscall"

const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
