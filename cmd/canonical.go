package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func canonicalSupervisorPreflight(*cobra.Command, []string) error {
	if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
		return &ExitError{Err: fmt.Errorf("canonical workflow mutation is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
	}
	return nil
}

func runCanonicalInit(cmd *cobra.Command, homePath string) error {
	if err := canonicalSupervisorPreflight(cmd, nil); err != nil {
		return err
	}
	if err := refuseManagedTreeHome(homePath); err != nil {
		return err
	}
	id, err := store.InitializeCanonicalV19(cmd.Context(), homePath)
	if err != nil {
		return err
	}
	var doc axi.Doc
	doc.Field("result", "canonical-fleet")
	doc.Field("home", homePath)
	doc.Field("fleet_id", id)
	doc.Field("worker_execution", "unqualified")
	return doc.Render(cmd.OutOrStdout())
}

func newTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "task", Short: "Manage canonical Tasks",
		PersistentPreRunE: canonicalSupervisorPreflight,
	}
	var projectID, goal string
	create := &cobra.Command{
		Use: "create <id>", Short: "Record a goal under one exact canonical Project",
		Long: "Create one Task with a caller-retained ID. Reusing an ID refuses; it never creates a duplicate Task after a lost response.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			id := args[0]
			ordinal, err := store.CreateCanonicalV19Task(cmd.Context(), homeDir, store.CanonicalV19TaskCreateInput{
				ID: id, ProjectID: projectID, Goal: goal,
				GoalDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(goal))),
				CreatedAt:  time.Now().UTC().Format(time.RFC3339Nano),
			})
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("task_id", id)
			doc.Field("project_id", projectID)
			doc.Int("ordinal", int(ordinal))
			return doc.Render(cmd.OutOrStdout())
		},
	}
	create.Flags().StringVar(&projectID, "project-id", "", "Exact canonical Project ID")
	create.Flags().StringVar(&goal, "goal", "", "Immutable operator goal")
	cmd.AddCommand(create)
	return cmd
}

func newProjectRegisterCmd() *cobra.Command {
	return &cobra.Command{
		Use: "register <name>", Short: "Register an existing projects/<name> Git clone in a canonical Fleet",
		Long: "Register an existing direct clone and capture its exact Git/physical identity.\n" +
			"No clone, worker, or policy is created. Plan creation still requires an explicit PolicyRevision.",
		Args:              usageArgs(cobra.ExactArgs(1)),
		PersistentPreRunE: canonicalSupervisorPreflight,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return err
			}
			projectID, workspaceID, err := store.RegisterCanonicalV19Project(cmd.Context(), homeDir, args[0])
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("project_id", projectID)
			doc.Field("workspace_binding_id", workspaceID)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}
