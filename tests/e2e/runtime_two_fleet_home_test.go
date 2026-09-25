//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/supervision"
	"github.com/atqamz/hand/internal/toolchain"
)

// Proves atqamz/hand#519's qualification gap: two real `hand init` Fleet homes sharing one
// user-global SECONDHAND_HOME. Other e2e tests give each home a private runtime (newHome).
func TestTwoFleetHomesShareOneUserGlobalRuntime(t *testing.T) {
	sharedParent := t.TempDir()
	sharedRoot := filepath.Join(sharedParent, ".secondhand")
	seedPrivateRuntime(t, sharedParent)

	dir := binDir(t)
	// Hang keeps the fake `herdr ... server` blocked instead of exiting immediately, so its
	// runtime lease stays live like a real managed Herdr server until this test kills it.
	faketool.Herdr{Hang: []string{"server"}}.Install(t, dir)

	homeA := newSharedHome(t, sharedRoot)
	homeB := newSharedHome(t, sharedRoot)

	fleetA, err := state.FleetIDReadOnly(homeA)
	if err != nil {
		t.Fatal(err)
	}
	fleetB, err := state.FleetIDReadOnly(homeB)
	if err != nil {
		t.Fatal(err)
	}
	if fleetA == fleetB {
		t.Fatalf("fleet IDs = %q for both homes, want distinct", fleetA)
	}

	lock, err := toolchain.LoadLock()
	if err != nil {
		t.Fatal(err)
	}
	store, err := toolchain.NewStore(sharedRoot, lock)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.GenerationID("", "")
	if err != nil {
		t.Fatal(err)
	}

	retained := []string{
		filepath.Join(sharedRoot, "pools", "foreign-live", "sentinel"),
		filepath.Join(sharedRoot, "integrations", "github", "gh", "payloads", "foreign-live", "sentinel"),
	}
	for _, path := range retained {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("retain"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sessionA, sessionB := herdr.SessionName(fleetA), herdr.SessionName(fleetB)
	requestA := runtimeLeaseRequest(generation, fleetA, sessionA)
	requestB := runtimeLeaseRequest(generation, fleetB, sessionB)

	serverA := startHandRuntimeHerdrServer(t, homeA, sharedRoot, fleetA, generation, sessionA)
	serverB := startHandRuntimeHerdrServer(t, homeB, sharedRoot, fleetB, generation, sessionB)
	waitForRuntimeLeaseHeld(t, store, requestA, serverA)
	waitForRuntimeLeaseHeld(t, store, requestB, serverB)
	assertHandLeaseHeld(t, store, handExecutableLeaseRequest(t, fleetA, sessionA), true)
	assertHandLeaseHeld(t, store, handExecutableLeaseRequest(t, fleetB, sessionB), true)

	if got := runtimeGenerationCount(t, sharedRoot); got != 1 {
		t.Fatalf("runtime generations before any ensure = %d, want one", got)
	}

	// Two real Fleet homes concurrently ensure/adopt the same runtime generation while both
	// already hold a live lease against it.
	runConcurrentRuntimeEnsure(t, homeA, homeB, sharedRoot, generation)
	if got := runtimeGenerationCount(t, sharedRoot); got != 1 {
		t.Fatalf("runtime generations after concurrent ensure = %d, want the one deterministic generation, not a minted duplicate", got)
	}
	assertRuntimeLeaseHeld(t, store, requestA, true)
	assertRuntimeLeaseHeld(t, store, requestB, true)
	assertFilesExist(t, retained)

	// Losing the shared selection pointer (an operator on home A deleting current.json, or a
	// crash mid-write) must not affect home B's live lease, and resolving it again must not
	// mutate the pointer or mint a second generation.
	current := filepath.Join(sharedRoot, "runtime", "current.json")
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	for _, home := range []string{homeA, homeB} {
		got := runSharedHand(t, home, sharedRoot, "runtime", "status")
		if got.code != 0 {
			t.Fatalf("hand runtime status without a selection pointer: exit %d, stderr %q", got.code, got.stderr)
		}
		for _, want := range []string{"ready: true", "selection: absent", "bundle: " + bundleDir(sharedRoot, generation)} {
			if !strings.Contains(got.stdout, want) {
				t.Fatalf("hand runtime status stdout = %q, want it to contain %q", got.stdout, want)
			}
		}
	}
	if _, err := os.Stat(current); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hand runtime status recreated the missing selection pointer: %v", err)
	}
	assertRuntimeLeaseHeld(t, store, requestB, true)

	runConcurrentRuntimeEnsure(t, homeA, homeB, sharedRoot, generation)
	if got := runtimeGenerationCount(t, sharedRoot); got != 1 {
		t.Fatalf("runtime generations after re-ensuring a lost pointer = %d, want one", got)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("hand runtime ensure did not restore the selection pointer: %v", err)
	}
	assertRuntimeLeaseHeld(t, store, requestB, true)
	assertFilesExist(t, retained)

	// Home A crashes: its lease disappears with its process, home B's does not, and no Fleet
	// (including B) can take over A's crash residue. No CLI reaches this refusal (0.8.0 has no
	// runtime lease/prune command), so it is proven at the toolchain layer, as atqamz/hand#616 does.
	killHandBackground(t, serverA)
	assertRuntimeLeaseHeld(t, store, requestA, false)
	assertRuntimeLeaseHeld(t, store, requestB, true)
	foreignTakeover := requestA
	foreignTakeover.FleetID = fleetB
	if _, err := store.AcquireLease(foreignTakeover); !errors.Is(err, toolchain.ErrLeaseMetadataUnknown) {
		t.Fatalf("fleet B acquired fleet A's crash residue: %v, want %v", err, toolchain.ErrLeaseMetadataUnknown)
	}
	assertFilesExist(t, retained)

	// Home A restarts and adopts the same generation again; home B was never disturbed.
	serverA = startHandRuntimeHerdrServer(t, homeA, sharedRoot, fleetA, generation, sessionA)
	waitForRuntimeLeaseHeld(t, store, requestA, serverA)
	assertRuntimeLeaseHeld(t, store, requestB, true)
	if got := runtimeGenerationCount(t, sharedRoot); got != 1 {
		t.Fatalf("runtime generations after restart = %d, want one", got)
	}
	assertFilesExist(t, retained)
}

func newSharedHome(t *testing.T, secondhandRoot string) string {
	t.Helper()
	isolateGitConfig(t)
	home := t.TempDir()
	if got := runSharedHand(t, home, secondhandRoot, "init"); got.code != 0 {
		t.Fatalf("hand init: exit %d, stderr %q", got.code, got.stderr)
	}
	return home
}

// Builds the environment directly with handProcessEnv, like TestCanonicalInitCompetingProcesses
// does, instead of runHand/runHandEnv: those append their own per-home SECONDHAND_HOME after any
// caller override, which would shadow the one shared root two homes need to actually share.
func runSharedHand(t *testing.T, home, secondhandRoot string, args ...string) invocation {
	t.Helper()
	cmd := exec.Command(handBin, args...)
	cmd.Dir = home
	cmd.Env = handProcessEnv("SECONDHAND_HOME=" + secondhandRoot)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run hand %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	t.Logf("$ hand %s (SECONDHAND_HOME=%s)\n  exit %d\n  stdout: %s\n  stderr: %s",
		strings.Join(args, " "), secondhandRoot, code, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func runConcurrentRuntimeEnsure(t *testing.T, homeA, homeB, secondhandRoot, generation string) {
	t.Helper()
	results := make([]invocation, 2)
	var wg sync.WaitGroup
	homes := []string{homeA, homeB}
	for i, home := range homes {
		wg.Add(1)
		go func(i int, home string) {
			defer wg.Done()
			results[i] = runSharedHand(t, home, secondhandRoot, "runtime", "ensure")
		}(i, home)
	}
	wg.Wait()
	for i, got := range results {
		if got.code != 0 {
			t.Fatalf("home %s: hand runtime ensure: exit %d, stderr %q", homes[i], got.code, got.stderr)
		}
		if want := "bundle: " + bundleDir(secondhandRoot, generation); !strings.Contains(got.stdout, want) {
			t.Fatalf("home %s: hand runtime ensure stdout = %q, want it to contain %q", homes[i], got.stdout, want)
		}
	}
}

// Mirrors startHandBackgroundEnv (background_*_test.go's process-group reaping applies the same
// way) but, like runSharedHand, sets SECONDHAND_HOME directly instead of through the per-home
// helper that would shadow it.
func startHandRuntimeHerdrServer(t *testing.T, home, secondhandRoot, fleetID, generation, session string) *backgroundHand {
	t.Helper()
	args := []string{"runtime", "herdr-server", "--fleet-id", fleetID, "--generation", generation, "--session", session}
	cmd := exec.Command(handBin, args...)
	cmd.Dir = home
	cmd.Env = handProcessEnv("SECONDHAND_HOME=" + secondhandRoot)
	stdout := &syncBuffer{}
	stderr := &syncBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	process, err := startBackgroundProcess(cmd)
	if err != nil {
		t.Fatalf("start hand %v: %v", args, err)
	}
	b := &backgroundHand{cmd: cmd, process: process, args: args, stdout: stdout, stderr: stderr}
	t.Cleanup(func() {
		if b.reaping {
			return
		}
		if cmd.ProcessState == nil {
			process.stop()
			_ = cmd.Wait()
		}
		if err := process.close(); err != nil {
			t.Errorf("close background hand process: %v", err)
		}
	})
	return b
}

func killHandBackground(t *testing.T, b *backgroundHand) {
	t.Helper()
	b.process.stop()
	_ = b.cmd.Wait()
}

func runtimeLeaseRequest(generation, fleetID, session string) toolchain.LeaseRequest {
	return toolchain.LeaseRequest{
		Generation: generation,
		LeaseID:    "herdr-server:" + session,
		FleetID:    fleetID,
		Consumer:   "herdr-server",
		Evidence:   "session=" + session,
	}
}

// `hand runtime herdr-server` also takes a second, independent lease on the exact Hand
// executable it was invoked as (its "runtime guardian" duty), keyed by that executable's content
// digest rather than the runtime generation.
func handExecutableLeaseRequest(t *testing.T, fleetID, session string) toolchain.LeaseRequest {
	t.Helper()
	generation, err := supervision.ExecutableGeneration(handBin)
	if err != nil {
		t.Fatal(err)
	}
	return toolchain.LeaseRequest{
		Generation: generation,
		LeaseID:    "herdr-guardian:" + session,
		FleetID:    fleetID,
		Consumer:   "runtime-guardian",
		Evidence:   "session=" + session,
	}
}

func bundleDir(secondhandRoot, generation string) string {
	return filepath.Join(secondhandRoot, "runtime", "bundles", generation)
}

func runtimeGenerationCount(t *testing.T, secondhandRoot string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(secondhandRoot, "runtime", "bundles"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func waitForRuntimeLeaseHeld(t *testing.T, store *toolchain.Store, request toolchain.LeaseRequest, server *backgroundHand) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if held, err := store.RuntimeLeaseHeld(request); err == nil && held {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	held, err := store.RuntimeLeaseHeld(request)
	t.Fatalf("timed out waiting for runtime lease %s to be held; held=%t err=%v herdr-server stdout=%q stderr=%q",
		request.LeaseID, held, err, server.stdout.String(), server.stderr.String())
}

func assertRuntimeLeaseHeld(t *testing.T, store *toolchain.Store, request toolchain.LeaseRequest, want bool) {
	t.Helper()
	held, err := store.RuntimeLeaseHeld(request)
	if err != nil || held != want {
		t.Fatalf("runtime lease %s held = %t, %v; want %t", request.LeaseID, held, err, want)
	}
}

func assertHandLeaseHeld(t *testing.T, store *toolchain.Store, request toolchain.LeaseRequest, want bool) {
	t.Helper()
	held, err := store.HandLeaseHeld(request)
	if err != nil || held != want {
		t.Fatalf("Hand executable lease %s held = %t, %v; want %t", request.LeaseID, held, err, want)
	}
}

func assertFilesExist(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s to remain, got: %v", path, err)
		}
	}
}
