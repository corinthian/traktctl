package commands

import (
	"net/http"
	"net/url"

	"github.com/rlarsen/traktctl/internal/client"
	"github.com/rlarsen/traktctl/internal/output"
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

// newUserCmd builds the `user` group: profile, social graph, lists, and the
// personal-data reads/mutations spec'd in the user command table. The bearer is
// attached when present so private profiles (incl. "me") resolve. Destructive
// writes are gated behind --confirm exactly like the sync mutations.
func newUserCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "user", Short: "User profile and personal data"}

	// --- account-scoped reads (no /{id}) ---
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
	root.AddCommand(app.userFixedRead("requests-following", "Pending follow requests you sent", "/users/requests/following"))
	root.AddCommand(app.userFixedRead("requests-follower", "Pending follow requests you received", "/users/requests"))
	root.AddCommand(app.userFixedRead("blocked", "Users you have blocked", "/users/blocked"))

	root.AddCommand(app.userSavedFilters())
	root.AddCommand(app.userHidden())

	// --- /{id}-scoped reads ---
	root.AddCommand(app.userIDCmd("profile", "User profile", ""))
	root.AddCommand(app.userIDCmd("stats", "Watch statistics", "/stats"))
	root.AddCommand(app.userIDCmd("watching", "What the user is watching now", "/watching"))
	root.AddCommand(app.userIDCmd("followers", "Followers", "/followers"))
	root.AddCommand(app.userIDCmd("following", "Following", "/following"))
	root.AddCommand(app.userIDCmd("friends", "Friends", "/friends"))
	root.AddCommand(app.userIDCmd("lists", "User's personal lists", "/lists"))
	root.AddCommand(app.userIDCmd("collaborations", "Lists the user collaborates on", "/lists/collaborations"))

	root.AddCommand(app.userTypeCmd("collection", "Collected items", "/collection", true))
	root.AddCommand(app.userTypeCmd("watched", "Watched items", "/watched", true))
	root.AddCommand(app.userTypeCmd("likes", "Items the user has liked", "/likes", false))
	root.AddCommand(app.userTypeCmd("notes", "User's notes", "/notes", false))

	root.AddCommand(app.userComments())

	root.AddCommand(app.userSortableCmd("watchlist", "User's watchlist", "watchlist"))
	root.AddCommand(app.userSortableCmd("favorites", "User's favorites", "favorites"))
	root.AddCommand(app.userCommentsOn("watchlist-comments", "Comments on the user's watchlist", "watchlist"))
	root.AddCommand(app.userCommentsOn("favorites-comments", "Comments on the user's favorites", "favorites"))

	root.AddCommand(app.userHistoryCmd())
	root.AddCommand(app.userRatingsCmd())

	// --- single list reads ---
	root.AddCommand(app.userListGet())
	root.AddCommand(app.userListItems())
	root.AddCommand(app.userListLikes())
	root.AddCommand(app.userListComments())

	// --- write/mutation commands (confirm-gated where destructive) ---
	root.AddCommand(app.userFollow())
	root.AddCommand(app.userBlock())
	root.AddCommand(app.userRequestsRespond())
	root.AddCommand(app.userListsReorder())
	root.AddCommand(app.userListCreate())
	root.AddCommand(app.userListUpdate())
	root.AddCommand(app.userListDelete())
	root.AddCommand(app.userListLike())
	root.AddCommand(app.userListUnlike())
	root.AddCommand(app.userListItemsAdd())
	root.AddCommand(app.userListItemsRemove())
	root.AddCommand(app.userListItemsReorder())
	root.AddCommand(app.userListItemUpdate())
	root.AddCommand(app.userListReport())
	root.AddCommand(app.userReport())

	return root
}

// userFixedRead: GET <path> with the bearer (account-scoped, no /{id}).
func (a *App) userFixedRead(use, short, path string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
}

