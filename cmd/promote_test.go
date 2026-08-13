package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atqamz/hand/internal/faketool"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
)

func promoteHerdr(agent, status string) faketool.Herdr {
	return faketool.Herdr{
		Workspaces: []faketool.HerdrWorkspace{{
			ID: "wA", Label: "hand:myproj",
			Tabs: []faketool.HerdrTab{
				{ID: "wA:tOld", Label: "old", Pane: "wA:pOld"},
				{ID: "wA:tOther", Label: "other", Pane: "wA:pOther"},
			},
		}},
		TabCreates: []faketool.HerdrTab{{ID: "wA:tNew", Label: "task-1", Pane: "wA:pNew"}},
		PaneAgent:  agent, PaneStatus: status,
	}
}

func setupPromoteHome(t *testing.T, oldWorktree, newWorktree string, herdr faketool.Herdr) string {
	t.Helper()
	useFastLaunchPolling(t)
	t.Setenv("HAND_HARNESS", harness.Claude)
	home := t.TempDir()

	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("scout findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("implement the fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := project.Add(home, project.Project{Name: "myproj", URL: "https://example.com/myproj.git", Mode: project.ModeDirectPR}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "projects", "myproj"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := state.Write(home, state.Task{
		ID:       "task-1",
		Project:  "myproj",
		Kind:     state.KindScout,
		Worktree: oldWorktree,
		Herdr:    state.Herdr{WorkspaceID: "wA", TabID: "wA:tOld", PaneID: "wA:pOld"},
	}); err != nil {
		t.Fatal(err)
	}

	bin := faketool.Bin(t)
	callLog := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("HERDR_CALL_LOG", callLog)
	herdr.Log = callLog
	herdr.Install(t, bin)
	faketool.Treehouse{Slots: []string{newWorktree, oldWorktree}}.Install(t, bin)
	t.Chdir(home)
	mkFleetDirs(t, home)
	return home
}

func TestPromoteUsesDetectedHarnessWithoutConfiguredOverride(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, promoteHerdr("codex", "done"))
	t.Setenv("HAND_HARNESS", harness.Codex)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindShip {
		t.Fatalf("kind = %q, want ship", got.Kind)
	}
	if got.Worktree != newWt {
		t.Fatalf("worktree = %q, want %q", got.Worktree, newWt)
	}
	if got.Herdr.TabID != "wA:tNew" || got.Herdr.PaneID != "wA:pNew" {
		t.Fatalf("herdr = %+v, want new tab/pane", got.Herdr)
	}
	if got.Harness != harness.Codex {
		t.Fatalf("harness = %q, want detected %q", got.Harness, harness.Codex)
	}
}

func TestPromoteLaunchCarriesWorkerRoleAndResolvedFleetHome(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, promoteHerdr("claude", "done"))
	t.Setenv("HAND_HOME", ".")
	callLog := os.Getenv("HERDR_CALL_LOG")

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	launch := string(calls)
	env := "HAND_ROLE=worker HAND_HOME='" + home + "'"
	envAt := strings.Index(launch, env)
	if envAt < 0 {
		t.Fatalf("launch = %q, want absolute worker environment %q", launch, env)
	}
	if harnessAt := strings.Index(launch, "claude --dangerously"); harnessAt < 0 || envAt > harnessAt {
		t.Fatalf("launch = %q, want worker environment before harness executable", launch)
	}
}

func TestPromoteConfiguredHarnessWinsOverDetectedHarness(t *testing.T) {
	home := setupPromoteHome(t, filepath.Join(t.TempDir(), "old-wt"), filepath.Join(t.TempDir(), "new-wt"), promoteHerdr("claude", "done"))
	t.Setenv("HAND_HARNESS", harness.Codex)
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "harness"), []byte(harness.Claude+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Harness != harness.Claude {
		t.Fatalf("harness = %q, want configured %q", got.Harness, harness.Claude)
	}
}

