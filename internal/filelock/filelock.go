// Package filelock advisory-locks an open file across a whole process, the way
// store, project, and watcher each need for their own on-disk lock files. Unix
// and windows have no shared syscall for this, so the two implementations live
// in build-tagged siblings behind this one portable signature.
package filelock

import "errors"

// ErrBusy is Lock's error when block is false and another process holds the
// lock. It replaces the platform-specific sentinel callers used to compare
// against directly (syscall.EWOULDBLOCK on unix, windows.ERROR_LOCK_VIOLATION).
var ErrBusy = errors.New("file is locked by another process")
