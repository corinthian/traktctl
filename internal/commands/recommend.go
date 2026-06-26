package commands

import (
	"net/http"
	"net/url"

	"github.com/spf13/cobra"
)

func init() { Register(newRecommendCmd) }

// newRecommendCmd builds the `recommend` group: personalized movie/show
// recommendations (auth) and hide mutations.
func newRecommendCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "recommend", Short: "Personalized recommendations"}

	root.AddCommand(app.recommendList("movies", "Recommended movies", "/recommendations/movies"))
	root.AddCommand(app.recommendList("shows", "Recommended shows", "/recommendations/shows"))

	root.AddCommand(app.recommendHide("hide-movie", "Stop recommending a movie", "/recommendations/movies"))
	root.AddCommand(app.recommendHide("hide-show", "Stop recommending a show", "/recommendations/shows"))

	return root
}

// recommendList: GET <path> with --ignore-collected / --ignore-watchlisted.
func (a *App) recommendList(use, short, path string) *cobra.Command {
	var ignoreCollected, ignoreWatchlisted bool
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := a.baseOpts(true)
			q := url.Values{}
			if ignoreCollected {
				q.Set("ignore_collected", "true")
			}
			if ignoreWatchlisted {
				q.Set("ignore_watchlisted", "true")
			}
			if len(q) > 0 {
				opts.Query = q
			}
			res, err := a.get(path, opts)
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().BoolVar(&ignoreCollected, "ignore-collected", false, "exclude collected items")
	c.Flags().BoolVar(&ignoreWatchlisted, "ignore-watchlisted", false, "exclude watchlisted items")
	return c
}

// recommendHide: DELETE <prefix>/{id} (auth). Idempotent, so no --confirm gate.
func (a *App) recommendHide(use, short, prefix string) *cobra.Command {
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.requireID()
			if err != nil {
				return err
			}
			res, cerr := a.Client.Do(a.ctx(), http.MethodDelete, prefix+"/"+id, a.baseOpts(true))
			if cerr != nil {
				return cerr
			}
			if len(res.Data) == 0 {
				payload, _ := jsonRaw(map[string]bool{"hidden": true})
				res.Data = payload
			}
			return a.emit(res, "")
		},
	}
	return c
}
