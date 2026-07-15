package commands

import (
	"net/http"
	"net/url"

	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newSyncCmd) }

// newSyncCmd builds the `sync` group — personal data reads and mutations.
// Adds are idempotent at Trakt's layer (no --confirm). Removes and the
// settings/reorder/update-item mutations are destructive and require --confirm
// (or TRAKTCTL_CONFIRM=1).
func newSyncCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "sync", Short: "User sync: personal data reads and mutations"}

	// Cheap polling cursor.
	root.AddCommand(&cobra.Command{
		Use:   "activities",
		Short: "Last-activity timestamps (cheap change-detection)",
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := app.get("/sync/last_activities", app.baseOpts(true))
			if err != nil {
				return err
			}
			return app.emit(res, "")
		},
	})

	root.AddCommand(app.syncCollection())
	root.AddCommand(app.syncWatched())
	root.AddCommand(app.syncHistory())
	root.AddCommand(app.syncRatings())
	root.AddCommand(app.syncSortable("watchlist"))
	root.AddCommand(app.syncSortable("favorites"))
	root.AddCommand(app.syncPlayback())

	return root
}

// syncTypeGet: GET /sync/{seg}/{type} (type appended when set).
func (a *App) syncTypeGet(seg string) *cobra.Command {
	var typ string
	c := &cobra.Command{
		Use:   "get",
		Short: "Get " + seg + " items",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/sync/" + seg
			if typ != "" {
				path += "/" + typ
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	return c
}

func (a *App) syncCollection() *cobra.Command {
	c := &cobra.Command{Use: "collection", Short: "Collected items"}
	c.AddCommand(a.syncTypeGet("collection"))
	c.AddCommand(a.postCmd("add", "Add to collection (idempotent)", "/sync/collection", false))
	c.AddCommand(a.postCmd("remove", "Remove from collection", "/sync/collection/remove", true))
	return c
}

func (a *App) syncWatched() *cobra.Command {
	c := &cobra.Command{Use: "watched", Short: "Fully-watched items"}
	c.AddCommand(a.syncTypeGet("watched"))
	return c
}

func (a *App) syncHistory() *cobra.Command {
	c := &cobra.Command{Use: "history", Short: "Watch history"}
	var typ, id, startAt, endAt string
	get := &cobra.Command{
		Use:   "get",
		Short: "Get watch history",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/sync/history"
			if typ != "" {
				path += "/" + typ
				if id != "" {
					path += "/" + id
				}
			}
			opts := a.baseOpts(true)
			q := url.Values{}
			if startAt != "" {
				q.Set("start_at", startAt)
			}
			if endAt != "" {
				q.Set("end_at", endAt)
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
	get.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	get.Flags().StringVar(&id, "id", "", "restrict to a single Trakt item id")
	get.Flags().StringVar(&startAt, "start-at", "", "ISO8601 lower bound")
	get.Flags().StringVar(&endAt, "end-at", "", "ISO8601 upper bound")
	c.AddCommand(get)
	c.AddCommand(a.postCmd("add", "Add to history (idempotent)", "/sync/history", false))
	c.AddCommand(a.postCmd("remove", "Remove from history", "/sync/history/remove", true))
	return c
}

func (a *App) syncRatings() *cobra.Command {
	c := &cobra.Command{Use: "ratings", Short: "Ratings"}
	var typ, rating string
	get := &cobra.Command{
		Use:   "get",
		Short: "Get ratings",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/sync/ratings"
			if typ != "" {
				path += "/" + typ
				if rating != "" {
					path += "/" + rating
				}
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	get.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	get.Flags().StringVar(&rating, "rating", "", "filter by rating 1-10")
	c.AddCommand(get)
	c.AddCommand(a.postCmd("add", "Add ratings (idempotent)", "/sync/ratings", false))
	c.AddCommand(a.postCmd("remove", "Remove ratings", "/sync/ratings/remove", true))
	return c
}

// syncSortable builds watchlist/favorites: get (with sort), add, remove, and the
// destructive settings/reorder/update-item mutations.
func (a *App) syncSortable(seg string) *cobra.Command {
	c := &cobra.Command{Use: seg, Short: seg + " items and management"}
	var typ, sortBy, sortHow string
	get := &cobra.Command{
		Use:   "get",
		Short: "Get " + seg,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/sync/" + seg
			if typ != "" {
				path += "/" + typ
				if sortBy != "" && sortHow != "" {
					path += "/" + sortBy + "/" + sortHow
				}
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	get.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes")
	get.Flags().StringVar(&sortBy, "sort-by", "", "rank|added|released|title")
	get.Flags().StringVar(&sortHow, "sort-how", "", "asc|desc")
	c.AddCommand(get)
	c.AddCommand(a.postCmd("add", "Add to "+seg+" (idempotent)", "/sync/"+seg, false))
	c.AddCommand(a.postCmd("remove", "Remove from "+seg, "/sync/"+seg+"/remove", true))
	c.AddCommand(a.putCmd("settings", "Update "+seg+" settings", "/sync/"+seg, true))
	c.AddCommand(a.postCmd("reorder", "Reorder "+seg, "/sync/"+seg+"/reorder", true))
	c.AddCommand(a.putItemCmd("update-item", "Update a "+seg+" item", "/sync/"+seg))
	return c
}

func (a *App) syncPlayback() *cobra.Command {
	c := &cobra.Command{Use: "playback", Short: "Playback progress"}
	var typ string
	get := &cobra.Command{
		Use:   "get",
		Short: "Get playback progress",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/sync/playback"
			if typ != "" {
				path += "/" + typ
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	get.Flags().StringVar(&typ, "type", "", "movies|episodes")
	c.AddCommand(get)

	remove := &cobra.Command{
		Use:         "remove",
		Short:       "Remove a playback item by id",
		Annotations: map[string]string{"example_globals": "id"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig, "destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			id, err := a.requireID()
			if err != nil {
				return err
			}
			res, cerr := a.Client.Do(a.ctx(), http.MethodDelete, "/sync/playback/"+id, a.baseOpts(true))
			if cerr != nil {
				return cerr
			}
			if len(res.Data) == 0 {
				res.Data, _ = jsonRaw(map[string]bool{"removed": true})
			}
			return a.emit(res, "")
		},
	}
	c.AddCommand(remove)
	return c
}
