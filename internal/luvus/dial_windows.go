package luvus

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const pipePrefix = `\\.\pipe\`

var waitNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

func dial(ctx context.Context, addr string) (net.Conn, error) {
	if !strings.HasPrefix(addr, pipePrefix) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", addr)
	}
	return dialPipe(ctx, addr, processUser)
}

func dialPipe(ctx context.Context, name string, owner func(pid uint32) (*windows.SID, error)) (net.Conn, error) {
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		h, err = windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OVERLAPPED, 0)
		if err == nil {
			break
		}
		if err != windows.ERROR_PIPE_BUSY {
			return nil, err
		}
		wait := 250 * time.Millisecond
		if dl, ok := ctx.Deadline(); ok {
			wait = min(wait, time.Until(dl))
		}
		_, _, _ = waitNamedPipe.Call(uintptr(unsafe.Pointer(path)), uintptr(max(wait.Milliseconds(), 1)))
	}
	if err := checkOwner(h, owner); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("%w: %s: %v", ErrForeignOwner, name, err)
	}
	return pipeConn{os.NewFile(uintptr(h), name), name}, nil
}

func checkOwner(pipe windows.Handle, owner func(pid uint32) (*windows.SID, error)) error {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(pipe, &pid); err != nil {
		return err
	}
	server, err := owner(pid)
	if err != nil {
		return fmt.Errorf("process %d: %w", pid, err)
	}
	me, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !server.Equals(me.User.Sid) {
		return fmt.Errorf("process %d runs as %s, not %s", pid, server, me.User.Sid)
	}
	return nil
}

func processUser(pid uint32) (*windows.SID, error) {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(proc)
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

type pipeConn struct {
	*os.File
	name string
}

func (c pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.name) }
func (c pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.name) }

type pipeAddr string

func (pipeAddr) Network() string  { return "pipe" }
func (a pipeAddr) String() string { return string(a) }
