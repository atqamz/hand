package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/toolchain"
)

type twoFleetRuntimeHolderInput struct {
	Root    string
	Lock    toolchain.Lock
	Request toolchain.LeaseRequest
	Ready   string
}

func TestTwoFleetManagedRuntimeSurvivesPointerLossAndOwnerRestart(t *testing.T) {
	store := managedServerLeaseStore(t)
	want, err := store.Ensure(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}
	requests := []toolchain.LeaseRequest{
		{Generation: generation, LeaseID: "herdr:fleet-a", FleetID: "f_0123456789abcdef0123456789abcdef", Consumer: "herdr-server", Evidence: "session=fleet-a"},
		{Generation: generation, LeaseID: "herdr:fleet-b", FleetID: "f_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Consumer: "herdr-server", Evidence: "session=fleet-b"},
	}
	first := startTwoFleetRuntimeHolder(t, store, requests[0])
	startTwoFleetRuntimeHolder(t, store, requests[1])
	other, err := toolchain.NewStore(store.Root, store.Lock)
	if err != nil {
		t.Fatal(err)
	}
	other.HTTPClient = store.HTTPClient
	for _, request := range requests {
		if held, err := other.RuntimeLeaseHeld(request); err != nil || !held {
			t.Fatalf("Fleet %s lease held = %t, %v", request.FleetID, held, err)
		}
	}
	retained := []string{
		filepath.Join(store.Root, "pools", "foreign-live", "sentinel"),
		filepath.Join(store.Root, "integrations", "github", "gh", "payloads", "foreign-live", "sentinel"),
	}
	for _, path := range retained {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("retain"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(store.Root, "runtime", "current.json")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveManagedRuntime(context.Background(), other)
	if err != nil || resolved.BundleDir != want.BundleDir {
		t.Fatalf("resolve shared runtime without selection = %q, %v; want %q", resolved.BundleDir, err, want.BundleDir)
	}
	if _, err := os.Stat(current); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selection was changed: %v", err)
	}
	foreignSelection := []byte(`{"runtime_id":"foreign"}`)
	if err := os.WriteFile(current, foreignSelection, 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err = managedClientRuntime(other)
	if err != nil || resolved.BundleDir != want.BundleDir {
		t.Fatalf("resolve shared runtime with foreign selection = %q, %v; want %q", resolved.BundleDir, err, want.BundleDir)
	}
	if got, err := os.ReadFile(current); err != nil || string(got) != string(foreignSelection) {
		t.Fatalf("foreign selection changed during read: %q, %v", got, err)
	}
	stopTwoFleetRuntimeHolder(t, first)
	if held, err := other.RuntimeLeaseHeld(requests[0]); err != nil || held {
		t.Fatalf("crashed Fleet lease held = %t, %v; want false", held, err)
	}
	if held, err := other.RuntimeLeaseHeld(requests[1]); err != nil || !held {
		t.Fatalf("surviving Fleet lease held = %t, %v; want true", held, err)
	}
	foreignRequest := requests[0]
	foreignRequest.FleetID = requests[1].FleetID
	if _, err := other.AcquireLease(foreignRequest); !errors.Is(err, toolchain.ErrLeaseMetadataUnknown) {
		t.Fatalf("foreign Fleet took over crash residue: %v", err)
	}
	startTwoFleetRuntimeHolder(t, store, requests[0])
	for _, request := range requests {
		if held, err := other.RuntimeLeaseHeld(request); err != nil || !held {
			t.Fatalf("restarted Fleet %s lease held = %t, %v", request.FleetID, held, err)
		}
	}
	adopted, err := other.Ensure(context.Background(), "", "")
	if err != nil || adopted.BundleDir != want.BundleDir {
		t.Fatalf("adopt shared runtime = %q, %v; want %q", adopted.BundleDir, err, want.BundleDir)
	}
	bundles, err := os.ReadDir(filepath.Join(store.Root, "runtime", "bundles"))
	if err != nil || len(bundles) != 1 {
		t.Fatalf("runtime generations = %d, %v; want one", len(bundles), err)
	}
	for _, path := range retained {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("runtime ensure removed %s: %v", path, err)
		}
	}
}

func startTwoFleetRuntimeHolder(t *testing.T, store *toolchain.Store, request toolchain.LeaseRequest) *exec.Cmd {
	t.Helper()
	input := twoFleetRuntimeHolderInput{
		Root: store.Root, Lock: store.Lock, Request: request,
		Ready: filepath.Join(t.TempDir(), "ready"),
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTwoFleetManagedRuntimeLeaseProcess$")
	cmd.Env = append(os.Environ(), "HAND_TWO_FLEET_RUNTIME_HOLDER="+string(data))
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	waitForTestFile(t, input.Ready)
	return cmd
}

func stopTwoFleetRuntimeHolder(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed Fleet holder exited successfully")
	}
}

func TestTwoFleetManagedRuntimeLeaseProcess(t *testing.T) {
	raw := os.Getenv("HAND_TWO_FLEET_RUNTIME_HOLDER")
	if raw == "" {
		return
	}
	var input twoFleetRuntimeHolderInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	store, err := toolchain.NewStore(input.Root, input.Lock)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireLease(input.Request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	if err := os.WriteFile(input.Ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
