package commands

import (
	"encoding/json"

	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newAuthCmd) }

// newAuthCmd builds the `auth` group: device-flow login, refresh, status,
// logout, and revoke.
func newAuthCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "auth", Short: "Authentication lifecycle"}

	var noBrowser bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Authorize via OAuth device flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cerr := app.requireClientID(); cerr != nil {
				return cerr
			}
			if app.Cfg.ClientSecret == "" {
				return output.NewError(output.CodeBadConfig,
					"device flow needs client_secret; set TRAKT_CLIENT_SECRET or config.toml", output.ExitUser)
			}
			tok, loc, err := app.Auth.LoginDevice(app.ctx(), app.Out.Err, !noBrowser)
			if err != nil {
				return output.NewError(output.CodeAuthRequired, "login failed: "+err.Error(), output.ExitAuthMissing)
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"authorized": true,
				"scope":      tok.Scope,
				"stored_at":  loc,
				"expires_at": tok.ExpiresAt().UTC().Format("2006-01-02T15:04:05Z"),
			})
			return app.Out.Emit(&output.Result{Data: payload, Terse: "Logged in (" + loc + ")"})
		},
	}
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "do not open a browser")
	root.AddCommand(login)

	root.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Force a token refresh",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.Auth.HasToken() {
				return output.NewError(output.CodeAuthRequired, "not logged in", output.ExitAuthMissing)
			}
			if err := app.Auth.Refresh(app.ctx()); err != nil {
				return output.NewError(output.CodeAuthExpired, "refresh failed: "+err.Error(), output.ExitTrakt)
			}
			tok, loc := app.Auth.Token()
			payload, _ := json.Marshal(map[string]interface{}{
				"refreshed":  true,
				"stored_at":  loc,
				"expires_at": tok.ExpiresAt().UTC().Format("2006-01-02T15:04:05Z"),
			})
			return app.Out.Emit(&output.Result{Data: payload, Terse: "Token refreshed"})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show token state (local only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			tok, loc := app.Auth.Token()
			if tok == nil {
				payload, _ := json.Marshal(map[string]interface{}{"authenticated": false})
				return app.Out.Emit(&output.Result{Data: payload, Terse: "Not logged in"})
			}
			payload, _ := json.Marshal(map[string]interface{}{
				"authenticated": true,
				"stored_at":     loc,
				"scope":         tok.Scope,
				"expired":       tok.Expired(),
				"expires_at":    tok.ExpiresAt().UTC().Format("2006-01-02T15:04:05Z"),
			})
			return app.Out.Emit(&output.Result{Data: payload})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Clear local token storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.Auth.Logout(); err != nil {
				return output.NewError(output.CodeBadConfig, "logout failed: "+err.Error(), output.ExitInternal)
			}
			payload, _ := json.Marshal(map[string]bool{"logged_out": true})
			return app.Out.Emit(&output.Result{Data: payload, Terse: "Logged out"})
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "revoke",
		Short: "Revoke the token at Trakt and clear local storage",
		RunE: func(cmd *cobra.Command, args []string) error {
			if !app.confirmed() {
				return output.NewError(output.CodeBadConfig,
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1", output.ExitUser)
			}
			if err := app.Auth.Revoke(app.ctx()); err != nil {
				e := output.NewError(output.CodeBadConfig, "revoke failed: "+err.Error(), output.ExitInternal)
				e.Hint = "the token was NOT cleared locally; retry, or run `traktctl auth logout` to clear it locally anyway"
				return e
			}
			payload, _ := json.Marshal(map[string]bool{"revoked": true})
			return app.Out.Emit(&output.Result{Data: payload, Terse: "Token revoked"})
		},
	})

	return root
}
