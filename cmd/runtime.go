package cmd

import (
	"fmt"

	"github.com/atqamz/hand/internal/axi"
	"github.com/atqamz/hand/internal/toolchain"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Inspect and repair the private core runtime",
		Args:  usageArgs(cobra.NoArgs),
	}
	cmd.AddCommand(newRuntimeStatusCmd(), newRuntimeEnsureCmd())
	return cmd
}

func newRuntimeStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report the selected private runtime without changing it",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := toolchain.DefaultStore()
			if err != nil {
				return err
			}
			status, err := store.Status("", "")
			if err != nil {
				return err
			}
			var doc axi.Doc
			doc.Field("target", status.Target)
			doc.Field("runtime_id", status.RuntimeID)
			doc.Bool("ready", status.Ready)
			doc.Field("bundle", valueOrNone(status.BundleDir))
			doc.Field("git", valueOrNone(status.GitPath))
			doc.Field("git_version", valueOrNone(status.GitVersion))
			doc.Bool("git_https_ready", status.GitHTTPSReady)
			doc.Field("treehouse", valueOrNone(status.TreehousePath))
			doc.Field("treehouse_version", valueOrNone(status.TreehouseVersion))
			doc.Field("herdr", valueOrNone(status.HerdrPath))
			doc.Field("herdr_version", valueOrNone(status.HerdrVersion))
			doc.Field("reason", valueOrNone(status.Reason))
			doc.Help(runtimeStatusHelp(status)...)
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

func newRuntimeEnsureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure",
		Short: "Install or repair the exact private core runtime",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := toolchain.DefaultStore()
			if err != nil {
				return err
			}
			runtime, err := store.Ensure(cmd.Context(), "", "")
			if err != nil {
				return fmt.Errorf("ensure private Secondhand runtime: %w; run `hand runtime status` for diagnostics", err)
			}
			httpsReady := runtime.SupportsGitTransport("https")
			var doc axi.Doc
			doc.Field("result", "ready")
			doc.Field("runtime_id", runtime.ID)
			doc.Field("target", runtime.Target)
			doc.Field("bundle", runtime.BundleDir)
			doc.Field("git", runtime.GitPath)
			doc.Field("git_version", runtime.GitVersion)
			doc.Bool("git_https_ready", httpsReady)
			doc.Field("treehouse", runtime.TreehousePath)
			doc.Field("treehouse_version", runtime.TreehouseVersion)
			doc.Field("herdr", runtime.HerdrPath)
			doc.Field("herdr_version", runtime.HerdrVersion)
			if !httpsReady {
				doc.Help(gitHTTPSTreatment("https"))
			}
			return doc.Render(cmd.OutOrStdout())
		},
	}
}

// Keeps the generic runtime-repair line first and appends the https transport gap only when the
// bundle is otherwise intact - `hand runtime ensure` reinstalls the same helper-less Git and
// would not help there.
func runtimeStatusHelp(status toolchain.Status) []string {
	help := []string{"Run `hand runtime ensure` to install or repair the exact private Git, Treehouse, and Herdr bundle"}
	if status.Ready && !status.GitHTTPSReady {
		help = append(help, gitHTTPSTreatment("https"))
	}
	return help
}

// The one sentence every surface (runtime status, doctor, project add) uses for a pinned Git
// that cannot clone a given scheme, so the diagnosis and the way out cannot drift apart (hand#440).
func gitHTTPSTreatment(scheme string) string {
	return fmt.Sprintf("the pinned runtime's Git has no %s transport helper (git-remote-%s); use the ssh remote form instead, e.g. `hand project add git@host:owner/repo.git`", scheme, scheme)
}

func valueOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

func renderRuntimeWarnings(cmd *cobra.Command, warnings []string) error {
	for _, warning := range warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return err
		}
	}
	return nil
}
