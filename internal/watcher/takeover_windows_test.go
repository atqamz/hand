//go:build windows

package watcher

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRequestTakeoverReportsMissingEventWithoutTreatingItAsDelivered(t *testing.T) {
	home := t.TempDir()
	if err := requestTakeover(home, testGen); !errors.Is(err, errTakeoverEndpointMissing) {
		t.Fatalf("requestTakeover for a missing generation event = %v, want errTakeoverEndpointMissing", err)
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

	if err := requestTakeover(home, genA); !errors.Is(err, errTakeoverEndpointMissing) {
		t.Fatalf("requesting a missing stale event = %v, want errTakeoverEndpointMissing", err)
	}
	select {
	case <-endpoint.Requested():
		t.Fatal("a stale generation signaled the current generation's takeover channel")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestTakeoverEventNameCanonicalizesEquivalentHomePaths(t *testing.T) {
	home := t.TempDir()
	for _, equivalent := range []string{filepath.Join(home, "."), filepath.Clean(home), strings.ToUpper(home)} {
		if got, want := takeoverEventName(equivalent, testGen), takeoverEventName(home, testGen); got != want {
			t.Fatalf("takeoverEventName(%q) = %q, want canonical identity %q", equivalent, got, want)
		}
	}
}

func TestRequestCurrentRetriesAfterMissingEvent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(OwnerRecordPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishOwnerRecord(home, OwnerRecord{Version: ownerRecordVersion, Generation: testGen, PID: os.Getpid() + 1}); err != nil {
		t.Fatal(err)
	}
	requested := make(map[string]bool)
	requestCurrent(home, requested)
	if requested[testGen] {
		t.Fatal("missing event marked the generation as requested")
	}

	endpoint, err := newTakeoverEndpoint(home, testGen)
	if err != nil {
		t.Fatal(err)
	}
	defer endpoint.Close()
	requestCurrent(home, requested)
	if !requested[testGen] {
		t.Fatal("successful event delivery did not mark the generation as requested")
	}
	select {
	case <-endpoint.Requested():
	case <-time.After(time.Second):
		t.Fatal("retry did not reach the generation event")
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
