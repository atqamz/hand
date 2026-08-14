package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/herdr"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/project"
	"github.com/atqamz/hand/internal/state"
	"github.com/atqamz/hand/internal/worktree"
	"github.com/spf13/cobra"
)

func newReopenCmd() *cobra.Command {
	var harnessName, model, effort string
	var skipGateCheck bool

	cmd := &cobra.Command{
		Use:   "reopen <id>",
		Short: "Start a new attempt for a terminal task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			id := args[0]
			homeDir, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}
			release, err := state.Lock(homeDir, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			history, err := state.ReadHistory(homeDir, id)
			if err != nil {
				return asPrecondition(err)
			}
			if history.Task.Lifecycle != state.TaskTerminal {
				return &ExitError{Err: fmt.Errorf("task %q is already open", id), Code: 3}
			}
			held, hasHold, err := state.ReadHold(homeDir, id)
			if err != nil {
				return asPrecondition(err)
			}
			if hasHold {
				return &ExitError{Err: fmt.Errorf("id %q has an open hold (%s: %s); clear it first: hand hold clear %s", id, held.Kind, held.Reason, id), Code: 3}
			}
			t := history.Task
			proj, exists, err := project.Find(homeDir, t.Project)
			if err != nil {
				return err
			}
			if !exists {
				return &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
			}
			clonePath := filepath.Join(homeDir, "projects", proj.Name)
			if err := gatePreflight(cmd, proj, clonePath, skipGateCheck); err != nil {
				return err
			}

			briefRel := filepath.Join("data", id, "brief.md")
			briefAbs := filepath.Join(homeDir, briefRel)
			if _, err := os.Stat(briefAbs); err != nil {
				return &ExitError{Err: fmt.Errorf("brief not found at %s", briefRel), Code: 3}
			}
			harnessFromFlag := harnessName != ""
			if !harnessFromFlag {
				cfg, err := currentWorkerConfig(homeDir)
				if err != nil {
					return err
				}
				harnessName = cfg.harness
				if harnessName == "" {
					return &ExitError{Err: fmt.Errorf("current supervisor harness is unknown and no worker harness override is configured; run hand config set harness <name>"), Code: 3}
				}
			}
			if !harness.IsSupported(harnessName) {
				return usageValue(harnessFromFlag, fmt.Errorf("harness %q not recognized", harnessName))
			}
			var briefHasFrontMatter bool
			model, effort, briefHasFrontMatter, err = resolveTier(cmd, homeDir, briefAbs, harnessName, model, effort)
			if err != nil {
				return err
			}
			startedAt := time.Now().UTC().Format(time.RFC3339)
			attempt, err := state.ReopenTask(homeDir, state.Attempt{
				TaskID: id, Lifecycle: state.AttemptProvisioning, Harness: harnessName, Model: model, Effort: effort,
				CreatedAt: startedAt,
			})
			if err != nil {
				return asPrecondition(fmt.Errorf("write reopened provisioning state: %w", err))
			}

			releaseProject, err := state.Lock(homeDir, "project:"+proj.Name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", proj.Name, err)
			}
			defer releaseProject()
			lease, err := worktree.Get(clonePath, "hand:"+id)
			if err != nil {
				return fmt.Errorf("acquire treehouse worktree: %w", err)
			}
			wt := lease.Path
			if err := state.RecordAttemptWorktree(homeDir, id, attempt.ID, wt, lease.ID); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record worktree ownership: %w", err), worktree.Return(wt, true))
			}
			releaseWorktree, err := state.Lock(homeDir, "worktree:"+wt)
			if err != nil {
				return reportSpawnCleanup(fmt.Errorf("lock worktree %q: %w", wt, err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			defer releaseWorktree()
			if conflict, err := worktree.CheckCollision(homeDir, lease, id); err != nil {
				return reportSpawnCleanup(err, returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			} else if conflict != "" {
				return reportSpawnCleanup(&ExitError{Err: fmt.Errorf("worktree collision: %s already holds %s", conflict, wt), Code: 3}, returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}

			client := herdr.NewClient()
			ws, tab, pane, rollback, err := acquireTaskWorkspace(client, wt, id, proj.Name)
			if err != nil {
				return reportSpawnCleanup(err, returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			started := false
			herdrRecorded := false
			defer func() {
				if started {
					return
				}
				if closeErr := rollback(); closeErr != nil {
					err = reportSpawnCleanup(err, closeErr)
				} else if herdrRecorded {
					if clearErr := state.ClearAttemptHerdr(homeDir, id, attempt.ID); clearErr != nil {
						err = reportSpawnCleanup(err, clearErr)
					}
				}
			}()
			paneStartedAt := time.Now().UTC().Format(time.RFC3339)
			if err := state.RecordAttemptHerdr(homeDir, id, attempt.ID, state.Herdr{Session: "default", WorkspaceID: ws.WorkspaceID, TabID: tab.TabID, PaneID: pane.PaneID}, paneStartedAt); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record Herdr ownership: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			herdrRecorded = true

			launchCmd, err := harness.Build(harnessName, harness.Options{Worktree: wt, Brief: briefAbs, FleetHome: homeDir, Model: model, Effort: effort, BriefHasFrontMatter: briefHasFrontMatter})
			if err != nil {
				return reportSpawnCleanup(err, returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			if err := client.PaneRun(pane.PaneID, launchCmd); err != nil {
				return reportSpawnCleanup(fmt.Errorf("send launch command failed: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			if err := state.MarkLaunchSubmitted(homeDir, id, attempt.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record launch submission: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			if err := confirmLaunch(client, pane.PaneID, harnessName); err != nil {
				return reportSpawnCleanup(fmt.Errorf("confirm worker started: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			if err := state.MarkLaunchConfirmed(homeDir, id, attempt.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record launch confirmation: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			if err := state.MarkAttemptRunning(homeDir, id, attempt.ID); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record running attempt: %w", err), returnProvisioningWorktree(homeDir, id, attempt.ID, wt))
			}
			started = true

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "reopened")
			doc.Field("attempt", "new")
			doc.Field("project", t.Project)
			doc.Field("kind", string(t.Kind))
			doc.Field("harness", harnessName)
			doc.Field("worktree", wt)
			doc.Help("Run `hand status " + id + "` to read this attempt")
			return doc.Render(cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&harnessName, "harness", "", "agent harness to launch (default: config/harness, then the detected supervisor harness)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort level for harnesses that support it")
	cmd.Flags().BoolVar(&skipGateCheck, "skip-gate-check", false, "dispatch even if the no-mistakes gate is not initialized, the clone path is missing from disk, or that path is not a git repository")
	return cmd
}
