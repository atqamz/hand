//go:build windows

package watcher

import (
	"testing"
	"time"
)

func TestTakeoverEventIsDelivered(t *testing.T) {
	home := t.TempDir()
	endpoint, err := newTakeoverEndpoint(home, testGen)
	if err != nil {
		t.Fatalf("newTakeoverEndpoint: %v", err)
	}
	defer endpoint.Close()

	if err := requestTakeover(home, testGen); err != nil {
		t.Fatalf("requestTakeover: %v", err)
	}

	select {
	case <-endpoint.Requested():
	case <-time.After(2 * time.Second):
		t.Fatal("takeover event was not delivered")
	}
}

func TestRequestTakeoverIgnoresMissingEvent(t *testing.T) {
	home := t.TempDir()
	if err := requestTakeover(home, testGen); err != nil {
		t.Fatalf("requestTakeover for a missing generation event: %v", err)
	}
}

// A stale generation's event is a different name entirely, so it cannot wake
// the current generation's handler.
func TestTakeoverStaleGenerationCannotWakeCurrentGeneration(t *testing.T) {
	home := t.TempDir()
	endpoint, err := newTakeoverEndpoint(home, genB)
	if err != nil {
		t.Fatalf("newTakeoverEndpoint(genB): %v", err)
	}
	defer endpoint.Close()

	if err := requestTakeover(home, genA); err != nil {
		t.Fatalf("requesting a missing stale event should be harmless, got %v", err)
	}
	select {
	case <-endpoint.Requested():
		t.Fatal("a stale generation signaled the current generation's takeover channel")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTakeoverEndpointCloseStopsListener(t *testing.T) {
	home := t.TempDir()
	for range 10 {
		gen, err := newGeneration()
		if err != nil {
			t.Fatal(err)
		}
		endpoint, err := newTakeoverEndpoint(home, gen)
		if err != nil {
			t.Fatalf("newTakeoverEndpoint: %v", err)
		}

		closed := make(chan struct{})
		go func() {
			endpoint.Close()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("takeover endpoint Close did not stop its listener")
		}
		endpoint.Close()
	}
}
