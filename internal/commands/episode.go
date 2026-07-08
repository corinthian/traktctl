package commands

import (
	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newEpisodeCmd) }

// newEpisodeCmd builds the `episode` group, scoped by --show, --season, and
// --episode.
func newEpisodeCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "episode", Short: "Episode endpoints"}

	root.AddCommand(app.episodeScoped("summary", "Single episode summary", "", false))
	root.AddCommand(app.episodeScoped("people", "Episode cast and crew", "/people", false))
	root.AddCommand(app.episodeScoped("ratings", "Episode ratings", "/ratings", false))
	root.AddCommand(app.episodeScoped("stats", "Episode stats", "/stats", false))
	root.AddCommand(app.episodeScoped("watching", "Users watching this episode", "/watching", false))
	root.AddCommand(app.episodeScoped("videos", "Episode videos", "/videos", false))

	root.AddCommand(app.episodeTrailing("comments", "Episode comments", "/comments", "sort", "newest",
		"sort: newest|oldest|likes|replies"))
	root.AddCommand(app.episodeTrailing("translations", "Episode translations", "/translations", "lang", "",
		"two-letter language code"))
	root.AddCommand(app.episodeTrailing("lists", "Lists with this episode", "/lists", "type", "personal",
		"list type: all|personal|official|watchlists"))

	return root
}

// episodeBase validates the three scope flags and returns the path prefix.
func episodeBase(show, season, episode string) (string, error) {
	if show == "" {
		return "", output.NewError(output.CodeBadConfig, "missing required --show", output.ExitUser)
	}
	if season == "" {
		return "", output.NewError(output.CodeBadConfig, "missing required --season", output.ExitUser)
	}
	if episode == "" {
		return "", output.NewError(output.CodeBadConfig, "missing required --episode", output.ExitUser)
	}
	return "/shows/" + show + "/seasons/" + season + "/episodes/" + episode, nil
}

func (a *App) episodeScoped(use, short, suffix string, auth bool) *cobra.Command {
	var show, season, episode string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := episodeBase(show, season, episode)
			if err != nil {
				return err
			}
			res, gerr := a.get(base+suffix, a.baseOpts(auth))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindEpisodeFlags(c, &show, &season, &episode)
	return c
}

func (a *App) episodeTrailing(use, short, suffix, flagName, flagDefault, flagUsage string) *cobra.Command {
	var show, season, episode, val string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := episodeBase(show, season, episode)
			if err != nil {
				return err
			}
			path := base + suffix
			if val != "" {
				path += "/" + val
			}
			res, gerr := a.get(path, a.baseOpts(false))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindEpisodeFlags(c, &show, &season, &episode)
	c.Flags().StringVar(&val, flagName, flagDefault, flagUsage)
	return c
}

func bindEpisodeFlags(c *cobra.Command, show, season, episode *string) {
	c.Flags().StringVar(show, "show", "", "show id")
	c.Flags().StringVar(season, "season", "", "season number")
	c.Flags().StringVar(episode, "episode", "", "episode number")
}
