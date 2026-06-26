package commands

import (
	"encoding/json"
	"time"

	"github.com/rlarsen/traktctl/internal/auth"
	"github.com/rlarsen/traktctl/internal/config"
	"github.com/rlarsen/traktctl/internal/output"
	"github.com/spf13/cobra"
)

func init() { Register(newConfigCmd) }

// newConfigCmd builds the `config` group: local configuration bootstrap.
func newConfigCmd(app *App) *cobra.Command {
	root := &cobra.Command{Use: "config", Short: "Local configuration bootstrap"}
	root.AddCommand(app.configInit())
	root.AddCommand(app.configPath())
	return root
}

// configInit writes config.toml from the global credential flags, optionally
// chaining into the device flow. Reads the RAW flag values (app.Flags), not the
// resolved config, so an env-held secret is never written to disk unless the
// user explicitly passes --client-secret.
func (a *App) configInit() *cobra.Command {
	var defaultUser string
	var force, login, noBrowser bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write config.toml (credentials) for a fresh machine",
		RunE: func(cmd *cobra.Command, args []string) error {
			clientID := a.Flags.ClientID
			if clientID == "" {
				return output.NewError(output.CodeBadConfig,
					"missing required --client-id", output.ExitUser)
			}
			baseURL := a.Flags.BaseURL
			if baseURL == "" {
				baseURL = "https://api.trakt.tv"
			}
			fc := config.FileConfig{
				ClientID:     clientID,
				ClientSecret: a.Flags.ClientSecret,
				DefaultUser:  defaultUser,
				BaseURL:      baseURL,
				Extended:     a.Flags.Extended,
			}

			path := a.Flags.ConfigPath
			if path == "" {
				p, err := config.DefaultConfigPath()
				if err != nil {
					return output.NewError(output.CodeBadConfig, "resolving config path: "+err.Error(), output.ExitInternal)
				}
				path = p
			}
			if err := config.WriteConfigFile(path, fc, force); err != nil {
				return output.NewError(output.CodeBadConfig, err.Error(), output.ExitUser)
			}

			out := map[string]interface{}{
				"written_to":   path,
				"client_id":    clientID,
				"base_url":     baseURL,
				"default_user": defaultUser,
			}
			if fc.ClientSecret != "" {
				out["client_secret"] = "***redacted***"
			} else {
				out["client_secret"] = "(not written; supply via TRAKT_CLIENT_SECRET)"
			}

			if login {
				if fc.ClientSecret == "" {
					return output.NewError(output.CodeBadConfig,
						"--login needs --client-secret for the device flow", output.ExitUser)
				}
				cfg := &config.Config{ClientID: clientID, ClientSecret: fc.ClientSecret, BaseURL: baseURL, Timeout: 30 * time.Second}
				mgr := auth.NewManager(cfg)
				tok, loc, err := mgr.LoginDevice(a.ctx(), a.Out.Err, !noBrowser)
				if err != nil {
					return output.NewError(output.CodeAuthRequired, "config written, but login failed: "+err.Error(), output.ExitAuthMissing)
				}
				out["logged_in"] = true
				out["token_stored_at"] = loc
				out["token_expires_at"] = tok.ExpiresAt().UTC().Format("2006-01-02T15:04:05Z")
			}

			payload, _ := json.Marshal(out)
			return a.Out.Emit(&output.Result{Data: payload, Terse: "Wrote " + path})
		},
	}
	c.Flags().StringVar(&defaultUser, "default-user", "", "default --user for user commands")
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	c.Flags().BoolVar(&login, "login", false, "run auth device flow after writing")
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "with --login, do not open a browser")
	return c
}

// configPath prints where config and tokens are read from (no secrets).
func (a *App) configPath() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Show resolved config and token storage locations",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved := config.ResolvedConfigPath(a.Flags.ConfigPath)
			def, _ := config.DefaultConfigPath()
			_, tokenLoc := a.Auth.Token()
			if tokenLoc == "" {
				tokenLoc = "(none stored)"
			}
			out := map[string]interface{}{
				"config_in_use":   resolved,
				"config_default":  def,
				"config_found":    resolved != "",
				"token_stored_at": tokenLoc,
			}
			payload, _ := json.Marshal(out)
			return a.Out.Emit(&output.Result{Data: payload})
		},
	}
}