// userIDCmd: GET /users/{id}{suffix}, with --user. Bearer is attached so
// private profiles (incl. "me") resolve.
func (a *App) userIDCmd(use, short, suffix string) *cobra.Command {
	var user string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.get("/users/"+a.userTarget(user)+suffix, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username (default: config default_user or 'me')")
	return c
}

// userTypeCmd: GET /users/{id}{base}[/{type}].
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
			res, err := a.get(path, a.baseOpts(true))
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

// userComments: GET /users/{id}/comments[/{comment_type}[/{type}]].
func (a *App) userComments() *cobra.Command {
	var user, commentType, typ string
	c := &cobra.Command{
		Use:   "comments",
		Short: "Comments the user has posted",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + "/comments"
			if commentType != "" {
				path += "/" + commentType
				if typ != "" {
					path += "/" + typ
				}
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&commentType, "comment-type", "", "all|reviews|shouts")
	c.Flags().StringVar(&typ, "type", "", "movies|shows|seasons|episodes|lists")
	return c
}

// userSavedFilters: GET /users/saved_filters[/{section}] (VIP-gated at Trakt).
func (a *App) userSavedFilters() *cobra.Command {
	var section string
	c := &cobra.Command{
		Use:   "saved-filters",
		Short: "Saved filters (VIP)",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/saved_filters"
			if section != "" {
				path += "/" + section
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&section, "section", "", "movies|shows|calendars|search")
	return c
}

// userHidden builds `user hidden {get,add,remove}` for GET/POST
// /users/hidden/{section}. get is a read; add/remove are destructive.
func (a *App) userHidden() *cobra.Command {
	root := &cobra.Command{Use: "hidden", Short: "Hidden items management"}

	var getSection, getType string
	get := &cobra.Command{
		Use:   "get",
		Short: "List hidden items in a section",
		RunE: func(cmd *cobra.Command, args []string) error {
			if getSection == "" {
				return output.NewError(output.CodeBadConfig, "missing required --section", output.ExitUser)
			}
			opts := a.baseOpts(true)
			if getType != "" {
				opts.Query = url.Values{"type": {getType}}
			}
			res, err := a.get("/users/hidden/"+getSection, opts)
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	get.Flags().StringVar(&getSection, "section", "", "calendar|progress_watched|progress_collected|recommendations|comments")
	get.Flags().StringVar(&getType, "type", "", "movie|show|season|user")
	root.AddCommand(get)

	root.AddCommand(a.hiddenWrite("add", "Hide items (destructive)", ""))
	root.AddCommand(a.hiddenWrite("remove", "Unhide items (destructive)", "/remove"))
	return root
}

// hiddenWrite: POST /users/hidden/{section}{suffix} with --section --payload.
func (a *App) hiddenWrite(use, short, suffix string) *cobra.Command {
	var section, payload string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if section == "" {
				return output.NewError(output.CodeBadConfig, "missing required --section", output.ExitUser)
			}
			body, err := parsePayload(payload)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, perr := a.post("/users/hidden/"+section+suffix, opts)
			if perr != nil {
				return perr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&section, "section", "", "hidden section")
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
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
			res, err := a.get(path, a.baseOpts(true))
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

// userCommentsOn: GET /users/{id}/{base}/comments[/{sort}] (watchlist/favorites).
func (a *App) userCommentsOn(use, short, base string) *cobra.Command {
	var user, sort string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/users/" + a.userTarget(user) + "/" + base + "/comments"
			if sort != "" {
				path += "/" + sort
			}
			res, err := a.get(path, a.baseOpts(true))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&sort, "sort", "", "newest|oldest|likes|replies")
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
			opts := a.baseOpts(true)
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
			res, err := a.get(path, a.baseOpts(true))
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

// --- single-list reads ---

// listPrefix resolves the path prefix /users/{id}/lists/{list_id} from --user
// and --list-id. Returns an error when --list-id is missing.
func (a *App) listPrefix(user, listID string) (string, error) {
	if listID == "" {
		return "", output.NewError(output.CodeBadConfig, "missing required --list-id", output.ExitUser)
	}
	return "/users/" + a.userTarget(user) + "/lists/" + listID, nil
}

// userListGet: GET /users/{id}/lists/{list_id}.
func (a *App) userListGet() *cobra.Command {
	var user, listID string
	c := &cobra.Command{
		Use:   "list",
		Short: "Get a single list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			res, gerr := a.get(prefix, a.baseOpts(true))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	return c
}

// userListItems: GET /users/{id}/lists/{list_id}/items[/{type}[/{sort_by}/{sort_how}]].
func (a *App) userListItems() *cobra.Command {
	var user, listID, typ, sortBy, sortHow string
	c := &cobra.Command{
		Use:   "list-items",
		Short: "Items in a list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			path := prefix + "/items"
			if typ != "" {
				path += "/" + typ
				if sortBy != "" && sortHow != "" {
					path += "/" + sortBy + "/" + sortHow
				}
			}
			res, gerr := a.get(path, a.baseOpts(true))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	c.Flags().StringVar(&typ, "type", "", "movie|show|season|episode|person")
	c.Flags().StringVar(&sortBy, "sort-by", "", "rank|added|title|released")
	c.Flags().StringVar(&sortHow, "sort-how", "", "asc|desc")
	return c
}

// userListLikes: GET /users/{id}/lists/{list_id}/likes.
func (a *App) userListLikes() *cobra.Command {
	var user, listID string
	c := &cobra.Command{
		Use:   "list-likes",
		Short: "Users who liked a list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			res, gerr := a.get(prefix+"/likes", a.baseOpts(true))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	return c
}

// userListComments: GET /users/{id}/lists/{list_id}/comments[/{sort}].
func (a *App) userListComments() *cobra.Command {
	var user, listID, sort string
	c := &cobra.Command{
		Use:   "list-comments",
		Short: "Comments on a list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			path := prefix + "/comments"
			if sort != "" {
				path += "/" + sort
			}
			res, gerr := a.get(path, a.baseOpts(true))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	c.Flags().StringVar(&sort, "sort", "", "newest|oldest|likes|replies")
	return c
}

// --- write/mutation commands ---

// userFollow: POST /users/{id}/follow (or DELETE with --unfollow). Destructive.
func (a *App) userFollow() *cobra.Command {
	var user string
	var unfollow bool
	c := &cobra.Command{
		Use:   "follow",
		Short: "Follow a user (--unfollow to reverse; destructive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if user == "" {
				return output.NewError(output.CodeBadConfig, "missing required --user", output.ExitUser)
			}
			path := "/users/" + user + "/follow"
			var res *client.Result
			var err error
			if unfollow {
				res, err = a.del(path, a.baseOpts(true))
			} else {
				res, err = a.post(path, a.baseOpts(true))
			}
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().BoolVar(&unfollow, "unfollow", false, "unfollow instead of follow")
	return c
}

// userBlock: POST /users/{id}/block (or DELETE with --unblock). Destructive.
func (a *App) userBlock() *cobra.Command {
	var user string
	var unblock bool
	c := &cobra.Command{
		Use:   "block",
		Short: "Block a user (--unblock to reverse; destructive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if user == "" {
				return output.NewError(output.CodeBadConfig, "missing required --user", output.ExitUser)
			}
			path := "/users/" + user + "/block"
			var res *client.Result
			var err error
			if unblock {
				res, err = a.del(path, a.baseOpts(true))
			} else {
				res, err = a.post(path, a.baseOpts(true))
			}
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().BoolVar(&unblock, "unblock", false, "unblock instead of block")
	return c
}

// userRequestsRespond: POST /users/requests/{id} (approve) or DELETE (deny).
func (a *App) userRequestsRespond() *cobra.Command {
	var id string
	var approved bool
	c := &cobra.Command{
		Use:   "requests-respond",
		Short: "Approve (--approved) or deny a follower request (destructive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if id == "" {
				return output.NewError(output.CodeBadConfig, "missing required --id (request id)", output.ExitUser)
			}
			path := "/users/requests/" + id
			var res *client.Result
			var err error
			if approved {
				res, err = a.post(path, a.baseOpts(true))
			} else {
				res, err = a.del(path, a.baseOpts(true))
			}
			if err != nil {
				return err
			}
			if res != nil && len(res.Data) == 0 {
				res.Data, _ = jsonRaw(map[string]bool{"ok": true})
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&id, "id", "", "follower request id")
	c.Flags().BoolVar(&approved, "approved", false, "approve (default denies)")
	return c
}

// userListsReorder: POST /users/{id}/lists/reorder. Destructive.
func (a *App) userListsReorder() *cobra.Command {
	return a.userListBodyWrite("lists-reorder", "Reorder the user's lists (destructive)",
		"/lists/reorder", http.MethodPost, false)
}

// userListCreate: POST /users/{id}/lists. Destructive (VIP at Trakt).
func (a *App) userListCreate() *cobra.Command {
	return a.userListBodyWrite("list-create", "Create a list (VIP; destructive)",
		"/lists", http.MethodPost, false)
}

// userListBodyWrite builds a /users/{id}{suffix} body mutation taking --payload
// and optionally --list-id. Always confirm-gated.
func (a *App) userListBodyWrite(use, short, suffix, method string, needListID bool) *cobra.Command {
	var user, payload, listID string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			path := "/users/" + a.userTarget(user)
			if needListID {
				if listID == "" {
					return output.NewError(output.CodeBadConfig, "missing required --list-id", output.ExitUser)
				}
				path += "/lists/" + listID + suffix
			} else {
				path += suffix
			}
			body, err := parsePayload(payload)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, cerr := a.Client.Do(a.ctx(), method, path, opts)
			if cerr != nil {
				return cerr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
	if needListID {
		c.Flags().StringVar(&listID, "list-id", "", "list id")
	}
	return c
}

// userListUpdate: PUT /users/{id}/lists/{list_id}. Destructive.
func (a *App) userListUpdate() *cobra.Command {
	return a.userListBodyWrite("list-update", "Update a list (destructive)",
		"", http.MethodPut, true)
}

// userListItemsAdd: POST /users/{id}/lists/{list_id}/items (VIP). Destructive.
func (a *App) userListItemsAdd() *cobra.Command {
	return a.userListBodyWrite("list-items-add", "Add items to a list (VIP; destructive)",
		"/items", http.MethodPost, true)
}

// userListItemsRemove: POST /users/{id}/lists/{list_id}/items/remove. Destructive.
func (a *App) userListItemsRemove() *cobra.Command {
	return a.userListBodyWrite("list-items-remove", "Remove items from a list (destructive)",
		"/items/remove", http.MethodPost, true)
}

// userListItemsReorder: POST /users/{id}/lists/{list_id}/items/reorder. Destructive.
func (a *App) userListItemsReorder() *cobra.Command {
	return a.userListBodyWrite("list-items-reorder", "Reorder list items (destructive)",
		"/items/reorder", http.MethodPost, true)
}

// userListReport: POST /users/{id}/lists/{list_id}/report. Destructive.
func (a *App) userListReport() *cobra.Command {
	return a.userListBodyWriteOptionalBody("list-report", "Report a list (destructive)",
		"/report", http.MethodPost, true)
}

// userListDelete: DELETE /users/{id}/lists/{list_id}. Destructive, no body.
func (a *App) userListDelete() *cobra.Command {
	return a.userListNoBodyWrite("list-delete", "Delete a list (destructive)",
		"", http.MethodDelete)
}

// userListLike: POST /users/{id}/lists/{list_id}/like. Destructive, no body.
func (a *App) userListLike() *cobra.Command {
	return a.userListNoBodyWrite("list-like", "Like a list (destructive)",
		"/like", http.MethodPost)
}

// userListUnlike: DELETE /users/{id}/lists/{list_id}/like. Destructive, no body.
func (a *App) userListUnlike() *cobra.Command {
	return a.userListNoBodyWrite("list-unlike", "Unlike a list (destructive)",
		"/like", http.MethodDelete)
}

// userListNoBodyWrite builds a confirm-gated /users/{id}/lists/{list_id}{suffix}
// mutation with no request body (like/unlike/delete).
func (a *App) userListNoBodyWrite(use, short, suffix, method string) *cobra.Command {
	var user, listID string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			res, cerr := a.Client.Do(a.ctx(), method, prefix+suffix, a.baseOpts(true))
			if cerr != nil {
				return cerr
			}
			if res != nil && len(res.Data) == 0 {
				res.Data, _ = jsonRaw(map[string]bool{"ok": true})
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	return c
}

// userListBodyWriteOptionalBody is like userListBodyWrite but the --payload is
// optional (e.g. report, which may carry an optional reason body).
func (a *App) userListBodyWriteOptionalBody(use, short, suffix, method string, needListID bool) *cobra.Command {
	var user, payload, listID string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			if payload != "" {
				body, perr := parsePayload(payload)
				if perr != nil {
					return perr
				}
				opts.Body = body
			}
			res, cerr := a.Client.Do(a.ctx(), method, prefix+suffix, opts)
			if cerr != nil {
				return cerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	c.Flags().StringVar(&payload, "payload", "", "optional request body as JSON")
	return c
}

// userListItemUpdate: PUT /users/{id}/lists/{list_id}/items/{list_item_id}.
func (a *App) userListItemUpdate() *cobra.Command {
	var user, listID, listItemID, payload string
	c := &cobra.Command{
		Use:   "list-item-update",
		Short: "Update a single list item (destructive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			prefix, err := a.listPrefix(user, listID)
			if err != nil {
				return err
			}
			if listItemID == "" {
				return output.NewError(output.CodeBadConfig, "missing required --list-item-id", output.ExitUser)
			}
			body, perr := parsePayload(payload)
			if perr != nil {
				return perr
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, cerr := a.Client.Do(a.ctx(), http.MethodPut, prefix+"/items/"+listItemID, opts)
			if cerr != nil {
				return cerr
			}
			return a.emit(res, "")
		},
	}
	bindUserList(c, &user, &listID)
	c.Flags().StringVar(&listItemID, "list-item-id", "", "list item id")
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
	return c
}

// userReport: POST /users/{id}/report. Destructive.
func (a *App) userReport() *cobra.Command {
	var user, payload string
	c := &cobra.Command{
		Use:   "report",
		Short: "Report a user (destructive)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if user == "" {
				return output.NewError(output.CodeBadConfig, "missing required --user", output.ExitUser)
			}
			opts := a.baseOpts(true)
			if payload != "" {
				body, perr := parsePayload(payload)
				if perr != nil {
					return perr
				}
				opts.Body = body
			}
			res, err := a.post("/users/"+user+"/report", opts)
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&user, "user", "", "target username")
	c.Flags().StringVar(&payload, "payload", "", "optional request body as JSON")
	return c
}

// bindUserList binds the shared --user and --list-id flags.
func bindUserList(c *cobra.Command, user, listID *string) {
	c.Flags().StringVar(user, "user", "", "target username")
	c.Flags().StringVar(listID, "list-id", "", "list id")
}
