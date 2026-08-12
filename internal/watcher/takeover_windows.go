//go:build windows

package watcher

import (
	"errors"
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

const takeoverEventPrefix = "Global\\hand-watch-takeover-"

type takeoverEndpoint struct {
	takeover  windows.Handle
	stop      windows.Handle
	requested chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func newTakeoverEndpoint(pid int) (*takeoverEndpoint, error) {
	name, err := windows.UTF16PtrFromString(takeoverEventName(pid))
	if err != nil {
		return nil, fmt.Errorf("encode takeover event name: %w", err)
	}
	takeover, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil {
		if takeover != 0 {
			_ = windows.CloseHandle(takeover)
		}
		return nil, fmt.Errorf("create takeover event: %w", err)
	}
	stop, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(takeover)
		return nil, fmt.Errorf("create takeover stop event: %w", err)
	}

	e := &takeoverEndpoint{
		takeover:  takeover,
		stop:      stop,
		requested: make(chan struct{}),
		done:      make(chan struct{}),
	}
	go e.listen()
	return e, nil
}

func (e *takeoverEndpoint) Requested() <-chan struct{} {
	return e.requested
}

func (e *takeoverEndpoint) Close() {
	if e == nil {
		return
	}
	e.closeOnce.Do(func() {
		_ = windows.SetEvent(e.stop)
		<-e.done
		_ = windows.CloseHandle(e.takeover)
		_ = windows.CloseHandle(e.stop)
	})
}

func (e *takeoverEndpoint) listen() {
	defer close(e.done)
	result, err := windows.WaitForMultipleObjects([]windows.Handle{e.takeover, e.stop}, false, windows.INFINITE)
	if err != nil || result == windows.WAIT_FAILED {
		return
	}
	if result == windows.WAIT_OBJECT_0 {
		close(e.requested)
	}
}

func requestTakeover(pid int) error {
	name, err := windows.UTF16PtrFromString(takeoverEventName(pid))
	if err != nil {
		return fmt.Errorf("encode takeover event name: %w", err)
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open takeover event: %w", err)
	}
	defer windows.CloseHandle(event)
	if err := windows.SetEvent(event); err != nil {
		return fmt.Errorf("set takeover event: %w", err)
	}
	return nil
}

func takeoverEventName(pid int) string {
	return fmt.Sprintf("%s%d", takeoverEventPrefix, pid)
}
