//go:build darwin

package integration

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

func startChildWithPayloadReference(cmd *exec.Cmd, lock, executable *os.File, rootHandle *os.Root, _, executableRoot, launchRoot string) error {
	var nonce [16]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return fmt.Errorf("create retained integration payload launch identity: %w", err)
	}
	launch := filepath.Join(launchRoot, ".launch-"+hex.EncodeToString(nonce[:]))
	if err := rootHandle.Link(executableRoot, launch); err != nil {
		return fmt.Errorf("link retained integration payload executable: %w", err)
	}
	defer func() { _ = rootHandle.Remove(launch) }()
	linked, err := rootHandle.Open(launch)
	if err != nil {
		return fmt.Errorf("open retained integration payload launch link: %w", err)
	}
	defer func() { _ = linked.Close() }()
	retained, err := executable.Stat()
	if err != nil {
		return fmt.Errorf("inspect retained integration payload executable: %w", err)
	}
	resolved, err := linked.Stat()
	if err != nil {
		return fmt.Errorf("inspect retained integration payload launch link: %w", err)
	}
	if !os.SameFile(retained, resolved) {
		return fmt.Errorf("retained integration payload launch link does not name the verified object")
	}
	path, err := retainedExecutablePath(linked)
	if err != nil {
		return err
	}
	cmd.ExtraFiles = append(cmd.ExtraFiles, executable, lock)
	cmd.Path = path
	return cmd.Start()
}

func retainedExecutablePath(executable *os.File) (string, error) {
	var path [unix.PathMax]byte
	_, err := unix.FcntlInt(executable.Fd(), unix.F_GETPATH, int(uintptr(unsafe.Pointer(&path[0]))))
	runtime.KeepAlive(executable)
	if err != nil {
		return "", fmt.Errorf("resolve retained integration payload executable: %w", err)
	}
	end := bytes.IndexByte(path[:], 0)
	if end <= 0 {
		return "", fmt.Errorf("resolve retained integration payload executable: empty path")
	}
	return string(path[:end]), nil
}
