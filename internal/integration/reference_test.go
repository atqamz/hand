package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

const integrationTestFleetID = "f_0123456789abcdef0123456789abcdef"

func abandonPayloadReferenceForTest(reference *PayloadReference) error {
	reference.closed = true
	return errors.Join(reference.lock.Close(), reference.rootHandle.Close())
}

func installReferenceFixture(t *testing.T) (*Store, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(source, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := NewStore(t.TempDir())
	path, err := store.Install("github/gh", source)
	if err != nil {
		t.Fatal(err)
	}
	return store, path
}

func TestPayloadReferenceRetainsExactObjectWithoutSelection(t *testing.T) {
	store, path := installReferenceFixture(t)
	request := PayloadReferenceRequest{
		ReferenceID: "run-1", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	reference, err := store.AcquireReference("github/gh", path, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reference.Close() })
	if _, err := store.AcquireReference("github/gh", path, request); !errors.Is(err, ErrPayloadReferenceHeld) {
		t.Fatalf("second acquisition = %v, want ErrPayloadReferenceHeld", err)
	}
	if err := store.Remove("github/gh"); err != nil {
		t.Fatal(err)
	}
	for _, retained := range []string{path, reference.RecordPath(), reference.LockPath()} {
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("selection removal deleted %s: %v", retained, err)
		}
	}
	if err := reference.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("graceful reference release deleted payload: %v", err)
	}
	if _, err := os.Stat(reference.RecordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("graceful release left metadata: %v", err)
	}
	if _, err := os.Stat(reference.LockPath()); err != nil {
		t.Fatalf("reference removed permanent lock rendezvous: %v", err)
	}
}

