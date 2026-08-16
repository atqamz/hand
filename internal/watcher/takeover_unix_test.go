//go:build !windows

package watcher

import (
	"net"
	"os"
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
	const sockaddrUnLimit = 104
	home := t.TempDir()
	if path := takeoverSocketPath(home, "deadbeef"+strings.Repeat("a", 24)); len(path) >= sockaddrUnLimit {
		t.Fatalf("takeover socket path %q is %d bytes, want < %d (macOS bind limit)", path, len(path), sockaddrUnLimit)
	}
}
