package herdr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/atqamz/hand/internal/toolchain"
)

func TestFleetHerdrEnsurePreservesPreviousHandGeneration(t *testing.T) {
	setFleetHerdrHome(t)
	ctx := context.Background()
	store := managedServerLeaseStore(t)
	if _, err := store.Ensure(ctx, "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), managedExecutableName("hand"))
	if err := os.WriteFile(source, []byte("previous Hand generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	previous, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	const fleetID = "f_0123456789abcdef0123456789abcdef"
	session := SessionName(fleetID)
	runtimeRequest := toolchain.LeaseRequest{
		Generation: generation, LeaseID: "herdr-server:" + session, FleetID: fleetID,
		Consumer: "herdr-server", Evidence: "session=" + session,
	}
	runtimeLease, err := store.AcquireLease(runtimeRequest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeLease.Close() })
	handRequest := toolchain.LeaseRequest{
		Generation: "sha256:" + filepath.Base(filepath.Dir(previous)), LeaseID: "herdr-guardian:" + session,
		FleetID: fleetID, Consumer: "runtime-guardian", Evidence: "session=" + session,
	}
	handLease, err := store.AcquireHandLease(handRequest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handLease.Close() })
	before := map[string][]byte{}
	for _, path := range []string{runtimeLease.RecordPath(), handLease.RecordPath()} {
		before[path], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("current Hand generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	current, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{store: store, guardian: current, runtimeGeneration: generation, fleetID: fleetID, session: session}
	if owned, err := client.serverLeaseOwned(ctx); err != nil || owned {
		t.Fatalf("caller-generation ownership = %t, %v; want false with the previous guardian still leased", owned, err)
	}
	starts := 0
	h := testFleetHerdr(func(context.Context) SessionObservation {
		return SessionObservation{Name: session, State: SessionRunningCompatible}
	}, func(context.Context) error { starts++; return nil }, nil)
	h.ownershipFn = client.serverLeaseOwned
	if err := h.Ensure(ctx); !errors.Is(err, ErrSessionUnowned) {
		t.Fatalf("running previous generation was not refused without restart: %v", err)
	}
	if starts != 0 {
		t.Fatalf("started %d replacement servers", starts)
	}
	if held, err := store.RuntimeLeaseHeld(runtimeRequest); err != nil || !held {
		t.Fatalf("previous runtime lease = %t, %v", held, err)
	}
	if held, err := store.HandLeaseHeld(handRequest); err != nil || !held {
		t.Fatalf("previous Hand lease = %t, %v", held, err)
	}
	for path, want := range before {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("previous ownership metadata changed: %s: %v", path, err)
		}
	}
}
