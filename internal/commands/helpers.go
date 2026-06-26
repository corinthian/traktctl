package commands

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rlarsen/traktctl/internal/output"
	"github.com/spf13/cobra"
)

// itoa is a local shorthand for building numeric path segments.
func itoa(n int) string { return strconv.Itoa(n) }

// requireID returns the global --id value or a user error if empty.
func (a *App) requireID() (string, error) {
	if strings.TrimSpace(a.Flags.ID) == "" {
		return "", output.NewError(output.CodeBadConfig, "missing required --id", output.ExitUser)
	}
	return a.Flags.ID, nil
}

// getList builds a no-argument GET command (e.g. `movie trending`).
func (a *App) getList(use, short, path string, auth bool) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := a.get(path, a.baseOpts(auth))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
}

// getByID builds an id-scoped GET command: GET <prefix>/{id}<suffix>.
func (a *App) getByID(use, short, prefix, suffix string, auth bool) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.requireID()
			if err != nil {
				return err
			}
			path := prefix + "/" + id + suffix
			res, gerr := a.get(path, a.baseOpts(auth))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
}

// getPeriod builds a `--period` GET command: GET <prefix>/{period}.
// Valid periods: daily, weekly, monthly, yearly, all.
func (a *App) getPeriod(use, short, prefix, def string, auth bool) *cobra.Command {
	var period string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := period
			if p == "" {
				p = def
			}
			res, err := a.get(prefix+"/"+p, a.baseOpts(auth))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&period, "period", def, "period: daily|weekly|monthly|yearly|all")
	return c
}

// getStart builds a `--start DATE` GET command: GET <prefix>/{start}.
func (a *App) getStart(use, short, prefix string, auth bool) *cobra.Command {
	var start string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if start == "" {
				return output.NewError(output.CodeBadConfig, "missing required --start (YYYY-MM-DD)", output.ExitUser)
			}
			res, err := a.get(prefix+"/"+start, a.baseOpts(auth))
			if err != nil {
				return err
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&start, "start", "", "start date YYYY-MM-DD")
	return c
}

// idSuffixCmd builds an id-scoped GET with an optional trailing path segment
// taken from a single string flag (e.g. comments --sort, releases --country).
func (a *App) idSuffixCmd(use, short, prefix, suffix, flagName, flagDefault, flagUsage string, auth bool) *cobra.Command {
	var val string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := a.requireID()
			if err != nil {
				return err
			}
			path := prefix + "/" + id + suffix
			if val != "" {
				path += "/" + val
			}
			res, gerr := a.get(path, a.baseOpts(auth))
			if gerr != nil {
				return gerr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&val, flagName, flagDefault, flagUsage)
	return c
}

// postCmd builds a mutating POST command that takes a --payload JSON body.
// When confirmRequired, it refuses without --confirm or TRAKTCTL_CONFIRM=1.
func (a *App) postCmd(use, short, path string, confirmRequired bool) *cobra.Command {
	var payload string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if confirmRequired && !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			body, err := parsePayload(payload)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, perr := a.post(path, opts)
			if perr != nil {
				return perr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
	return c
}

// putCmd builds a mutating PUT command taking a --payload JSON body. Always
// confirm-gated (PUT here means replace settings/state).
func (a *App) putCmd(use, short, path string, confirmRequired bool) *cobra.Command {
	var payload string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if confirmRequired && !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			body, err := parsePayload(payload)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, cerr := a.Client.Do(a.ctx(), http.MethodPut, path, opts)
			if cerr != nil {
				return cerr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
	return c
}

// putItemCmd builds `<use> --list-item-id ID --payload JSON`: PUT
// <prefix>/{list_item_id}. Confirm-gated.
func (a *App) putItemCmd(use, short, prefix string) *cobra.Command {
	var payload, itemID string
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !a.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if itemID == "" {
				return output.NewError(output.CodeBadConfig, "missing required --list-item-id", output.ExitUser)
			}
			body, err := parsePayload(payload)
			if err != nil {
				return err
			}
			opts := a.baseOpts(true)
			opts.Body = body
			res, cerr := a.Client.Do(a.ctx(), http.MethodPut, prefix+"/"+itemID, opts)
			if cerr != nil {
				return cerr
			}
			return a.emit(res, "")
		},
	}
	c.Flags().StringVar(&itemID, "list-item-id", "", "list item id")
	c.Flags().StringVar(&payload, "payload", "", "request body as JSON")
	return c
}
