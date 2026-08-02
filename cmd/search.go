package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/store"
	"github.com/spf13/cobra"
)

const searchDefaultLimit = 20

func newSearchCmd() *cobra.Command {
	var asJSON, rebuild bool
	var limit int

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search the prose corpus under data/",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := home.Resolve()
			if err != nil {
				return asPrecondition(err)
			}

			if rebuild {
				sync, err := store.Rebuild(homeDir)
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "rebuilt index: %d documents\n", sync.Total); err != nil {
					return err
				}
			}

			ix, err := store.OpenIndex(homeDir)
			if err != nil {
				return err
			}
			defer func() { _ = ix.Close() }()

			// Refresh before every query rather than on a schedule: the index is
			// derived, so a stale answer is a defect the corpus can always settle,
			// and paying for it here keeps every write path free of the index.
			if !rebuild {
				if _, err := ix.Refresh(); err != nil {
					return err
				}
			}

			query := strings.Join(args, " ")
			hits, err := ix.Search(query, limit)
			if err != nil {
				return &ExitError{Err: err, Code: 2}
			}

			// An empty stdout is the honest answer for a pipeline, but on its own
			// it cannot be told from a search that failed to run, so the "nothing
			// matched" goes to stderr where it costs the pipeline nothing.
			if len(hits) == 0 && !asJSON {
				_, err := fmt.Fprintf(cmd.ErrOrStderr(), "no matches for %q\n", query)
				return err
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if hits == nil {
					hits = []store.Hit{}
				}
				return enc.Encode(hits)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			for _, h := range hits {
				if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", h.Path, h.Title, h.Snippet); err != nil {
					return err
				}
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "discard and re-derive the index before searching")
	cmd.Flags().IntVar(&limit, "limit", searchDefaultLimit, "maximum number of hits")
	return cmd
}
