package toolchain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const testFleetID = "f_0123456789abcdef0123456789abcdef"

func TestManagedHandLeasePublishesExactIdentityAndHoldsKernelLock(t *testing.T) {
	store, _ := generationStoreFixture(t)
	source := filepath.Join(t.TempDir(), executableName("hand"))
	if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	managed, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	generation := "sha256:" + filepath.Base(filepath.Dir(managed))
	request := LeaseRequest{
		Generation: generation,
		LeaseID:    "waiter:session-1",
		FleetID:    testFleetID,
		Consumer:   "supervision-waiter",
		Evidence:   "host=codex;session=session-1",
	}
	lease, err := store.AcquireHandLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(lease.RecordPath()), "/runtime/hand-references/"+strings.TrimPrefix(generation, "sha256:")+"/") {
		t.Fatalf("Hand lease record path = %q, want exact Hand generation namespace", lease.RecordPath())
	}
	if _, err := store.AcquireHandLease(request); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second Hand lease acquisition = %v, want ErrLeaseHeld", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeasePublishesExactIdentityAndHoldsKernelLock(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{
		Generation: generation,
		LeaseID:    "herdr:" + testFleetID,
		FleetID:    testFleetID,
		Consumer:   "herdr-server",
		Evidence:   "session=hand-" + testFleetID,
	}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })

	raw, err := os.ReadFile(lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"schema": LeaseSchema, "generation": generation, "lease_id": request.LeaseID,
		"fleet_id": testFleetID, "consumer": request.Consumer, "evidence": request.Evidence,
	} {
		if got, _ := record[key].(string); got != want {
			t.Fatalf("record[%q] = %q, want %q", key, got, want)
		}
	}
	for _, forbidden := range []string{"pid", "cwd", "executable", "process_path"} {
		if _, ok := record[forbidden]; ok {
			t.Fatalf("lease record uses diagnostic-only %q as ownership evidence", forbidden)
		}
	}

	if _, err := store.AcquireLease(request); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second acquisition = %v, want ErrLeaseHeld", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.RecordPath()); err != nil {
		t.Fatalf("graceful release removed durable record: %v", err)
	}
	if _, err := os.Stat(lease.LockPath()); err != nil {
		t.Fatalf("permanent lock rendezvous was removed: %v", err)
	}
}

func TestLeaseHeldRequiresMatchingRecordAndLiveKernelLock(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{
		Generation: generation, LeaseID: "herdr-server:hand-f_test", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=hand-f_test",
	}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := store.RuntimeLeaseHeld(request); err != nil || !held {
		t.Fatalf("live runtime lease held = %t, %v; want true", held, err)
	}
	if err := abandonLeaseForTest(lease); err != nil {
		t.Fatal(err)
	}
	if held, err := store.RuntimeLeaseHeld(request); err != nil || held {
		t.Fatalf("stale runtime lease held = %t, %v; want false", held, err)
	}
}

func TestRuntimeLeaseHeldTreatsMissingMetadataWithLiveLockAsUnknown(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{
		Generation: generation, LeaseID: "herdr-server:hand-f_test", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=hand-f_test",
	}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if err := os.Remove(lease.RecordPath()); err != nil {
		t.Fatal(err)
	}

	if held, err := store.RuntimeLeaseHeld(request); held || !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("live runtime lock without metadata = %t, %v; want false, ErrLeaseMetadataUnknown", held, err)
	}
}

