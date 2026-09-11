package commands

import "github.com/spf13/cobra"

func init() { Register(newPersonCmd) }

// newPersonCmd builds the `person` group: person detail plus filmography and
// list-membership reads. All four commands are id-scoped GETs built on the
// same getByID helper the movie/show groups use for their single-item reads
// — global --id/--id-type resolve the person (trakt|slug|imdb; tmdb/tvdb are
// rejected by requireLookupID the same as everywhere else) and --extended
// passes through unchanged. Verified live 2026-09-11: all four paths return
// 200 with a bare id, no --type/query segment needed on /people/{id}/lists.
func newPersonCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "person", Short: "People: detail, filmography, and list membership"}
	root.AddCommand(app.getByID("get", "Get a single person", "/people", "", false))
	root.AddCommand(app.getByID("movies", "Filmography: movie credits (cast/crew)", "/people", "/movies", false))
	root.AddCommand(app.getByID("shows", "Filmography: show credits (cast/crew)", "/people", "/shows", false))
	root.AddCommand(app.getByID("lists", "Lists containing this person", "/people", "/lists", false))
	return root
}
