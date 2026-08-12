//go:build !windows

package watcher

import (
	"errors"
	"syscall"
)

type takeoverEndpoint struct{}

func newTakeoverEndpoint(_ int) (*takeoverEndpoint, error) {
	return &takeoverEndpoint{}, nil
}

func (e *takeoverEndpoint) Requested() <-chan struct{} {
	return nil
}

func (e *takeoverEndpoint) Close() {}

func requestTakeover(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
