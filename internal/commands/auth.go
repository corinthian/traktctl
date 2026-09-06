package commands

import (
	"encoding/json"
	"errors"
	"net/url"

	"github.com/corinthian/traktctl/internal/cause"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/corinthian/traktctl/internal/xhttp"
	"github.com/spf13/cobra"
)

// authFailure wraps an OAuth error under the code its cause deserves. A
// network fault or a body traktctl could not decode used to arrive as
// AUTH_EXPIRED, which told the user their session had gone and sent them to
// log in again over something that had nothing to do with their session. The
// auth package keeps returning a plain error; the classification happens here.
// Only an error that actually carries a transport or decode cause is
// reclassified. Classify's default branch is TransportOther, so an OAuth
// failure that is really about the status — "token endpoint failed: HTTP 401"
// — would otherwise be relabelled a network fault. A plain status error
// matches none of the three tests below and keeps the caller's fallback.
func authFailure(err error, prefix, fallback string, fallbackExit output.ExitCode) *output.CLIError {
	code, exit := fallback, fallbackExit
	var urlErr *url.Error
	var coded cause.Coded
	classified := errors.As(err, &urlErr) || errors.As(err, &coded) ||
		xhttp.Classify(err) != cause.TransportOther
	if !classified {
		return output.NewError(code, prefix+": "+err.Error(), exit)
	}
	switch xhttp.Classify(err) {
	case cause.Timeout:
		code, exit = output.CodeTransportTimeout, output.ExitTransport
	case cause.DNS, cause.TLS, cause.Refused, cause.TransportOther, cause.Cancelled:
		code, exit = output.CodeTransportFailed, output.ExitTransport
	case cause.Oversize, cause.Decode:
		code, exit = output.CodeDecodeError, output.ExitInternal
	}
	return output.NewError(code, prefix+": "+err.Error(), exit)
}

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
				return authFailure(err, "login failed", output.CodeAuthRequired, output.ExitAuthMissing)
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
				return authFailure(err, "refresh failed", output.CodeAuthExpired, output.ExitTrakt)
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
			// A load failure that is not "nothing stored" (locked keychain,
			// unreadable tokens.json) reads identically to "not logged in"
			// without this: same authenticated:false, same AUTH_REQUIRED on
			// the next call, and no way to tell the two apart.
			out := map[string]interface{}{"authenticated": tok != nil}
			if lerr := app.Auth.LoadError(); lerr != nil {
				out["load_error"] = lerr.Error()
			}
			if tok == nil {
				payload, _ := json.Marshal(out)
				return app.Out.Emit(&output.Result{Data: payload, Terse: "Not logged in"})
			}
			out["stored_at"] = loc
			out["scope"] = tok.Scope
			out["expired"] = tok.Expired()
			out["expires_at"] = tok.ExpiresAt().UTC().Format("2006-01-02T15:04:05Z")
			payload, _ := json.Marshal(out)
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
				return output.UsageError(
					"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1")
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
