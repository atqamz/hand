package luvus

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var impersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

func clientLevel(pipe windows.Handle) (uint32, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, err := impersonateNamedPipeClient.Call(uintptr(pipe)); r == 0 {
		return 0, err
	}
	defer windows.RevertToSelf()
	thread, err := windows.GetCurrentThread()
	if err != nil {
		return 0, err
	}
	var token windows.Token
	if err := windows.OpenThreadToken(thread, windows.TOKEN_QUERY, true, &token); err != nil {
		return 0, err
	}
	defer token.Close()
	var level, n uint32
	if err := windows.GetTokenInformation(token, windows.TokenImpersonationLevel, (*byte)(unsafe.Pointer(&level)), 4, &n); err != nil {
		return 0, err
	}
	return level, nil
}

func servePipe(t *testing.T) (string, <-chan uint32) {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\hand-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateNamedPipe(path, windows.PIPE_ACCESS_DUPLEX, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	done, levels := make(chan struct{}), make(chan uint32, 1)
	go func() {
		defer close(done)
		if err := windows.ConnectNamedPipe(h, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return
		}
		buf := make([]byte, 64)
		var n uint32
		if windows.ReadFile(h, buf, &n, nil) == nil {
			level, err := clientLevel(h)
			if err != nil {
				level = 0xffffffff
			}
			levels <- level
			_ = windows.WriteFile(h, buf[:n], &n, nil)
		}
	}()
	t.Cleanup(func() {
		_ = windows.DisconnectNamedPipe(h)
		_ = windows.CloseHandle(h)
		<-done
	})
	return name, levels
}

func TestPipeDialEcho(t *testing.T) {
	name, _ := servePipe(t)
	conn, err := dial(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.RemoteAddr().String() != name {
		t.Fatalf("remote = %s", conn.RemoteAddr())
	}
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "hello\n" {
		t.Fatalf("echo = %q, %v", line, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("idle read = %v, want a deadline error", err)
	}
	past, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	start := time.Now()
	if _, err := dial(past, name); err == nil || time.Since(start) > 100*time.Millisecond {
		t.Fatalf("expired dial = %v after %s", err, time.Since(start))
	}
}

func TestPipeOwnerMismatch(t *testing.T) {
	name, _ := servePipe(t)
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	other := func(uint32) (*windows.SID, error) { return system, nil }
	if _, err := dialPipe(context.Background(), name, other); !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("dial as another user = %v, want ErrForeignOwner", err)
	}
}

func TestPipeServerCannotImpersonateTheClient(t *testing.T) {
	name, levels := servePipe(t)
	conn, err := dial(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	if level := <-levels; level != windows.SecurityIdentification {
		t.Fatalf("server saw impersonation level %d, want %d (identification)", level, windows.SecurityIdentification)
	}
}
