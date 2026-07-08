package commands

import (
	"net/url"

	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newSearchCmd) }

// newSearchCmd builds the `search` group: text query and lookup-by-id.
func newSearchCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "search", Short: "Search Trakt by text or external id"}

	var typ, q, field string
	query := &cobra.Command{
		Use:   "query",
		Short: "Text search: GET /search/{type}?query=...",
		RunE: func(cmd *cobra.Command, args []string) error {
			if typ == "" {
				return output.NewError(output.CodeBadConfig, "missing required --type (movie|show|episode|person|list)", output.ExitUser)
			}
			if q == "" {
				return output.NewError(output.CodeBadConfig, "missing required --q (search text)", output.ExitUser)
			}
			opts := app.baseOpts(false)
			opts.Query = url.Values{"query": {q}}
			if field != "" {
				opts.Query.Set("fields", field)
			}
			res, err := app.get("/search/"+typ, opts)
			if err != nil {
				return err
			}
			return app.emit(res, "")
		},
	}
	query.Flags().StringVar(&typ, "type", "", "movie|show|episode|person|list (comma-separated allowed)")
	query.Flags().StringVar(&q, "q", "", "search text")
	query.Flags().StringVar(&field, "field", "", "restrict to fields, e.g. title,aliases")
	root.AddCommand(query)

	var idResultType string
	byID := &cobra.Command{
		Use:   "id",
		Short: "Lookup by external id: GET /search/{id-type}/{id}",
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := app.requireID()
			if err != nil {
				return err
			}
			opts := app.baseOpts(false)
			if idResultType != "" {
				opts.Query = url.Values{"type": {idResultType}}
			}
			res, gerr := app.get("/search/"+app.Flags.IDType+"/"+id, opts)
			if gerr != nil {
				return gerr
			}
			return app.emit(res, "")
		},
	}
	byID.Flags().StringVar(&idResultType, "type", "", "restrict result type: movie|show|episode|person")
	root.AddCommand(byID)

	return root
}
