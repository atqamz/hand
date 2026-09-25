//go:build linux

package store

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/execguard"
	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/launch"
)

func (l *execGuardLaunchTest) launch(t *testing.T, fake faketool.Herdr) (string, string, error) {
	t.Helper()
	fake.PaneCwd = l.input.Spec.Cwd
	deps, log := l.installHerdr(t, fake)
	if fake.PaneRunShell {
		deps.settle = 10 * time.Second
	}
	state, err := launchCanonicalV19Herdr(context.Background(), l.home, l.input, deps)
	return state, log, err
}

func paneRuns(t *testing.T, log string) int {
	t.Helper()
	data, err := os.ReadFile(log)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.Count(string(data), " pane run ")
}

func TestExecGuardLaunchTypesOneInvocationAndPersistsOnlyTheVerifier(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "credential")
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", `printf %s "$HAND_WORKER_CREDENTIAL" > "$0.tmp" && mv "$0.tmp" "$0" && exec sleep 30`, marker)
	state, log, err := l.launch(t, faketool.Herdr{PaneRunShell: true})
	if persisted, key, terminal := l.binding(t); state != "succeeded" || err != nil || persisted != "succeeded" || key == "" || terminal != "" {
		t.Fatalf("Launch = %q (%q, key %q, termination %q), %v, want succeeded from the live guard's records (EG-5)", state, persisted, key, terminal, err)
	}
	waitExecGuard(t, "the harness to report its credential", func() bool { _, err := os.Stat(marker); return err == nil })
	credential, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	current, err := readCanonicalV19HerdrLaunchCurrent(context.Background(), l.home, l.input.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if row := current.Current.Request.Spec.Environment[execguard.CredentialEnv]; row.ValueDigest != launch.ExecGuardCredentialVerifier(current.FleetID, l.input.BindingID, string(credential)) {
		t.Fatalf("the harness's HAND_WORKER_CREDENTIAL does not verify against V_B %q (EG-1, EG-11)", row.ValueDigest)
	}
	assertExecGuardSecretOnlyIn(t, string(credential), l.home)
	assertExecGuardSecretOnlyIn(t, string(credential), filepath.Dir(log))
	if runs := paneRuns(t, log); runs != 1 {
		t.Fatalf("pane runs = %d, want exactly one guard invocation", runs)
	}
}

func TestExecGuardLaunchBehindAnUnreadyPaneSettlesNoEffectWithoutTyping(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	state, log, err := l.launch(t, faketool.Herdr{PaneAgent: "codex"})
	if persisted, _, _ := l.binding(t); state != "no-effect" || err == nil || persisted != "no-effect" || paneRuns(t, log) != 0 {
		t.Fatalf("Launch = %q (%q), %v with %d pane runs, want no-effect before submission and no invocation", state, persisted, err, paneRuns(t, log))
	}
	assertExecGuardHandoffGone(t, l.dir)
}

func TestExecGuardLaunchObservedAcrossAnImmediateExitStillSucceeds(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	state, _, err := l.launch(t, faketool.Herdr{PaneRunShell: true})
	if persisted, key, terminal := l.binding(t); state != "succeeded" || err != nil || key == "" || (terminal != "" && terminal != "completed") {
		t.Fatalf("Launch = %q (%q, key %q, termination %q), %v, want succeeded whether G was seen live or ceased (counterexample 13)", state, persisted, key, terminal, err)
	}
}

func TestExecGuardLaunchThatNoGuardClaimsIsUncertainAndNeverRetyped(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	state, log, err := l.launch(t, faketool.Herdr{})
	if state != "uncertain" || err == nil {
		t.Fatalf("Launch = %q, %v, want uncertain while no guard claims", state, err)
	}
	if _, err := os.Stat(filepath.Join(l.dir, "fenced")); err != nil {
		t.Fatalf("tombstone after the settle timeout: %v, want the handoff fenced before uncertain", err)
	}
	deps := canonicalV19HerdrLaunchDefaultDeps()
	deps.execGuard = true
	if state, err := reconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID, deps); state != "uncertain" || err == nil || paneRuns(t, log) != 1 {
		t.Fatalf("reconcile = %q, %v with %d pane runs, want uncertain and no second invocation (EG-6)", state, err, paneRuns(t, log))
	}
}

func TestExecGuardLaunchRefusesAHandBinaryReplacedInPlace(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	deps, log := l.installHerdr(t, faketool.Herdr{PaneCwd: l.input.Spec.Cwd})
	deps.hand = func() (string, error) { return "/opt/hand/hand (deleted)", nil }
	before := canonicalV19WorktreeCreateOperationCount(t, l.home)
	state, err := launchCanonicalV19Herdr(context.Background(), l.home, l.input, deps)
	if state != "" || err == nil || canonicalV19WorktreeCreateOperationCount(t, l.home) != before || paneRuns(t, log) != 0 {
		t.Fatalf("Launch = %q, %v, want a refusal before any row or typing when os.Executable names a deleted binary", state, err)
	}
}

func TestProductionReconcileKeepsTheRevisionOneRefusalForAGuardedLaunch(t *testing.T) {
	l := newExecGuardLaunchTest(t, "/bin/sh", "-c", "exit 0")
	l.submit(t)
	state, err := ReconcileCanonicalV19HerdrLaunch(context.Background(), l.home, l.input.OperationID)
	if _, statErr := os.Stat(execguard.Locator(l.dir)); state != "uncertain" || !errors.Is(err, ErrCanonicalV19HerdrCapabilityUnsupported) || statErr != nil {
		t.Fatalf("production reconcile = %q, %v (handoff %v), want the revision-1 refusal untouched until the platform acceptance tests pass", state, err, statErr)
	}
}