func TestPromoteUnknownDetectedHarnessFailsBeforeWorktreeAcquisition(t *testing.T) {
	home := setupPromoteHome(t, filepath.Join(t.TempDir(), "old-wt"), filepath.Join(t.TempDir(), "new-wt"), promoteHerdr("claude", "done"))
	t.Setenv("HAND_HARNESS", "unknown")
	bin := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))[0]

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	assertExitCode3(t, err)
	if !strings.Contains(err.Error(), "current supervisor harness is unknown") {
		t.Fatalf("error = %v, want unknown-supervisor remedy", err)
	}
	if _, statErr := os.Stat(filepath.Join(bin, ".treehouse-leases")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("treehouse acquisition counter exists: %v", statErr)
	}
	got, readErr := state.Read(home, "task-1")
	if readErr != nil || got.Kind != state.KindScout {
		t.Fatalf("task after refusal = %#v, %v, want unchanged scout", got, readErr)
	}
}

func TestPromoteResetsPaneScopedMarkersButCarriesReportOffset(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, promoteHerdr(harness.Codex, "done"))

	scout, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	scout.DoneVerified = true
	scout.Harness = harness.Claude
	scout.Model = "scout-model"
	scout.Effort = "scout-effort"
	scout.LeaseID = "lease-old"
	scout.ReportOffset = 42
	scout.ReportDigest = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	stale := time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339)
	scout.StatusChangedAt = stale
	scout.StatusChangedFor = "working"
	scout.LastReportState = state.ReportDone
	scout.LastReportNote = "scout findings"
	scout.DeliveredAt = "2026-08-03T00:00:00Z"
	scout.DeliveredReason = "report at data/task-1/report.md, no code to land"
	scout.UsageLimitRetryAt = "2026-08-03T01:00:00Z"
	scout.UsageLimitAttempts = 2
	if err := state.Write(home, scout); err != nil {
		t.Fatal(err)
	}
	if err := state.SetHold(home, state.Hold{ID: "task-1", Kind: state.HoldKindLimit, Reason: "out of quota"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1", "--harness", harness.Codex, "--model", "ship-model", "--effort", "ship-effort"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DoneVerified {
		t.Fatal("DoneVerified = true, want the scout's marker cleared for the ship run")
	}
	if got.Harness != harness.Codex || got.Model != "ship-model" || got.Effort != "ship-effort" {
		t.Fatalf("execution tier = %q/%q/%q, want the newly resolved ship values", got.Harness, got.Model, got.Effort)
	}
	if got.Worktree != newWt || got.LeaseID != "lease-1" {
		t.Fatalf("worktree/lease = %q/%q, want the new ship execution identity", got.Worktree, got.LeaseID)
	}
	if got.Herdr.Session != "default" || got.Herdr.WorkspaceID != "wA" || got.Herdr.TabID != "wA:tNew" || got.Herdr.PaneID != "wA:pNew" {
		t.Fatalf("herdr = %+v, want the new ship execution identity", got.Herdr)
	}
	if got.ReportOffset != 42 {
		t.Fatalf("ReportOffset = %d, want the scout's offset carried forward", got.ReportOffset)
	}
	// The offset is only trusted together with the digest of what it consumed, so
	// carrying one without the other discards it and replays the scout's history.
	if got.ReportDigest != scout.ReportDigest {
		t.Fatalf("ReportDigest = %q, want the scout's digest carried forward with its offset", got.ReportDigest)
	}
	if got.StatusChangedAt == stale {
		t.Fatal("StatusChangedAt kept the scout's transition, want the ship's dwell reseeded at promotion")
	}
	changed, err := time.Parse(time.RFC3339, got.StatusChangedAt)
	if err != nil {
		t.Fatalf("parse StatusChangedAt %q: %v", got.StatusChangedAt, err)
	}
	if time.Since(changed) > time.Minute {
		t.Fatalf("StatusChangedAt = %q, want it stamped at promotion time", got.StatusChangedAt)
	}
	if got.StatusChangedFor != "" {
		t.Fatalf("StatusChangedFor = %q, want it cleared so the ship's first observed status starts a fresh dwell", got.StatusChangedFor)
	}
	if got.LastReportState != "" || got.LastReportNote != "" {
		t.Fatalf("LastReportState/Note = %q/%q, want the scout's report evidence cleared for the ship run", got.LastReportState, got.LastReportNote)
	}
	// Carried forward, the mark would let teardown accept the ship task as terminal
	// on a delivery that only ever described the scout's report.
	if got.DeliveredAt != "" || got.DeliveredReason != "" {
		t.Fatalf("DeliveredAt/Reason = %q/%q, want the scout's delivery cleared for the ship run", got.DeliveredAt, got.DeliveredReason)
	}
	// The scout's harness process is gone with its pane. Carried forward, the schedule
	// would steer the ship's fresh pane on a clock the scout's refusal set, and the
	// hold would refuse a spawn over quota the ship is not short of.
	if got.UsageLimitRetryAt != "" || got.UsageLimitAttempts != 0 {
		t.Fatalf("usage-limit columns = %q/%d, want the scout's limit schedule cleared", got.UsageLimitRetryAt, got.UsageLimitAttempts)
	}
	if _, found, err := state.ReadHold(home, "task-1"); err != nil || found {
		t.Fatalf("ReadHold = %v, %v, want the scout's limit hold cleared", found, err)
	}
}

