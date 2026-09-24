package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/atqamz/hand/internal/filelock"
)

func TestLiveLeaseLockReplacementFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("open Windows lock files cannot be unlinked by this fixture")
	}
	for _, kind := range []string{"runtime", "hand"} {
		for _, scoped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/scoped=%t", kind, scoped), func(t *testing.T) {
				store, _ := generationStoreFixture(t)
				acquire, held := store.AcquireLease, store.RuntimeLeaseHeld
				request := LeaseRequest{LeaseID: "live-holder", FleetID: testFleetID, Consumer: "supervision-waiter", Evidence: "session=live-holder"}
				if scoped {
					request.LockScope = "supervision-waiter:live-holder:0"
				}
				if kind == "hand" {
					source := filepath.Join(t.TempDir(), executableName("hand"))
					if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
						t.Fatal(err)
					}
					managed, err := store.MaterializeHandExecutable(source)
					if err != nil {
						t.Fatal(err)
					}
					request.Generation = "sha256:" + filepath.Base(filepath.Dir(managed))
					acquire, held = store.AcquireHandLease, store.HandLeaseHeld
				} else {
					if _, err := store.Ensure(context.Background(), "", ""); err != nil {
						t.Fatal(err)
					}
					generation, err := store.GenerationID("", "")
					if err != nil {
						t.Fatal(err)
					}
					request.Generation = generation
				}

				lease, err := acquire(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = lease.Close() })
				original, err := lease.lock.Stat()
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(lease.LockPath())
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(lease.LockPath()); err != nil {
					t.Fatal(err)
				}
				if scoped {
					successor := request
					successor.LeaseID = "next-holder"
					if nextHeld, err := held(successor); nextHeld || !errors.Is(err, ErrLeaseMetadataUnknown) {
						t.Fatalf("unrecorded successor behind missing lock = %t, %v, want unknown", nextHeld, err)
					}
				}
				if err := os.WriteFile(lease.LockPath(), data, 0o600); err != nil {
					t.Fatal(err)
				}
				replacement, err := os.Stat(lease.LockPath())
				if err != nil || os.SameFile(original, replacement) {
					t.Fatalf("replacement lock file identity did not change: %v", err)
				}

				probe := exec.Command(os.Args[0], "-test.run=^TestLeaseReplacementKernelProbeProcess$")
				probe.Env = append(os.Environ(), "HAND_LEASE_REPLACEMENT_PROBE="+lease.LockPath())
				if output, err := probe.CombinedOutput(); err != nil {
					t.Fatalf("separate process could not lock replacement inode: %v: %s", err, output)
				}

				live, heldErr := held(request)
				second, acquireErr := acquire(request)
				if second != nil {
					_ = second.Close()
				}
				if live || !errors.Is(heldErr, ErrLeaseMetadataUnknown) || !errors.Is(acquireErr, ErrLeaseMetadataUnknown) {
					t.Fatalf("live holder behind replaced lock: held=%t/%v acquire=%v; want unknown/unknown", live, heldErr, acquireErr)
				}
				if scoped {
					successor := request
					successor.LeaseID = "next-holder"
					if nextHeld, err := held(successor); nextHeld || !errors.Is(err, ErrLeaseMetadataUnknown) {
						t.Fatalf("unrecorded scoped successor behind replaced lock = %t, %v, want unknown", nextHeld, err)
					}
					next, err := acquire(successor)
					if next != nil {
						_ = next.Close()
					}
					if !errors.Is(err, ErrLeaseMetadataUnknown) {
						t.Fatalf("different holder reused replaced scoped lock: %v, want ErrLeaseMetadataUnknown", err)
					}
				}
				if err := lease.Close(); !errors.Is(err, ErrLeaseMetadataUnknown) {
					t.Fatalf("close after lock replacement = %v, want ErrLeaseMetadataUnknown", err)
				}
			})
		}
	}
}

func TestLeaseReplacementKernelProbeProcess(t *testing.T) {
	path := os.Getenv("HAND_LEASE_REPLACEMENT_PROBE")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := filelock.Lock(file, false); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = filelock.Unlock(file) }()
}

