package commands

import (
	"github.com/rlarsen/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newSeasonCmd) }

// newSeasonCmd builds the `season` group. All endpoints are scoped by --show
// and (except summary) --season.
func newSeasonCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "season", Short: "Season endpoints"}

	// summary: all seasons for a show — GET /shows/{id}/seasons.
	root.AddCommand(app.showScoped("summary", "All seasons for a show", "/seasons", false, false))

	root.AddCommand(app.showScoped("info", "Single season info", "/info", true, false))
	root.AddCommand(app.showScoped("episodes", "Episodes in a season", "", true, false))
	root.AddCommand(app.showScoped("people", "Season cast and crew", "/people", true, false))
	root.AddCommand(app.showScoped("ratings", "Season ratings", "/ratings", true, false))
	root.AddCommand(app.showScoped("stats", "Season stats", "/stats", true, false))
	root.AddCommand(app.showScoped("watching", "Users watching this season", "/watching", true, false))
	root.AddCommand(app.showScoped("videos", "Season videos", "/videos", true, false))

	root.AddCommand(app.seasonTrailing("comments", "Season comments", "/comments", "sort", "newest",
		"sort: newest|oldest|likes|replies"))
	root.AddCommand(app.seasonTrailing("translations", "Season translations", "/translations", "lang", "",
		"two-letter language code"))
	root.AddCommand(app.seasonTrailing("lists", "Lists containing this season", "/lists", "type", "personal",
		"list type: all|personal|official|watchlists"))

	root.AddCommand(app.seasonReport())

	return root
}

// seasonReport: POST /shows/{id}/seasons/{n}/report. Destructive (files a
// report against the item), so confirm-gated like the sync mutations.
func (a *App) seasonReport() *cobra.Command {
	var show, season string
	c := &cobra.Command{
		Use:   "report",
		Short: "Report a season (destructive; requires --confirm)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if show == "" {
				return output.NewError(output.CodeBadConfig, "missing required --show", output.ExitUser)
			}
			if season == "" {
				return output.NewError(output.CodeBadConfig, "missing required --season", output.ExitUser)
			}
			res, err := a.post("/shows/"+show+"/seasons/"+season+"/report", a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&show, "show", "", "show id")
	c.Flags().StringVar(&season, "season", "", "season number")
	return c
}

// showScoped builds a command scoped by --show and optionally --season. When
// needSeason, the path is /shows/{show}/seasons/{season}{suffix}; otherwise
// /shows/{show}{suffix}.
func (a *App) showScoped(use, short, suffix string, needSeason, auth bool) *cobra.Command {
	var show, season string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if show == "" {
				return output.NewError(output.CodeBadConfig, "missing required --show", output.ExitUser)
			}
			path := "/shows/" + show
			if needSeason {
				if season == "" {
					return output.NewError(output.CodeBadConfig, "missing required --season", output.ExitUser)
				}
				path += "/seasons/" + season + suffix
			} else {
				path += suffix
			}
			res, err := a.get(path, a.baseOpts(auth))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&show, "show", "", "show id")
	if needSeason {
		c.Flags().StringVar(&season, "season", "", "season number")
	}
	return c
}

// seasonTrailing builds a season command with one optional trailing path
// segment from a flag (e.g. comments --sort, translations --lang).
func (a *App) seasonTrailing(use, short, suffix, flagName, flagDefault, flagUsage string) *cobra.Command {
	var show, season, val string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if show == "" {
				return output.NewError(output.CodeBadConfig, "missing required --show", output.ExitUser)
			}
			if season == "" {
				return output.NewError(output.CodeBadConfig, "missing required --season", output.ExitUser)
			}
			path := "/shows/" + show + "/seasons/" + season + suffix
			if val != "" {
				path += "/" + val
			}
			res, err := a.get(path, a.baseOpts(false))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&show, "show", "", "show id")
	c.Flags().StringVar(&season, "season", "", "season number")
	c.Flags().StringVar(&val, flagName, flagDefault, flagUsage)
	return c
}
