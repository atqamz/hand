package toolchain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

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
