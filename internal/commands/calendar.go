package commands

import "github.com/spf13/cobra"

func init() { Register(newCalendarCmd) }

// calRow maps a calendar subcommand to its API path under /calendars and
// whether it requires auth (my-* personal vs all-* public).
type calRow struct {
	use, short, path string
	auth             bool
}

// newCalendarCmd builds the `calendar` group. Each command accepts optional
// --start (YYYY-MM-DD) and --days N appended to the path as /{start}/{days}.
func newCalendarCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "calendar", Short: "Upcoming releases (my-* personal, all-* public)"}
	rows := []calRow{
		{"my-shows", "Your shows", "my/shows", true},
		{"my-new-shows", "Your new shows", "my/shows/new", true},
		{"my-premieres", "Your season premieres", "my/shows/premieres", true},
		{"my-finales", "Your season finales", "my/shows/finales", true},
		{"my-movies", "Your movies", "my/movies", true},
		{"my-streaming", "Your streaming releases", "my/streaming", true},
		{"my-dvd", "Your DVD releases", "my/dvd", true},
		{"all-shows", "All shows", "all/shows", false},
		{"all-new-shows", "All new shows", "all/shows/new", false},
		{"all-premieres", "All season premieres", "all/shows/premieres", false},
		{"all-finales", "All season finales", "all/shows/finales", false},
		{"all-movies", "All movies", "all/movies", false},
		{"all-streaming", "All streaming releases", "all/streaming", false},
		{"all-dvd", "All DVD releases", "all/dvd", false},
	}
	for _, r := range rows {
		root.AddCommand(app.calendarCmd(r))
	}
	return root
}

func (a *App) calendarCmd(r calRow) *cobra.Command {
	var start string
	var days int
	c := &cobra.Command{
		Use:   r.use,
		Short: r.short,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/calendars/" + r.path
			if start != "" {
				path += "/" + start
				if days > 0 {
					path += "/" + itoa(days)
				}
			}
			res, err := a.get(path, a.baseOpts(r.auth))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&start, "start", "", "start date YYYY-MM-DD (default: today)")
	c.Flags().IntVar(&days, "days", 0, "number of days (default: 7; requires --start)")
	return c
}
