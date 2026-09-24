package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/harness"
	"github.com/atqamz/hand/internal/home"
	"github.com/atqamz/hand/internal/registry"
	"github.com/atqamz/hand/internal/store"
	"github.com/spf13/cobra"
)

func canonicalSupervisorPreflight(cmd *cobra.Command, _ []string) error {
	if os.Getenv(harness.RoleEnv) == harness.WorkerRole {
		return &ExitError{Err: fmt.Errorf("canonical workflow mutation is unavailable when %s=%s", harness.RoleEnv, harness.WorkerRole), Code: 3}
	}
	// Init has an explicit target; group help and exact historical reads must not
	// depend on an ambient Fleet or repair its discovery projection.
	if cmd.Name() == "init" || cmd.HasSubCommands() || cmd.CommandPath() == "hand decision show" || cmd.CommandPath() == "hand task hold show" || cmd.CommandPath() == "hand task show" {
		return nil
	}
	homeDir, err := home.Resolve()
	if err != nil {
		return err
	}
	// Canonical mutations skip legacy startup, but retain its duplicate-Fleet
	// guard. The read-only path never opens the canonical DB as a legacy writer.
	warnings, err := registry.Preflight(homeDir, true)
	if err != nil {
		return asPrecondition(err)
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return err
		}
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
	registryOutcome, registryErr := registerFleet(homePath, id)
	var doc axi.Doc
	doc.Field("result", "canonical-fleet")
	doc.Field("home", homePath)
	doc.Field("fleet_id", id)
	doc.Field("registry", registryOutcome)
	doc.Field("worker_execution", "unqualified")
	if err := doc.Render(cmd.OutOrStdout()); err != nil {
		return err
	}
	if registryErr != nil {
		return fmt.Errorf("canonical Fleet initialized at %s with fleet_id %s, but registry discovery update failed; retry init --canonical to repair discovery: %w", homePath, id, registryErr)
	}
	return nil
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
	cmd.AddCommand(create, newTaskHoldCmd(), newTaskArchiveCmd(), newTaskShowCmd())
	return cmd
}

func canonicalTimestamp(value string) error {
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("exact canonical timestamp must be RFC3339: %w", err)
	}
	return nil
}

func canonicalEvidenceDigest(value string) error {
	if len(value) != sha256.Size*2 || strings.Trim(value, "0123456789abcdef") != "" {
		return fmt.Errorf("--evidence-digest must be the exact lowercase SHA-256 of retained evidence")
	}
	return nil
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
