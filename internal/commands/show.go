package commands

import "github.com/spf13/cobra"

func init() { Register(newShowCmd) }

// newShowCmd builds the `show` group: the shared media surface plus
// show-specific endpoints (certifications, next/last episode, watch progress).
func newShowCmd(app *App) *cobra.Command {
	c := mediaGroup(app, "show", "shows", "Show endpoints")
	c.AddCommand(app.getByID("certifications", "Content certifications", "/shows", "/certifications", false))
	c.AddCommand(app.getByID("next-episode", "Next scheduled episode (204 if none)", "/shows", "/next_episode", false))
	c.AddCommand(app.getByID("last-episode", "Most recently aired episode", "/shows", "/last_episode", false))

	prog := &cobra.Command{Use: "progress", Short: "Watched/collected progress (auth)"}
	prog.AddCommand(app.getByID("collection", "Collection progress", "/shows", "/progress/collection", true))
	prog.AddCommand(app.getByID("watched", "Watched progress", "/shows", "/progress/watched", true))
	c.AddCommand(prog)
	return c
}