func TestLegacyLeaseRecordRequiresPositiveLivenessReconciliation(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "legacy-holder", LockScope: "legacy-slot", FleetID: testFleetID,
		Consumer: "supervision-waiter", Evidence: "session=legacy-holder"}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
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
	record["schema"] = "hand.runtime.lease.v1"
	delete(record, "lock_identity")
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.RecordPath(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(lease.LockPath() + ".identity"); err != nil {
		t.Fatal(err)
	}
	contender, err := store.AcquireLease(request)
	if contender != nil {
		_ = contender.Close()
	}
	if !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("legacy metadata reacquisition = %v, want ErrLeaseMetadataUnknown", err)
	}
	if held, err := store.RuntimeLeaseHeld(request); held || !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("legacy metadata liveness = %t, %v, want unknown", held, err)
	}
	successor := request
	successor.LeaseID = "next-holder"
	next, err := store.AcquireLease(successor)
	if next != nil {
		_ = next.Close()
	}
	if !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("legacy scope successor = %v, want ErrLeaseMetadataUnknown", err)
	}
	unrelated := request
	unrelated.LeaseID = "new-holder"
	unrelated.LockScope = ""
	newLease, err := store.AcquireLease(unrelated)
	if err != nil {
		t.Fatalf("unrelated scope blocked by legacy metadata: %v", err)
	}
	if err := newLease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveLeaseMissingScopeIdentityFailsClosed(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "first-holder", LockScope: "shared-slot", FleetID: testFleetID,
		Consumer: "supervision-waiter", Evidence: "session=first-holder"}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if err := os.Remove(lease.LockPath() + ".identity"); err != nil {
		t.Fatal(err)
	}
	if held, err := store.RuntimeLeaseHeld(request); held || !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("live lease without scope identity = %t, %v, want unknown", held, err)
	}
	successor := request
	successor.LeaseID = "next-holder"
	next, err := store.AcquireLease(successor)
	if next != nil {
		_ = next.Close()
	}
	if !errors.Is(err, ErrLeaseMetadataUnknown) && !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("missing scope identity allowed successor: %v", err)
	}
	if err := lease.Close(); !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("close without scope identity = %v, want unknown", err)
	}
}

func TestLeaseHeldDoesNotAttributeSuccessorLockToAbandonedHolder(t *testing.T) {
	for _, kind := range []string{"runtime", "hand"} {
		for _, scoped := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/scoped=%t", kind, scoped), func(t *testing.T) {
				store, _ := generationStoreFixture(t)
				acquire, held := store.AcquireLease, store.RuntimeLeaseHeld
				request := LeaseRequest{
					LeaseID: "waiter-old", FleetID: testFleetID,
					Consumer: "supervision-waiter", Evidence: "session=scope-reuse",
				}
				if scoped {
					request.LockScope = "supervision-waiter:scope-reuse:0"
				}
				if kind == "hand" {
					source := filepath.Join(t.TempDir(), executableName("hand"))
					if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
						t.Fatal(err)
					}
					managed, err := store.MaterializeHandExecutable(source)
					if err != nil {
						t.Fatal(err)
					}
					request.Generation = "sha256:" + filepath.Base(filepath.Dir(managed))
					acquire, held = store.AcquireHandLease, store.HandLeaseHeld
				} else {
					if _, err := store.Ensure(context.Background(), "", ""); err != nil {
						t.Fatal(err)
					}
					generation, err := store.GenerationID("", "")
					if err != nil {
						t.Fatal(err)
					}
					request.Generation = generation
				}

				first, err := acquire(request)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = first.Close() })
				firstRecord, err := os.ReadFile(first.RecordPath())
				if err != nil {
					t.Fatal(err)
				}
				if err := abandonLeaseForTest(first); err != nil {
					t.Fatal(err)
				}
				if live, err := held(request); err != nil || live {
					t.Fatalf("abandoned holder with unlocked slot = %t, %v; want false, nil", live, err)
				}

				next := request
				next.LeaseID = "waiter-new"
				second, err := acquire(next)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = second.Close() })
				secondRecord, err := os.ReadFile(second.RecordPath())
				if err != nil {
					t.Fatal(err)
				}
				if first.RecordPath() == second.RecordPath() || (first.LockPath() == second.LockPath()) != scoped {
					t.Fatal("fixture did not separate holder records and reuse only the scoped lock")
				}
				var wantErr error
				if scoped {
					wantErr = ErrLeaseMetadataUnknown
				}
				if live, err := held(request); live || !errors.Is(err, wantErr) {
					t.Errorf("abandoned holder borrowed successor liveness: live=%t err=%v scoped=%t", live, err, scoped)
				}
				wantLive := !scoped
				if live, err := held(next); live != wantLive || !errors.Is(err, wantErr) {
					t.Errorf("successor observation = %t, %v; scoped=%t", live, err, scoped)
				}
				if contender, err := acquire(next); !errors.Is(err, ErrLeaseHeld) {
					if contender != nil {
						_ = contender.Close()
					}
					t.Fatalf("ownership probe released successor lock: %v", err)
				}
				for path, want := range map[string][]byte{
					first.RecordPath(): firstRecord, second.RecordPath(): secondRecord,
				} {
					if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
						t.Fatalf("ownership observation changed durable evidence at %s: %v", path, err)
					}
				}
				if err := second.Close(); err != nil {
					t.Fatal(err)
				}
				if live, err := held(request); err != nil || live {
					t.Fatalf("abandoned holder after successor release = %t, %v; want false, nil", live, err)
				}
				if live, err := held(next); err != nil || live {
					t.Fatalf("retired successor = %t, %v; want false, nil", live, err)
				}
			})
		}
	}
}
