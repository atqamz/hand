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

func newPromoteCmd() *cobra.Command {
	var harnessName string
	var model string
	var effort string
	var skipGateCheck bool

	cmd := &cobra.Command{
		Use:   "promote <id>",
		Short: "Promote a completed scout task into a ship task",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			id := args[0]
			home, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			release, err := state.Lock(home, "task:"+id)
			if err != nil {
				return fmt.Errorf("lock task %q: %w", id, err)
			}
			defer release()

			history, err := state.ReadHistory(home, id)
			if err != nil {
				return asPrecondition(err)
			}
			if history.ActiveAttempt == nil {
				return &ExitError{Err: fmt.Errorf("task %q has no active scout attempt", id), Code: 3}
			}
			t := history.Task
			active := *history.ActiveAttempt
			if t.Kind != state.KindScout {
				return &ExitError{Err: fmt.Errorf("task %q is not a scout", id), Code: 3}
			}

			reportRel := filepath.Join("data", id, "report.md")
			if _, err := os.Stat(filepath.Join(home, reportRel)); err != nil {
				return &ExitError{Err: fmt.Errorf("scout report not found at %s", reportRel), Code: 3}
			}

			client := herdr.NewClient()
			if s := herdr.Status(paneAgentStatus(client, active.Herdr.PaneID)); !s.NotBusy() && s != herdr.StatusUnknown {
				return &ExitError{Err: fmt.Errorf("task %q is not a completed scout (agent state: %s)", id, s), Code: 3}
			}

			briefRel := filepath.Join("data", id, "brief.md")
			briefAbs := filepath.Join(home, briefRel)
			if _, err := os.Stat(briefAbs); err != nil {
				return &ExitError{Err: fmt.Errorf("brief not found at %s", briefRel), Code: 3}
			}

			proj, exists, err := project.Find(home, t.Project)
			if err != nil {
				return err
			}
			if !exists {
				return &ExitError{Err: fmt.Errorf("project %q not registered", t.Project), Code: 3}
			}

			clonePath := filepath.Join(home, "projects", proj.Name)
			if err := gatePreflight(cmd, proj, clonePath, skipGateCheck); err != nil {
				return err
			}

			harnessFromFlag := harnessName != ""
			if !harnessFromFlag {
				cfg, err := currentWorkerConfig(home)
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
			model, effort, briefHasFrontMatter, err = resolveTier(cmd, home, briefAbs, harnessName, model, effort)
			if err != nil {
				return err
			}
			promotedAt := time.Now().UTC().Format(time.RFC3339)
			shipAttempt, err := state.PromoteTask(home, id, active.ID, active.Lifecycle, state.Attempt{
				TaskID: id, Lifecycle: state.AttemptProvisioning, Harness: harnessName, Model: model, Effort: effort,
				CreatedAt: promotedAt,
			})
			if err != nil {
				return asPrecondition(fmt.Errorf("write promoted provisioning state: %w", err))
			}

			releaseProject, err := state.Lock(home, "project:"+proj.Name)
			if err != nil {
				return fmt.Errorf("lock project %q: %w", proj.Name, err)
			}
			defer releaseProject()

			oldWorktree := active.Worktree
			oldWorkspaceID := active.Herdr.WorkspaceID
			oldTabID := active.Herdr.TabID
			// The scout Attempt is terminal from the promotion above, so its tab and worktree are this
			// command's to release however the ship Attempt ends: leaving them to the success path leaks
			// a leased slot and a pane that nothing points at on every later failure.
			defer func() {
				if closeErr := closeTaskTab(client, oldWorkspaceID, oldTabID); closeErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: herdr tab close failed: %v\n", closeErr)
				}
				if returnErr := worktree.Return(oldWorktree, true); returnErr != nil {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: return scout worktree failed: %v\n", returnErr)
				}
			}()

			lease, err := worktree.Get(clonePath, "hand:"+id)
			if err != nil {
				return fmt.Errorf("acquire treehouse worktree: %w", err)
			}
			wt := lease.Path
			if err := state.RecordAttemptWorktree(home, id, shipAttempt.ID, wt, lease.ID); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record worktree ownership: %w", err), worktree.Return(wt, true))
			}
			releaseWorktree, err := state.Lock(home, "worktree:"+wt)
			if err != nil {
				return reportSpawnCleanup(fmt.Errorf("lock worktree %q: %w", wt, err), worktree.Return(wt, true))
			}
			defer releaseWorktree()

			if conflict, err := worktree.CheckCollision(home, lease, id); err != nil {
				return reportSpawnCleanup(err, worktree.Return(wt, true))
			} else if conflict != "" {
				return reportSpawnCleanup(&ExitError{Err: fmt.Errorf("worktree collision: %s already holds %s", conflict, wt), Code: 3}, worktree.Return(wt, true))
			}

			ws, tab, pane, rollback, err := acquireTaskWorkspace(client, wt, id, proj.Name)
			if err != nil {
				return reportSpawnCleanup(err, worktree.Return(wt, true))
			}
			promoted := false
			defer func() {
				if promoted {
					return
				}
				if closeErr := rollback(); closeErr != nil {
					err = reportSpawnCleanup(err, closeErr)
				}
			}()
			paneStartedAt := time.Now().UTC().Format(time.RFC3339)
			if err := state.RecordAttemptHerdr(home, id, shipAttempt.ID, state.Herdr{Session: "default", WorkspaceID: ws.WorkspaceID, TabID: tab.TabID, PaneID: pane.PaneID}, paneStartedAt); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record Herdr ownership: %w", err), worktree.Return(wt, true))
			}

			// Provisioning evidence is durable before launch; rollback still releases resources when
			// this call fails, while the partial Attempt remains truthful for later inspection.
			launchCmd, err := harness.Build(harnessName, harness.Options{
				Worktree:            wt,
				Brief:               briefAbs,
				FleetHome:           home,
				Model:               model,
				Effort:              effort,
				BriefHasFrontMatter: briefHasFrontMatter,
			})
			if err != nil {
				return reportSpawnCleanup(err, worktree.Return(wt, true))
			}

			if err := client.PaneRun(pane.PaneID, launchCmd); err != nil {
				return reportSpawnCleanup(fmt.Errorf("send launch command failed: %w", err), worktree.Return(wt, true))
			}
			if err := state.MarkLaunchSubmitted(home, id, shipAttempt.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record launch submission: %w", err), worktree.Return(wt, true))
			}

			if err := confirmLaunch(client, pane.PaneID, harnessName); err != nil {
				return reportSpawnCleanup(fmt.Errorf("confirm worker started: %w", err), worktree.Return(wt, true))
			}
			if err := state.MarkLaunchConfirmed(home, id, shipAttempt.ID, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record launch confirmation: %w", err), worktree.Return(wt, true))
			}
			if err := state.MarkAttemptRunning(home, id, shipAttempt.ID); err != nil {
				return reportSpawnCleanup(fmt.Errorf("record running attempt: %w", err), worktree.Return(wt, true))
			}
			promoted = true
			if err := state.ClearHoldIfKind(home, id, state.HoldKindLimit); err != nil {
				if _, printErr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: clear usage-limit hold failed: %v\n", err); printErr != nil {
					return printErr
				}
			}

			var doc axi.Doc
			doc.Field("id", id)
			doc.Field("result", "promoted")
			doc.Field("kind", string(state.KindShip))
			doc.Field("was", string(state.KindScout))
			doc.Field("project", proj.Name)
			doc.Field("harness", harnessName)
			doc.Field("worktree", wt)
			doc.Help("The scout's worktree and pane are gone; run `hand status "+id+"` to read the ship worker",
				"The scout's delivery no longer counts for this task, so `hand deliver "+id+"` runs again on the code")
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().StringVar(&harnessName, "harness", "", "harness for the new ship worker (default: config/harness, then the detected supervisor harness)")
	cmd.Flags().StringVar(&model, "model", "", "model override for harnesses that support it")
	cmd.Flags().StringVar(&effort, "effort", "", "effort override for harnesses that support it")
	cmd.Flags().BoolVar(&skipGateCheck, "skip-gate-check", false, "dispatch even if the no-mistakes gate is not initialized, the clone path is missing from disk, or that path is not a git repository")
	return cmd
}
