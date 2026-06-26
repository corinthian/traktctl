package commands

import "github.com/spf13/cobra"

// mediaGroup builds the command surface shared by the `movie` and `show`
// groups (spec: "show ... same shape as the movie group"). typ is the API path
// segment ("movies" or "shows"). Callers add type-specific subcommands to the
// returned command. This shared builder is the canonical table-fill pattern the
// per-group fan-out copies.
func mediaGroup(app *App, use, typ, short string) *cobra.Command {
	root := &cobra.Command{Use: use, Short: short}
	base := "/" + typ

	// Discovery lists (no id).
	root.AddCommand(app.getList("trending", "Trending "+typ, base+"/trending", false))
	root.AddCommand(app.getList("popular", "Popular "+typ, base+"/popular", false))
	root.AddCommand(app.getList("anticipated", "Most anticipated "+typ, base+"/anticipated", false))

	// Period-scoped popularity lists.
	root.AddCommand(app.getPeriod("favorited", "Most favorited", base+"/favorited", "weekly", false))
	root.AddCommand(app.getPeriod("played", "Most played", base+"/played", "weekly", false))
	root.AddCommand(app.getPeriod("watched", "Most watched", base+"/watched", "weekly", false))
	root.AddCommand(app.getPeriod("collected", "Most collected", base+"/collected", "weekly", false))
	root.AddCommand(app.getPeriod("streaming", "Most watched on streaming", base+"/streaming", "weekly", false))

	// Update feeds.
	root.AddCommand(app.getStart("updates", "Recently updated "+typ, base+"/updates", false))
	root.AddCommand(app.getStart("updated-ids", "Recently updated "+typ+" (ids only)", base+"/updates/id", false))

	// Single-item reads.
	root.AddCommand(app.getByID("get", "Get a single "+singular(typ), base, "", false))
	root.AddCommand(app.getByID("aliases", "Title aliases", base, "/aliases", false))
	root.AddCommand(app.getByID("translations", "Translations", base, "/translations", false))
	root.AddCommand(app.getByID("people", "Cast and crew", base, "/people", false))
	root.AddCommand(app.getByID("ratings", "Aggregate ratings", base, "/ratings", false))
	root.AddCommand(app.getByID("related", "Related "+typ, base, "/related", false))
	root.AddCommand(app.getByID("stats", "Stats", base, "/stats", false))
	root.AddCommand(app.getByID("sentiments", "Sentiment analysis", base, "/sentiments", false))
	root.AddCommand(app.getByID("studios", "Studios", base, "/studios", false))
	root.AddCommand(app.getByID("watching", "Users currently watching", base, "/watching", false))
	root.AddCommand(app.getByID("videos", "Trailers and clips", base, "/videos", false))
	root.AddCommand(app.idSuffixCmd("comments", "Comments", base, "/comments", "sort", "newest",
		"sort: newest|oldest|likes|replies|highest|lowest|plays", false))
	root.AddCommand(app.idSuffixCmd("lists", "Lists containing this "+singular(typ), base, "/lists", "type", "personal",
		"list type: all|personal|official|watchlists|recommendations", false))

	return root
}

// singular trims a trailing "s" for help text.
func singular(typ string) string {
	if len(typ) > 1 && typ[len(typ)-1] == 's' {
		return typ[:len(typ)-1]
	}
	return typ
}
