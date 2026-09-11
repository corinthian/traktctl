package commands

import (
	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newListCmd) }

// newListCmd builds the `list` group: Trakt's public/curated lists surface
// (trending, popular, a single list's summary/items/likes). This is distinct
// from `user lists`, which reads a specific user's personal lists — `list`
// takes no --user, only a global --list-id.
//
// Read-only in this wave (B2). The mutation half (like/unlike a list) is not
// built here; see the B2 PR notes.
//
// Trakt serves all five of these endpoints with optional auth: a bearer is
// attached whenever one is configured (see client.Do), so a signed-in user's
// own private lists still resolve, but no token is required. auth=false below
// only means "not a hard requirement", matching the movie/show discovery
// pattern in titles.go.
func newListCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "list", Short: "Public and curated lists (trending, popular, get, items, likes)"}

	root.AddCommand(app.getList("trending", "Trending lists", "/lists/trending", false))
	root.AddCommand(app.getList("popular", "Popular lists", "/lists/popular", false))
	root.AddCommand(app.listGet())
	root.AddCommand(app.listItems())
	root.AddCommand(app.listLikes())

	return root
}

// listIDPrefix resolves /lists/{id} from --list-id, erroring when absent.
func (a *App) listIDPrefix(listID string) (string, error) {
	if listID == "" {
		return "", output.UsageError("missing required --list-id")
	}
	return "/lists/" + listID, nil
}

// bindListID binds the --list-id flag shared by get/items/likes.
func bindListID(c *cobra.Command, listID *string) {
	c.Flags().StringVar(listID, "list-id", "", "list id")
}

// listGet: GET /lists/{list_id}.
func (a *App) listGet() *cobra.Command {
	var listID string
	c := &cobra.Command{
		Use:   "get",
		Short: "Get a single list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listIDPrefix(listID)
			if err != nil {
				return err
			}
			res, gerr := a.get(prefix, a.baseOpts(false))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindListID(c, &listID)
	return c
}

// listItems: GET /lists/{list_id}/items[/{type}]. --type takes the same
// values as `user list-items` (movie|show|season|episode|person) and is
// optional, matching the endpoint.
func (a *App) listItems() *cobra.Command {
	var listID, typ string
	c := &cobra.Command{
		Use:   "items",
		Short: "Items in a list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listIDPrefix(listID)
			if err != nil {
				return err
			}
			path := prefix + "/items"
			if typ != "" {
				path += "/" + typ
			}
			res, gerr := a.get(path, a.baseOpts(false))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindListID(c, &listID)
	c.Flags().StringVar(&typ, "type", "", "movie|show|season|episode|person")
	return c
}

// listLikes: GET /lists/{list_id}/likes.
func (a *App) listLikes() *cobra.Command {
	var listID string
	c := &cobra.Command{
		Use:   "likes",
		Short: "Users who liked a list",
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix, err := a.listIDPrefix(listID)
			if err != nil {
				return err
			}
			res, gerr := a.get(prefix+"/likes", a.baseOpts(false))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	bindListID(c, &listID)
	return c
}
