//go:build !windows

package watcher

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The deterministic core proof for the publication race: a stale generation-A
// request must never wake the current generation-B endpoint, because each
// generation owns a distinct socket derived from home identity + generation.
func TestUnixStaleGenerationCannotWakeCurrentGeneration(t *testing.T) {
	home := t.TempDir()
	e, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatalf("newTakeoverEndpoint(genB): %v", err)
	}
	defer e.Close()

	if err := requestTakeover(home, genA); err == nil {
		t.Fatal("requestTakeover(genA) reached genB's socket, want a safe failure")
	}
	select {
	case <-e.Requested():
		t.Fatal("a stale generation signaled the current generation's takeover channel")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestUnixTakeoverValidGenerationClosesRequested(t *testing.T) {
	home := t.TempDir()
	e, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatalf("newTakeoverEndpoint: %v", err)
	}
	defer e.Close()

	if err := requestTakeover(home, genB); err != nil {
		t.Fatalf("requestTakeover(genB): %v", err)
	}
	select {
	case <-e.Requested():
	case <-time.After(2 * time.Second):
		t.Fatal("a valid generation request did not close the takeover channel")
	}
}

func TestUnixTakeoverMalformedRequestDoesNotWake(t *testing.T) {
	home := t.TempDir()
	e, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	conn, err := net.Dial("unix", takeoverSocketPath(home, genB))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("not-the-generation\n")); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	select {
	case <-e.Requested():
		t.Fatal("a malformed request closed the takeover channel")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestUnixTakeoverRequiresPositiveAcknowledgment(t *testing.T) {
	home := t.TempDir()
	sock := takeoverSocketPath(home, genB)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(sock)
	}()

	served := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_, _ = conn.Write([]byte("not-ok\n"))
			_ = conn.Close()
		}
		close(served)
	}()

	if err := requestTakeover(home, genB); err == nil {
		t.Fatal("requestTakeover accepted an invalid acknowledgment")
	}
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("test endpoint did not receive the request")
	}
}

func TestRequestCurrentRetriesAfterFailedAcknowledgment(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(OwnerRecordPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishOwnerRecord(home, OwnerRecord{Version: ownerRecordVersion, Generation: genB, PID: os.Getpid() + 1}); err != nil {
		t.Fatal(err)
	}
	sock := takeoverSocketPath(home, genB)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(reply string) <-chan error {
		done := make(chan error, 1)
		go func() {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				done <- acceptErr
				return
			}
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			_, _ = bufio.NewReader(conn).ReadString('\n')
			_, _ = conn.Write([]byte(reply))
			_ = conn.Close()
			done <- nil
		}()
		return done
	}

	requested := make(map[string]bool)
	serverDone := serve("not-ok\n")
	requestCurrent(home, requested)
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	if requested[genB] {
		t.Fatal("failed acknowledgment marked the generation as requested")
	}
	_ = listener.Close()
	_ = os.Remove(sock)

	endpoint, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	requestCurrent(home, requested)
	if !requested[genB] {
		t.Fatal("successful acknowledgment did not mark the generation as requested")
	}
	select {
	case <-endpoint.Requested():
	case <-time.After(time.Second):
		t.Fatal("retry did not reach the generation endpoint")
	}
}

// Closing the endpoint is teardown, never a takeover: the channel must stay
// open and the socket must be removed.
func TestUnixEndpointCloseDoesNotMasqueradeAsTakeover(t *testing.T) {
	home := t.TempDir()
	e, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatal(err)
	}
	sock := takeoverSocketPath(home, genB)
	e.Close()

	select {
	case <-e.Requested():
		t.Fatal("endpoint teardown closed the takeover channel")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("takeover socket %s still present after Close (stat err %v)", sock, err)
	}
	if err := requestTakeover(home, genB); err == nil {
		t.Fatal("requestTakeover to a closed endpoint should fail safely")
	}
}

// A socket that never existed (stale generation, crashed predecessor) fails
// safely, with no fallback to pid-based targeting.
func TestUnixStaleSocketFailsSafely(t *testing.T) {
	home := t.TempDir()
	if err := requestTakeover(home, strings.Repeat("c", 32)); err == nil {
		t.Fatal("dialing a non-existent socket succeeded, want a safe failure")
	}
}

// macOS bounds sockaddr_un sun_path to 104 bytes; os.TempDir() alone is often
// most of that, so the derived filename has to be compact. Rising over the
// limit here is a bind: invalid argument failure on mac CI, so pin it.
func TestUnixSocketPathStaysWithinSockaddrUnLimit(t *testing.T) {
	home := t.TempDir()
	if path := takeoverSocketPath(home, "deadbeef"+strings.Repeat("a", 24)); len(path) >= sockaddrUnLimit {
		t.Fatalf("takeover socket path %q is %d bytes, want < %d (macOS bind limit)", path, len(path), sockaddrUnLimit)
	}
}

func TestUnixSocketPathFallsBackFromAnOverlongTempDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(string(filepath.Separator), strings.Repeat("t", 120)))
	path := takeoverSocketPath(home, genB)
	if len(path) >= sockaddrUnLimit {
		t.Fatalf("takeover socket path %q is %d bytes, want < %d", path, len(path), sockaddrUnLimit)
	}
	if !strings.HasPrefix(path, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
		t.Fatalf("takeover socket path %q did not use the short fallback directory", path)
	}
}

func TestUnixSocketPathCanonicalizesEquivalentHomeAliases(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "fleet")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasRoot := t.TempDir()
	alias := filepath.Join(aliasRoot, "alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}

	for _, equivalent := range []string{filepath.Join(home, "."), alias} {
		if got, want := takeoverSocketPath(equivalent, genB), takeoverSocketPath(home, genB); got != want {
			t.Fatalf("takeoverSocketPath(%q) = %q, want canonical identity %q", equivalent, got, want)
		}
	}
}
