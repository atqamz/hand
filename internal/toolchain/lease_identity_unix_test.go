//go:build !windows

package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"syscall"
	"testing"
)

func TestLeaseScopeIdentitySurvivesDeviceRenumbering(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "herdr-server:renumbered", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=renumbered"}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	info, err := lease.lock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}

	renumbered := *info.Sys().(*syscall.Stat_t)
	renumbered.Dev ^= 0x3a
	identity := leaseStatIdentity(&renumbered)
	if err := os.WriteFile(lease.LockPath()+".identity", []byte(identity+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["lock_identity"] = identity
	if raw, err = json.Marshal(record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.RecordPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	live, err := store.AcquireLease(request)
	if err != nil {
		t.Fatalf("same lock inode under a renumbered device = %v, want acquired", err)
	}
	t.Cleanup(func() { _ = live.Close() })
	if err := os.Remove(live.LockPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live.LockPath(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	contender, err := store.AcquireLease(request)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("replaced lock inode behind a live holder = %v, want ErrLeaseMetadataUnknown", err)
	}
}
