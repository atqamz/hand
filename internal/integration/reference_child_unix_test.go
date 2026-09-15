//go:build !windows

package integration

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPayloadReferenceRemainsHeldByManagedChildAfterParentRelease(t *testing.T) {
	store, path := installExecutableReferenceFixture(t)
	request := PayloadReferenceRequest{
		ReferenceID: "managed-child", FleetID: integrationTestFleetID,
		Consumer: "integration-process", Evidence: "capability=github/gh",
	}
	reference, err := store.AcquireReference("github/gh", path, request)
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(t.TempDir(), "ready")
	release := filepath.Join(t.TempDir(), "release")
	child := exec.Command(os.Args[0], "-test.run=^TestPayloadReferenceChildProcessHelper$")
	child.Env = append(os.Environ(),
		"HAND_INTEGRATION_CHILD_HELPER=1",
		"HAND_INTEGRATION_CHILD_READY="+ready,
		"HAND_INTEGRATION_CHILD_RELEASE="+release,
	)
	if err := reference.StartChild(child); err != nil {
		t.Fatal(err)
	}
	waitForReferenceTestFile(t, ready)
	if err := abandonPayloadReferenceForTest(reference); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireReference("github/gh", path, request); !errors.Is(err, ErrPayloadReferenceHeld) {
		t.Fatalf("acquisition while managed child is alive = %v, want ErrPayloadReferenceHeld", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.AcquireReference("github/gh", path, request)
	if err != nil {
		t.Fatalf("acquisition after managed child exit: %v", err)
	}
	if err := recovered.Close(); err != nil {
		t.Fatal(err)
	}
}
