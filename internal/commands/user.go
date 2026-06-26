package commands

import (
	"net/url"

	"github.com/spf13/cobra"
)

func init() { Register(newUserCmd) }

// userTarget resolves the target user id: --user flag, else config default_user,
// else "me".
func (a *App) userTarget(flagUser string) string {
	if flagUser != "" {
		return flagUser
	}
	if a.Cfg.DefaultUser != "" {
		return a.Cfg.DefaultUser
	}
	return "me"
}

// newUserCmd builds the `user` group: profile and personal-data reads. The
// bearer is attached when present so private profiles (incl. "me") resolve.
func newUserCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "user", Short: "User profile and personal data"}

	// settings — requires auth (no target id).
	root.AddCommand(&cobra.Command{
		Use:   "settings",
		Short: "Account settings (auth required)",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := app.get("/users/settings", app.baseOpts(true))
			if err != nil {
				return err
			}
			return app.emit(res, "")
		},
	})

	root.AddCommand(app.userIDCmd("profile", "User profile", ""))
	root.AddCommand(app.userIDCmd("stats", "Watch statistics", "/stats"))
	root.AddCommand(app.userIDCmd("watching", "What the user is watching now", "/watching"))
	root.AddCommand(app.userIDCmd("followers", "Followers", "/followers"))
	root.AddCommand(app.userIDCmd("following", "Following", "/following"))
	root.AddCommand(app.userIDCmd("friends", "Friends", "/friends"))
	root.AddCommand(app.userIDCmd("lists", "User's personal lists", "/lists"))

	root.AddCommand(app.userTypeCmd("collection", "Collected items", "/collection", true))
	root.AddCommand(app.userTypeCmd("watched", "Watched items", "/watched", true))

	root.AddCommand(app.userSortableCmd("watchlist", "User's watchlist", "watchlist"))
	root.AddCommand(app.userSortableCmd("favorites", "User's favorites", "favorites"))

	root.AddCommand(app.userHistoryCmd())
	root.AddCommand(app.userRatingsCmd())

	return root
}

// userIDCmd: GET /users/{id}{suffix}, with --user.
func (a *App) userIDCmd(use, short, suffix string) *cobra.Command {
	var user string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.get("/users/"+a.userTarget(user)+suffix, a.baseOpts(false))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username (default: config default_user or 'me')")
	return c
}

// userTypeCmd: GET /users/{id}{base}/{type}. type is required when typeRequired.
func (a *App) userTypeCmd(use, short, base string, typeRequired bool) *cobra.Command {
	var user, typ string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + base
			if typ != "" {
				path += "/" + typ
			}
			res, err := a.get(path, a.baseOpts(false))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	return c
}

// userSortableCmd: GET /users/{id}/{base}[/{type}[/{sort_by}/{sort_how}]].
func (a *App) userSortableCmd(use, short, base string) *cobra.Command {
	var user, typ, sortBy, sortHow string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + "/" + base
			if typ != "" {
				path += "/" + typ
				if sortBy != "" && sortHow != "" {
					path += "/" + sortBy + "/" + sortHow
				}
			}
			res, err := a.get(path, a.baseOpts(false))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	c.Flags().StringVar(&sortBy, "sort-by", "", "rank|added|released|title")
	c.Flags().StringVar(&sortHow, "sort-how", "", "asc|desc")
	return c
}

// userHistoryCmd: GET /users/{id}/history[/{type}[/{item_id}]] with date range
// and the --recent N convenience (limit N, page 1).
func (a *App) userHistoryCmd() *cobra.Command {
	var user, typ, itemID, startAt, endAt string
	var recent int
	c := &cobra.Command{
		Use:   "history",
		Short: "Watch history",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + "/history"
			if typ != "" {
				path += "/" + typ
				if itemID != "" {
					path += "/" + itemID
				}
			}
			opts := a.baseOpts(false)
			q := url.Values{}
			if startAt != "" {
				q.Set("start_at", startAt)
			}
			if endAt != "" {
				q.Set("end_at", endAt)
			}
			if recent > 0 {
				opts.Page = 1
				opts.Limit = recent
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
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	c.Flags().StringVar(&itemID, "item-id", "", "restrict to a single item id")
	c.Flags().StringVar(&startAt, "start-at", "", "ISO8601 lower bound")
	c.Flags().StringVar(&endAt, "end-at", "", "ISO8601 upper bound")
	c.Flags().IntVar(&recent, "recent", 0, "shorthand for --limit N --page 1")
	return c
}

// userRatingsCmd: GET /users/{id}/ratings[/{type}[/{rating}]].
func (a *App) userRatingsCmd() *cobra.Command {
	var user, typ, rating string
	c := &cobra.Command{
		Use:   "ratings",
		Short: "User ratings",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + "/ratings"
			if typ != "" {
				path += "/" + typ
				if rating != "" {
					path += "/" + rating
				}
			}
			res, err := a.get(path, a.baseOpts(false))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	c.Flags().StringVar(&rating, "rating", "", "filter by rating 1-10")
	return c
}