func TestPromoteRefusesNonScout(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindShip}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesMissingReport(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesMissingBrief(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func TestPromoteRefusesUnregisteredProject(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	mkFleetDirs(t, home)
	if err := os.MkdirAll(filepath.Join(home, "data", "task-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "report.md"), []byte("findings"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "data", "task-1", "brief.md"), []byte("implement the fix"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := state.Write(home, state.Task{ID: "task-1", Kind: state.KindScout, Project: "unregistered-proj"}); err != nil {
		t.Fatal(err)
	}

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}

func promoteLeakHerdr(workspaceExists bool) faketool.Herdr {
	herdr := promoteHerdr("claude", "done")
	herdr.Responses = []faketool.HerdrResponse{{Command: "pane run", Exit: 1}}
	if !workspaceExists {
		herdr.Workspaces = nil
		herdr.Creates = []faketool.HerdrWorkspace{{
			ID: "wA", Label: "myproj",
			Tabs: []faketool.HerdrTab{{ID: "wA:tNew", Label: "1", Pane: "wA:pNew"}},
		}}
	}
	return herdr
}

func TestPromoteFailureClosesWorkspaceItCreated(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, promoteLeakHerdr(false))
	callLog := setupSpawnLeakEnv(t, false)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected promote to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "workspace close wA") {
		t.Fatalf("calls = %q, want the workspace hand created to be closed", calls)
	}
	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindScout {
		t.Fatalf("kind = %q, want the task left as a scout", got.Kind)
	}
}

func TestPromoteKeepsShipAfterScoutCleanupFailure(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	herdr := promoteHerdr("claude", "done")
	herdr.Responses = []faketool.HerdrResponse{{Command: "tab close", Exit: 1}}
	home := setupPromoteHome(t, oldWt, newWt, herdr)

	var errOut strings.Builder
	var out strings.Builder
	cmd := newPromoteCmd()
	cmd.SetErr(&errOut)
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	got, err := state.Read(home, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != state.KindShip || got.Worktree != newWt || got.Herdr.TabID != "wA:tNew" {
		t.Fatalf("task after scout cleanup failure = %+v, want the durably written ship execution", got)
	}
	if !strings.Contains(errOut.String(), "herdr tab close failed") {
		t.Fatalf("stderr = %q, want a cleanup warning", errOut.String())
	}
	if !strings.Contains(out.String(), "result: promoted\n") {
		t.Fatalf("output = %q, want promotion success after cleanup warning", out.String())
	}
}

func TestPromoteFailureKeepsPreexistingWorkspace(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	setupPromoteHome(t, oldWt, newWt, promoteLeakHerdr(true))
	callLog := setupSpawnLeakEnv(t, true)

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected promote to fail")
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "workspace close") {
		t.Fatalf("calls = %q, want the pre-existing shared workspace left open", calls)
	}
	if !strings.Contains(string(calls), "tab close wA:tNew") {
		t.Fatalf("calls = %q, want only the new tab closed", calls)
	}
}

func TestPromoteRefusesWhenAgentStillWorking(t *testing.T) {
	oldWt := filepath.Join(t.TempDir(), "old-wt")
	newWt := filepath.Join(t.TempDir(), "new-wt")
	home := setupPromoteHome(t, oldWt, newWt, promoteHerdr("claude", "working"))
	_ = home

	cmd := newPromoteCmd()
	cmd.SetArgs([]string{"task-1"})
	err := cmd.Execute()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 3 {
		t.Fatalf("got %v, want ExitError code 3", err)
	}
}
