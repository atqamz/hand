package luvus

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func servePipe(t *testing.T) string {
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := windows.ConnectNamedPipe(h, nil); err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			return
		}
		buf := make([]byte, 64)
		var n uint32
		if windows.ReadFile(h, buf, &n, nil) == nil {
			_ = windows.WriteFile(h, buf[:n], &n, nil)
		}
	}()
	t.Cleanup(func() {
		_ = windows.DisconnectNamedPipe(h)
		_ = windows.CloseHandle(h)
		<-done
	})
	return name
}

func TestPipeDialEcho(t *testing.T) {
	name := servePipe(t)
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
	name := servePipe(t)
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	other := func(uint32) (*windows.SID, error) { return system, nil }
	if _, err := dialPipe(context.Background(), name, other); !errors.Is(err, ErrForeignOwner) {
		t.Fatalf("dial as another user = %v, want ErrForeignOwner", err)
	}
}
