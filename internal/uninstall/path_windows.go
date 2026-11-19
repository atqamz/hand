//go:build windows

package uninstall

import (
	"errors"
	"io/fs"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var envKey = "Environment"

var sendMessage = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

func broadcast() {
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	var result uintptr
	_, _, _ = sendMessage.Call(0xffff, 0x1a, 0, uintptr(unsafe.Pointer(env)), 2, 5000, uintptr(unsafe.Pointer(&result)))
}

func userPath(dir string, apply bool) (bool, error) {
	access := uint32(registry.QUERY_VALUE)
	if apply {
		access |= registry.SET_VALUE
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, envKey, access)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	old, kind, err := k.GetStringValue("Path")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	kept, found := dropPath(old, dir, func(s string) string {
		if out, err := registry.ExpandString(s); err == nil {
			return out
		}
		return s
	})
	if !found || !apply {
		return found, nil
	}
	switch {
	case kept == "":
		err = k.DeleteValue("Path")
	case kind == registry.SZ:
		err = k.SetStringValue("Path", kept)
	default:
		err = k.SetExpandStringValue("Path", kept)
	}
	if err != nil {
		return true, err
	}
	broadcast()
	return true, nil
}