func TestHandLeaseHeldRequiresMatchingRecordAndLiveKernelLock(t *testing.T) {
	store, _ := generationStoreFixture(t)
	source := filepath.Join(t.TempDir(), executableName("hand"))
	if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	managed, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{
		Generation: "sha256:" + filepath.Base(filepath.Dir(managed)), LeaseID: "herdr-guardian:hand-f_test",
		FleetID: testFleetID, Consumer: "runtime-guardian", Evidence: "session=hand-f_test",
	}
	lease, err := store.AcquireHandLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := store.HandLeaseHeld(request); err != nil || !held {
		t.Fatalf("live Hand lease held = %t, %v; want true", held, err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if held, err := store.HandLeaseHeld(request); err != nil || held {
		t.Fatalf("retired Hand lease held = %t, %v; want false", held, err)
	}
}

func TestRuntimeLeaseReusesStableLockScopeAcrossUniqueHolders(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{
		Generation: generation,
		LeaseID:    "waiter-one",
		LockScope:  "waiter:fleet:host:session:generation:slot-0",
		FleetID:    testFleetID,
		Consumer:   "supervision-waiter",
		Evidence:   "host=codex;session=session-1",
	}
	first, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(first.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	var record leaseRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.LockScope != request.LockScope {
		t.Fatalf("durable lock scope = %q, %v; want %q", record.LockScope, err, request.LockScope)
	}
	secondRequest := request
	secondRequest.LeaseID = "waiter-two"
	if _, err := store.AcquireLease(secondRequest); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("second holder in stable lock scope = %v, want ErrLeaseHeld", err)
	}
	firstRecord, firstLock := first.RecordPath(), first.LockPath()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := store.AcquireLease(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if second.RecordPath() == firstRecord || second.LockPath() != firstLock {
		t.Fatalf("record/lock paths = %q/%q then %q/%q; want unique records sharing one stable lock", firstRecord, firstLock, second.RecordPath(), second.LockPath())
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(firstLock))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("stable lease scope left %v, want two durable records, one rendezvous, and its identity", entries)
	}
	for _, path := range []string{firstRecord, second.RecordPath(), firstLock, firstLock + ".identity"} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stable lease scope lost %s: %v", path, err)
		}
	}
}