func TestPayloadReferenceReusesStableLockScopeAcrossUniqueHolders(t *testing.T) {
	store, path := installReferenceFixture(t)
	request := PayloadReferenceRequest{
		ReferenceID: "run-one", LockScope: "integration:fleet:worker:slot-0",
		FleetID: integrationTestFleetID, Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	first, err := store.AcquireReference("github/gh", path, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(first.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	var record payloadReferenceRecord
	if err := json.Unmarshal(raw, &record); err != nil || record.LockScope != request.LockScope {
		t.Fatalf("durable lock scope = %q, %v; want %q", record.LockScope, err, request.LockScope)
	}
	secondRequest := request
	secondRequest.ReferenceID = "run-two"
	if _, err := store.AcquireReference("github/gh", path, secondRequest); !errors.Is(err, ErrPayloadReferenceHeld) {
		t.Fatalf("second holder in stable lock scope = %v, want ErrPayloadReferenceHeld", err)
	}
	firstRecord, firstLock := first.RecordPath(), first.LockPath()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := store.AcquireReference("github/gh", path, secondRequest)
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
	if len(entries) != 1 || entries[0].Name() != filepath.Base(firstLock) {
		t.Fatalf("stable reference scope left %v, want one permanent rendezvous", entries)
	}
}

func TestPayloadReferenceRetiresThroughItsAcquisitionRoot(t *testing.T) {
	store, path := installReferenceFixture(t)
	reference, err := store.AcquireReference("github/gh", path, PayloadReferenceRequest{
		ReferenceID: "root-replacement", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(reference.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(store.Root)
	moved := filepath.Join(parent, "moved-secondhand")
	if err := os.Rename(store.Root, moved); err != nil {
		if runtime.GOOS == "windows" {
			if closeErr := reference.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return
		}
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(reference.RecordPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reference.RecordPath(), record, 0o600); err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(store.Root, reference.RecordPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := reference.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(moved, relative)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("acquired-root reference record was not retired: %v", err)
	}
	if got, err := os.ReadFile(reference.RecordPath()); err != nil || !bytes.Equal(got, record) {
		t.Fatalf("replacement-root decoy changed: %q, %v", got, err)
	}
}

func TestPayloadReferenceCrashAndUnknownMetadataFailClosed(t *testing.T) {
	store, path := installReferenceFixture(t)
	request := PayloadReferenceRequest{
		ReferenceID: "run-1", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	reference, err := store.AcquireReference("github/gh", path, request)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := reference.RecordPath()
	if err := abandonPayloadReferenceForTest(reference); err != nil {
		t.Fatal(err)
	}

	foreign := request
	foreign.FleetID = "f_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.AcquireReference("github/gh", path, foreign); !errors.Is(err, ErrPayloadReferenceUnknown) {
		t.Fatalf("foreign Fleet residue = %v, want ErrPayloadReferenceUnknown", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("foreign residue check deleted payload: %v", err)
	}
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"pid", "cwd", "process_path", "executable_path"} {
		if _, found := record[forbidden]; found {
			t.Fatalf("reference uses diagnostic-only %q as ownership evidence", forbidden)
		}
	}

	malformed := []byte("{truncated")
	if err := os.WriteFile(recordPath, malformed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireReference("github/gh", path, request); !errors.Is(err, ErrPayloadReferenceUnknown) {
		t.Fatalf("malformed residue = %v, want ErrPayloadReferenceUnknown", err)
	}
	kept, err := os.ReadFile(recordPath)
	if err != nil || string(kept) != string(malformed) {
		t.Fatalf("malformed residue changed: %q, %v", kept, err)
	}
}

func TestPayloadReferenceRejectsAliasesAndInvalidIdentity(t *testing.T) {
	store, path := installReferenceFixture(t)
	request := PayloadReferenceRequest{
		ReferenceID: "run-1", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	alias := filepath.Join(t.TempDir(), "gh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alias, data, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireReference("github/gh", alias, request); err == nil {
		t.Fatal("payload alias outside the exact store was accepted")
	}
	invalid := request
	invalid.FleetID = "f_reused-pid"
	if _, err := store.AcquireReference("github/gh", path, invalid); err == nil {
		t.Fatal("invalid Fleet identity was accepted")
	}
	invalid = request
	invalid.LockScope = "bad\x00scope"
	if _, err := store.AcquireReference("github/gh", path, invalid); err == nil {
		t.Fatal("invalid lock scope was accepted")
	}
}

func TestPayloadReferenceRejectsSymlinkedPayloadParent(t *testing.T) {
	store, path := installReferenceFixture(t)
	payloads := filepath.Dir(filepath.Dir(path))
	external := filepath.Join(t.TempDir(), "external-payloads")
	if err := os.Rename(payloads, external); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, payloads); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	redirected := filepath.Join(payloads, filepath.Base(filepath.Dir(path)), filepath.Base(path))
	request := PayloadReferenceRequest{
		ReferenceID: "run-1", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	if _, err := store.AcquireReference("github/gh", redirected, request); err == nil {
		t.Fatal("payload through a symlinked parent was accepted")
	}
}

func TestManagedRunHoldsExactPayloadReference(t *testing.T) {
	if legacyCapabilityFallback {
		t.Skip("test-tag builds intentionally execute PATH fakes")
	}
	root := t.TempDir()
	t.Setenv("SECONDHAND_HOME", root)
	t.Setenv("HAND_HOME", "")
	ready := filepath.Join(root, "ready")
	release := filepath.Join(root, "release")
	t.Setenv("HAND_INTEGRATION_REFERENCE_HELPER", "1")
	t.Setenv("HAND_INTEGRATION_REFERENCE_READY", ready)
	t.Setenv("HAND_INTEGRATION_REFERENCE_RELEASE", release)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	payload, err := store.Install("github/gh", executable)
	if err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, _, runErr := Run(context.Background(), "github/gh", "", "-test.run=^TestManagedRunProcessHelper$")
		result <- runErr
	}()
	waitForReferenceTestFile(t, ready)
	records, err := filepath.Glob(filepath.Join(root, "integrations", "github", "gh", "references", "*", "*.json"))
	if err != nil || len(records) != 1 {
		t.Fatalf("live reference records = %q, %v; want one", records, err)
	}
	if err := store.Remove("github/gh"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("selection removal deleted executing payload: %v", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("managed integration process did not exit")
	}
	if _, err := os.Stat(records[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed process left live reference metadata: %v", err)
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("completed process deleted immutable payload: %v", err)
	}
}

func TestRepeatedManagedRunsReusePayloadLockScope(t *testing.T) {
	if legacyCapabilityFallback {
		t.Skip("test-tag builds intentionally execute PATH fakes")
	}
	root := t.TempDir()
	t.Setenv("SECONDHAND_HOME", root)
	t.Setenv("HAND_HOME", "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(root)
	path, err := store.Install("github/gh", executable)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := Run(context.Background(), "github/gh", "", "-test.run=^TestManagedRunProcessHelper$"); err != nil {
			t.Fatal(err)
		}
	}
	locks, err := filepath.Glob(filepath.Join(root, "integrations", "github", "gh", "references", filepath.Base(filepath.Dir(path)), "*.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 {
		t.Fatalf("permanent payload lock rendezvous = %q, want one reused scope", locks)
	}
}

func TestManagedRunProcessHelper(t *testing.T) {
	if os.Getenv("HAND_INTEGRATION_REFERENCE_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HAND_INTEGRATION_REFERENCE_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("HAND_INTEGRATION_REFERENCE_RELEASE")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("release was not published")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPayloadReferenceChildProcessHelper(t *testing.T) {
	if os.Getenv("HAND_INTEGRATION_CHILD_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("HAND_INTEGRATION_CHILD_READY"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitForReferenceTestFile(t, os.Getenv("HAND_INTEGRATION_CHILD_RELEASE"))
}

func waitForReferenceTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
