package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/state"
)

func TestProvisionRejectsInheritedLaunchAppendixBeforeBuildAndRecovers(t *testing.T) {
	for _, previous := range []string{harness.Grok, harness.Pi} {
		for _, cleanupFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-to-claude/cleanup-fails=%t", previous, cleanupFails), func(t *testing.T) {
				home, attempt := provisioningFixture(t)
				briefPath := filepath.Join(home, "data", "task-1", "brief.md")
				body, err := os.ReadFile(briefPath)
				if err != nil {
					t.Fatal(err)
				}
				reportPath, err := absoluteReportPath(home, attempt.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				oldReportPath := reportPath + ".previous"
				if err := harness.AppendPromptToBrief(previous, harness.Options{
					Brief: briefPath, ReportPath: oldReportPath, Kind: state.KindScout,
				}); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(briefPath)
				if err != nil {
					t.Fatal(err)
				}

				fake := &provisionHerdr{}
				r := testProvisionRuntime(fake, func(lifecyclePhase) error { return nil })
				buildCalls, returns := 0, 0
				r.deps.buildHarness = func(name string, options harness.Options) (launchSpec, error) {
					buildCalls++
					return harness.Build(name, options)
				}
				clonePath := filepath.Join(home, "projects", "demo")
				worktreePath := filepath.Join(clonePath, "leased")
				cleanupErr := errors.New("exact worktree return failed")
				r.deps.worktree.returnWithID = func(clone, worktree, leaseID string, force bool) error {
					returns++
					if clone != clonePath || worktree != worktreePath || leaseID != "lease-1" || !force {
						t.Fatalf("cleanup retargeted worktree: clone=%q worktree=%q lease=%q force=%t", clone, worktree, leaseID, force)
					}
					if cleanupFails {
						return cleanupErr
					}
					return nil
				}
				req := provisioningRequest{
					home: home, projectName: "demo", clonePath: clonePath,
					briefPath: briefPath, attempt: attempt, taskKind: state.KindShip,
				}
				if _, err := r.provision(context.Background(), req); err == nil || !strings.Contains(err.Error(), "rewrite the supervisor brief") {
					t.Fatalf("stale launch appendix was not the refusal: %v", err)
				} else if cleanupFails && !strings.Contains(err.Error(), cleanupErr.Error()) {
					t.Fatalf("refusal hid cleanup failure: %v", err)
				}
				if buildCalls != 0 || fake.createdWorkspace || fake.tabCwd != "" || fake.submitted.Executable != "" || returns != 1 {
					t.Fatalf("refused launch progressed: builds=%d returns=%d provider=%+v", buildCalls, returns, fake)
				}
				after, err := os.ReadFile(briefPath)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("refusal changed supervisor brief: %v", err)
				}
				history, err := state.ReadHistory(home, attempt.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				got := history.ActiveAttempt
				if got == nil || got.ID != attempt.ID || got.Lifecycle != state.AttemptProvisioning {
					t.Fatalf("refusal changed Attempt identity or lifecycle: %+v", got)
				}
				if got.Herdr.PaneID != "" || got.LaunchSubmittedAt != "" || got.LaunchConfirmedAt != "" {
					t.Fatalf("refusal fabricated launch evidence: %+v", got)
				}
				if cleanupFails {
					if got.Worktree != worktreePath || got.LeaseID != "lease-1" {
						t.Fatalf("failed cleanup discarded resource evidence: %+v", got)
					}
					return
				}
				if got.Worktree != "" || got.LeaseID != "" {
					t.Fatalf("successful cleanup retained returned worktree: %+v", got)
				}

				if err := os.WriteFile(briefPath, body, 0o644); err != nil {
					t.Fatal(err)
				}
				req.attempt = *got
				if _, err := r.provision(context.Background(), req); err != nil {
					t.Fatalf("explicit brief rewrite did not recover provisioning: %v", err)
				}
				prompt := strings.Join(fake.submitted.Args, " ")
				if buildCalls != 1 || fake.submitted.Executable != harness.Claude || !strings.Contains(prompt, reportPath) ||
					strings.Contains(prompt, oldReportPath) || !strings.Contains(prompt, "authorized to commit, push your branch") {
					t.Fatalf("recovered launch did not carry current report/authority: builds=%d spec=%+v", buildCalls, fake.submitted)
				}
				if fake.submitted.Env[harness.HomeEnv] != home || fake.submitted.Env[harness.RoleEnv] != harness.WorkerRole {
					t.Fatalf("recovered launch lost worker environment: %+v", fake.submitted.Env)
				}
				after, err = os.ReadFile(briefPath)
				if err != nil || !bytes.Equal(after, body) {
					t.Fatalf("argument-harness recovery rewrote the fresh brief: %v", err)
				}
				history, err = state.ReadHistory(home, attempt.TaskID)
				if err != nil {
					t.Fatal(err)
				}
				if history.ActiveAttempt == nil || history.ActiveAttempt.ID != attempt.ID || history.ActiveAttempt.Lifecycle != state.AttemptRunning {
					t.Fatalf("recovery changed the Attempt or failed to record running: %+v", history.ActiveAttempt)
				}
			})
		}
	}
}
