package commands

import "github.com/spf13/cobra"

func init() { Register(newMovieCmd) }

// newMovieCmd builds the `movie` group: the shared media surface plus the
// movie-specific endpoints (boxoffice, releases).
func newMovieCmd(app *App) *cobra.Command {
	c := mediaGroup(app, "movie", "movies", "Movie endpoints")
	c.AddCommand(app.getList("boxoffice", "Weekend box office top 10", "/movies/boxoffice", false))
	c.AddCommand(app.idSuffixCmd("releases", "Release dates", "/movies", "/releases", "country", "",
		"two-letter country code, optional", false))
	return c
}
