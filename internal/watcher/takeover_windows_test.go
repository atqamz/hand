//go:build windows

package watcher

import (
	"os"
	"testing"
	"time"
)

func TestTakeoverEventIsDelivered(t *testing.T) {
	endpoint, err := newTakeoverEndpoint(os.Getpid())
	if err != nil {
		t.Fatalf("newTakeoverEndpoint: %v", err)
	}
	defer endpoint.Close()

	if err := requestTakeover(os.Getpid()); err != nil {
		t.Fatalf("requestTakeover: %v", err)
	}

	select {
	case <-endpoint.Requested():
	case <-time.After(2 * time.Second):
		t.Fatal("takeover event was not delivered")
	}
}

func TestRequestTakeoverIgnoresMissingEvent(t *testing.T) {
	if err := requestTakeover(deadPid(t)); err != nil {
		t.Fatalf("requestTakeover for a missing event: %v", err)
	}
}

func TestTakeoverEndpointCloseStopsListener(t *testing.T) {
	for range 10 {
		endpoint, err := newTakeoverEndpoint(os.Getpid())
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
