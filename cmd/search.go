package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atqamz/secondhand/internal/axi"
	"github.com/atqamz/secondhand/internal/home"
	"github.com/atqamz/secondhand/internal/store"
	"github.com/spf13/cobra"
)

const searchDefaultLimit = 20

var searchFields = []axi.Column[store.Hit]{
	{Name: "path", Value: func(h store.Hit) string { return h.Path }},
	{Name: "title", Value: func(h store.Hit) string { return h.Title }},
	{Name: "snippet", Value: func(h store.Hit) string { return h.Snippet }},
}

var searchDefaultFields = []string{"path", "title", "snippet"}

func newSearchCmd() *cobra.Command {
	var asJSON, rebuild bool
	var limit int
	var fields []string

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Full-text search the prose corpus under data/",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := rejectFieldsWithJSON(fields, asJSON); err != nil {
				return err
			}
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

			cols, err := pickFields(searchFields, fields, searchDefaultFields)
			if err != nil {
				return err
			}

			var doc axi.Doc
			doc.Field("query", query)
			doc.Int("count", len(hits))
			axi.Table(&doc, "hits", hits, cols)
			doc.Help(searchHelp(query, len(hits), limit)...)
			return doc.Render(cmd.OutOrStdout())
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON instead of TOON")
	cmd.Flags().BoolVar(&rebuild, "rebuild", false, "discard and re-derive the index before searching")
	cmd.Flags().IntVar(&limit, "limit", searchDefaultLimit, "maximum number of hits")
	cmd.Flags().StringSliceVar(&fields, "fields", nil, fieldsFlagUsage(searchFields, searchDefaultFields))
	return cmd
}

// A query that matched nothing and a corpus the index never caught up with
// look identical from the outside, so the empty answer names the one flag that
// tells them apart.
func searchHelp(query string, hits, limit int) []string {
	if hits == 0 {
		return []string{
			"Every token has to match: drop one to widen the query",
			"Run `hand search --rebuild " + query + "` if the corpus changed but the index did not",
		}
	}
	help := []string{"Read a hit's path for the whole document; the snippet is a window, not the match in full"}
	if hits == limit {
		help = append(help, fmt.Sprintf("This is the --limit cap, so there may be more: run `hand search --limit %d %s`", limit*2, query))
	}
	return help
}