func TestRuntimeLeaseCloseRetainsRecordAtAcquisitionRoot(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireLease(LeaseRequest{
		Generation: generation, LeaseID: "herdr:root-replacement", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=root-replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(store.Root)
	moved := filepath.Join(parent, "moved-secondhand")
	if err := os.Rename(store.Root, moved); err != nil {
		if runtime.GOOS == "windows" {
			if closeErr := lease.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return
		}
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lease.RecordPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.RecordPath(), record, 0o600); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(store.Root, lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(moved, relative)); err != nil || !bytes.Equal(got, record) {
		t.Fatalf("acquired-root lease record changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(lease.RecordPath()); err != nil || !bytes.Equal(got, record) {
		t.Fatalf("replacement-root decoy changed: %q, %v", got, err)
	}
}

func TestRuntimeLeaseCloseRetainsReplacedRecord(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireLease(LeaseRequest{
		Generation: generation, LeaseID: "replaced-record", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=replaced-record",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lease.RecordPath(), lease.RecordPath()+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lease.RecordPath(), record, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("close replaced record = %v, want ErrLeaseMetadataUnknown", err)
	}
	if got, err := os.ReadFile(lease.RecordPath()); err != nil || !bytes.Equal(got, record) {
		t.Fatalf("replacement record changed: %q, %v", got, err)
	}
}

func TestLeaseAcquisitionRemainsAnchoredToValidatedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows denies replacement of an open rooted directory")
	}
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	rootHandle, err := openDirectRuntimeRoot(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Lock.Target("", "")
	if err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	if _, _, err := store.generationAt(rootHandle, filepath.Join("bundles", generation), currentTargetName("", ""), target); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	moved := store.Root + "-moved"
	if err := os.Rename(store.Root, moved); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Root, "runtime"), 0o700); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	lease, err := store.acquireLeaseAt(rootHandle, LeaseRequest{
		Generation: generation, LeaseID: "anchored-acquire", FleetID: testFleetID,
		Consumer: "herdr-server", Evidence: "session=anchored",
	}, filepath.Join(store.Root, "runtime", "references", generation))
	if err != nil {
		_ = rootHandle.Close()
		t.Fatalf("lease acquisition followed replacement root: %v", err)
	}
	relative, err := filepath.Rel(store.Root, lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, relative)); err != nil {
		t.Fatalf("lease record missing from validated root: %v", err)
	}
	if _, err := os.Stat(lease.RecordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lease record written through replacement root: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHandLeaseAcquisitionRemainsAnchoredToValidatedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows denies replacement of an open rooted directory")
	}
	store, _ := generationStoreFixture(t)
	source := filepath.Join(t.TempDir(), executableName("hand"))
	if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	managed, err := store.MaterializeHandExecutable(source)
	if err != nil {
		t.Fatal(err)
	}
	generation := "sha256:" + filepath.Base(filepath.Dir(managed))
	rootHandle, err := openDirectRuntimeRoot(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.handGenerationAt(rootHandle, generation); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	moved := store.Root + "-moved"
	if err := os.Rename(store.Root, moved); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(store.Root, "runtime"), 0o700); err != nil {
		_ = rootHandle.Close()
		t.Fatal(err)
	}
	lease, err := store.acquireLeaseAt(rootHandle, LeaseRequest{
		Generation: generation, LeaseID: "anchored-hand-acquire", FleetID: testFleetID,
		Consumer: "runtime-guardian", Evidence: "session=anchored",
	}, filepath.Join(store.Root, "runtime", "hand-references", strings.TrimPrefix(generation, "sha256:")))
	if err != nil {
		_ = rootHandle.Close()
		t.Fatalf("Hand lease acquisition followed replacement root: %v", err)
	}
	relative, err := filepath.Rel(store.Root, lease.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, relative)); err != nil {
		t.Fatalf("Hand lease record missing from validated root: %v", err)
	}
	if _, err := os.Stat(lease.RecordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Hand lease record written through replacement root: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationResolvesExactBundleWithoutSelection(t *testing.T) {
	store, _ := generationStoreFixture(t)
	runtime, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Generation(generation, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.BundleDir != runtime.BundleDir {
		t.Fatalf("resolved bundle = %q, want %q", resolved.BundleDir, runtime.BundleDir)
	}
	if _, err := store.Generation("foreign-generation", "", ""); err == nil {
		t.Fatal("foreign generation resolved")
	}
}

func TestTwoFleetsHoldOneExactGenerationConcurrently(t *testing.T) {
	store, _ := generationStoreFixture(t)
	other, err := NewStore(store.Root, store.Lock)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	requests := []LeaseRequest{
		{Generation: generation, LeaseID: "herdr:fleet-a", FleetID: testFleetID, Consumer: "herdr-server", Evidence: "session=fleet-a"},
		{Generation: generation, LeaseID: "herdr:fleet-b", FleetID: "f_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Consumer: "herdr-server", Evidence: "session=fleet-b"},
	}
	leases := make([]*Lease, 0, len(requests))
	for index, request := range requests {
		owner := store
		if index == 1 {
			owner = other
		}
		lease, err := owner.AcquireLease(request)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	t.Cleanup(func() {
		for _, lease := range leases {
			_ = lease.Close()
		}
	})
	if err := os.Remove(filepath.Join(store.Root, "runtime", currentName)); err != nil {
		t.Fatal(err)
	}
	adopted, err := other.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.BundleDir != runtime.BundleDir {
		t.Fatalf("adopted bundle = %q, want shared generation %q", adopted.BundleDir, runtime.BundleDir)
	}
	for _, lease := range leases {
		if _, err := os.Stat(lease.RecordPath()); err != nil {
			t.Fatalf("ensure removed live Fleet reference: %v", err)
		}
	}
}

func TestRuntimeLeaseCrashResidueIsRetainedAndRecoverableOnlyAfterLockRelease(t *testing.T) {
	store, _ := generationStoreFixture(t)
	runtime, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "waiter:session-1", FleetID: testFleetID, Consumer: "supervision-waiter", Evidence: "host=claude;session=session-1"}
	crashed, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := crashed.RecordPath()
	if err := abandonLeaseForTest(crashed); err != nil {
		t.Fatal(err)
	}

	currentPath := filepath.Join(store.Root, "runtime", currentName)
	if err := os.Remove(currentPath); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"pools/foreign-live/sentinel", "integrations/github/gh/payloads/live/sentinel"} {
		path := filepath.Join(store.Root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("retain"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	adopted, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if adopted.BundleDir != runtime.BundleDir {
		t.Fatalf("adopted bundle = %q, want %q", adopted.BundleDir, runtime.BundleDir)
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("ensure removed crash lease residue: %v", err)
	}
	for _, rel := range []string{"pools/foreign-live/sentinel", "integrations/github/gh/payloads/live/sentinel"} {
		if _, err := os.Stat(filepath.Join(store.Root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("ensure removed %s: %v", rel, err)
		}
	}

	recovered, err := store.AcquireLease(request)
	if err != nil {
		t.Fatalf("recover exact crash residue after kernel lock release: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeLeaseRemainsHeldByManagedChildAfterGuardianDeath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows couples the runtime child to its guardian with a kill-on-close job")
	}
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "herdr:fleet-a", FleetID: testFleetID, Consumer: "herdr-server", Evidence: "session=fleet-a"}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	child := exec.Command(os.Args[0], "-test.run=^TestRuntimeLeaseManagedChildProcess$")
	child.Env = append(os.Environ(), "HAND_RUNTIME_LEASE_CHILD=1", "HAND_RUNTIME_LEASE_READY="+ready)
	if err := lease.StartChild(child); err != nil {
		t.Fatal(err)
	}
	waitForGenerationHelper(t, ready)
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	if err := abandonLeaseForTest(lease); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireLease(request); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("acquire after guardian death = %v, want child-held ErrLeaseHeld", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	waited = true

	deadline := time.Now().Add(5 * time.Second)
	for {
		recovered, err := store.AcquireLease(request)
		if err == nil {
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
			break
		}
		if !errors.Is(err, ErrLeaseHeld) || time.Now().After(deadline) {
			t.Fatalf("recover after managed child exit = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRuntimeLeaseCloseRetainsManagedChildOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows couples the runtime child to its guardian with a kill-on-close job")
	}
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "herdr:fleet-a", FleetID: testFleetID, Consumer: "herdr-server", Evidence: "session=fleet-a"}
	lease, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	child := exec.Command(os.Args[0], "-test.run=^TestRuntimeLeaseManagedChildProcess$")
	child.Env = append(os.Environ(), "HAND_RUNTIME_LEASE_CHILD=1", "HAND_RUNTIME_LEASE_READY="+ready)
	if err := lease.StartChild(child); err != nil {
		t.Fatal(err)
	}
	waitForGenerationHelper(t, ready)
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.RecordPath()); err != nil {
		t.Fatalf("child-owned lease record missing after parent close: %v", err)
	}
	if _, err := store.AcquireLease(request); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("acquire while managed child is alive = %v, want ErrLeaseHeld", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	recovered, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lease.RecordPath()); err != nil {
		t.Fatalf("recovered lease record was removed after child exit: %v", err)
	}
}

func TestRuntimeLeaseManagedChildProcess(t *testing.T) {
	if os.Getenv("HAND_RUNTIME_LEASE_CHILD") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HAND_RUNTIME_LEASE_READY"), nil, 0o600); err != nil {
		os.Exit(2)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestRuntimeLeaseMalformedAndForeignResidueFailClosed(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	request := LeaseRequest{Generation: generation, LeaseID: "consumer:one", FleetID: testFleetID, Consumer: "supervision-waiter", Evidence: "session=one"}

	residue, err := store.AcquireLease(request)
	if err != nil {
		t.Fatal(err)
	}
	path := residue.RecordPath()
	if err := abandonLeaseForTest(residue); err != nil {
		t.Fatal(err)
	}
	foreign := request
	foreign.FleetID = "f_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.AcquireLease(foreign); !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("foreign Fleet reuse = %v, want ErrLeaseMetadataUnknown", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign Fleet check removed residue: %v", err)
	}

	malformed := []byte("{truncated")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireLease(request); !errors.Is(err, ErrLeaseMetadataUnknown) {
		t.Fatalf("malformed residue = %v, want ErrLeaseMetadataUnknown", err)
	}
	kept, err := os.ReadFile(path)
	if err != nil || string(kept) != string(malformed) {
		t.Fatalf("malformed residue changed: %q, %v", kept, err)
	}
}

func TestGenerationLeaseRefusesMetadataWithoutLock(t *testing.T) {
	tests := []struct {
		name, kind, oldID, oldScope, nextID, nextScope string
	}{
		{"runtime exact", "runtime", "consumer:one", "", "consumer:one", ""},
		{"hand exact", "hand", "consumer:one", "", "consumer:one", ""},
		{"scoped successor", "runtime", "waiter:old", "waiter:slot-0", "waiter:next", "waiter:slot-0"},
		{"unscoped to scoped", "runtime", "waiter:slot-0", "", "waiter:next", "waiter:slot-0"},
		{"scoped to unscoped", "runtime", "waiter:old", "waiter:slot-0", "waiter:slot-0", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := generationStoreFixture(t)
			acquire := store.AcquireLease
			generation := ""
			if test.kind == "hand" {
				source := filepath.Join(t.TempDir(), executableName("hand"))
				if err := os.WriteFile(source, []byte("managed Hand generation"), 0o700); err != nil {
					t.Fatal(err)
				}
				managed, err := store.MaterializeHandExecutable(source)
				if err != nil {
					t.Fatal(err)
				}
				generation = "sha256:" + filepath.Base(filepath.Dir(managed))
				acquire = store.AcquireHandLease
			} else {
				if _, err := store.Ensure(context.Background(), "", ""); err != nil {
					t.Fatal(err)
				}
				var err error
				generation, err = store.GenerationID("", "")
				if err != nil {
					t.Fatal(err)
				}
			}
			old := LeaseRequest{Generation: generation, LeaseID: test.oldID, LockScope: test.oldScope, FleetID: testFleetID, Consumer: "herdr-server", Evidence: "session=one"}
			lease, err := acquire(old)
			if err != nil {
				t.Fatal(err)
			}
			if err := abandonLeaseForTest(lease); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(lease.LockPath()); err != nil {
				t.Fatal(err)
			}
			next := old
			next.LeaseID, next.LockScope = test.nextID, test.nextScope
			if _, err := acquire(next); !errors.Is(err, ErrLeaseMetadataUnknown) {
				t.Fatalf("acquire without durable lock = %v, want ErrLeaseMetadataUnknown", err)
			}
			if _, err := os.Stat(lease.LockPath()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing lock was recreated: %v", err)
			}
			if _, err := os.Stat(lease.RecordPath()); err != nil {
				t.Fatalf("durable lease record was removed: %v", err)
			}
		})
	}
}

func abandonLeaseForTest(lease *Lease) error {
	lease.closed = true
	return errors.Join(lease.lock.Close(), lease.rootHandle.Close())
}

func TestRuntimeLeaseRejectsUnverifiedIdentityInputs(t *testing.T) {
	store, _ := generationStoreFixture(t)
	if _, err := store.Ensure(context.Background(), "", ""); err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	valid := LeaseRequest{Generation: generation, LeaseID: "lease-1", FleetID: testFleetID, Consumer: "herdr-server", Evidence: "session=one"}
	for name, mutate := range map[string]func(*LeaseRequest){
		"generation": func(request *LeaseRequest) { request.Generation = "g_unknown" },
		"lease":      func(request *LeaseRequest) { request.LeaseID = "bad\x00lease" },
		"lock scope": func(request *LeaseRequest) { request.LockScope = "bad\x00scope" },
		"fleet":      func(request *LeaseRequest) { request.FleetID = "f_reused-pid" },
		"consumer":   func(request *LeaseRequest) { request.Consumer = "" },
		"evidence":   func(request *LeaseRequest) { request.Evidence = "" },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if _, err := store.AcquireLease(request); err == nil {
				t.Fatal("invalid lease request was accepted")
			}
		})
	}
}
