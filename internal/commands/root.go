package commands

import (
	"errors"
	"os"

	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

// NewRoot builds the root command, binds global flags, registers every group,
// and wires the --llm short-circuit. Returns the root and the App so the caller
// (main) can emit typed errors with the correct exit code after Execute.
func NewRoot() (*cobra.Command, *App) {
	app := NewApp()
	g := app.Flags

	root := &cobra.Command{
		Use:           "traktctl",
		Short:         "JSON-first CLI wrapper over the Trakt API",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
		// Resolve config/client once flags are parsed, and honor --llm before
		// any command runs.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if g.LLM {
				emitLLMHelp(cmd, app.Out)
				os.Exit(0)
			}
			if cerr := validateIDType(g.IDType); cerr != nil {
				return cerr
			}
			if cerr := app.build(); cerr != nil {
				return cerr
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmd.Help(); err != nil {
				return output.NewError(output.CodeParseError, "writing help: "+err.Error(), output.ExitInternal)
			}
			return nil
		},
	}

	// Flag parse failures (unknown flag, missing flag value, bad flag type)
	// become typed usage errors at the parse layer, on the root and on every
	// subcommand — cobra propagates the func down the tree.
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return output.UsageErrorHint(err.Error(),
			"run `"+c.CommandPath()+" --help` for flags, or `--llm` for JSON help")
	})

	pf := root.PersistentFlags()
	pf.StringVar(&g.ClientID, "client-id", "", "Trakt client_id (overrides env/config)")
	pf.StringVar(&g.ClientSecret, "client-secret", "", "Trakt client_secret")
	pf.StringVar(&g.AccessToken, "access-token", "", "OAuth access token (overrides stored)")
	pf.StringVar(&g.BaseURL, "base-url", "", "API base URL (default https://api.trakt.tv)")
	pf.StringVar(&g.ConfigPath, "config", "", "path to config.toml")

	pf.StringVar(&g.Extended, "extended", "", "extended info, e.g. full,images")
	pf.IntVar(&g.Page, "page", 0, "page number")
	pf.IntVar(&g.Limit, "limit", 0, "items per page")
	pf.BoolVar(&g.All, "all", false, "auto-paginate list results (capped at 100 pages)")
	pf.BoolVar(&g.ReallyAll, "really-all", false, "bypass the --all page cap")
	pf.StringArrayVar(&g.Filters, "filter", nil, "query filter key=value (repeatable)")

	pf.StringVar(&g.IDType, "id-type", "trakt", "id type: trakt|slug|imdb|tmdb|tvdb")
	pf.StringVar(&g.ID, "id", "", "media id value")

	pf.BoolVar(&g.Confirm, "confirm", false, "confirm a destructive mutation")
	pf.BoolVar(&g.LLM, "llm", false, "emit machine-readable JSON help and exit")

	pf.BoolVar(&g.Raw, "raw", false, "pass Trakt's response through untouched")
	pf.BoolVar(&g.NDJSON, "ndjson", false, "emit one JSON object per line (lists)")
	pf.BoolVar(&g.Terse, "terse", false, "emit a one-line human summary")

	root.SetHelpTemplate(root.HelpTemplate() +
		"\nFor machine-readable JSON help, append --llm to any command.\n")

	addAll(root, app)
	root.AddCommand(newCommandsCmd(app))
	return root, app
}

// Execute runs the root command and translates errors into the JSON error
// envelope + stable exit code.
func Execute() {
	root, app := NewRoot()
	err := root.Execute()
	if err == nil {
		return
	}
	os.Exit(int(app.Out.EmitError(classifyError(err))))
}

// classifyError types whatever Execute returned. Anything cobra hands back
// that is not already a CLIError is a usage error: unknown command, unknown or
// malformed flag, bad positional args. Every traktctl-internal failure path
// returns a *output.CLIError (output.Emit types its own write failures), so
// nothing else reaches the fallback.
func classifyError(err error) *output.CLIError {
	var cliErr *output.CLIError
	if errors.As(err, &cliErr) {
		return cliErr
	}
	return output.UsageErrorHint(err.Error(),
		"run `traktctl --help` or `traktctl commands` to list commands")
}

// newCommandsCmd implements `traktctl commands`: the full tree as JSON.
func newCommandsCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "commands",
		Short: "Emit the full command tree as JSON",
		RunE: func(cmd *cobra.Command, args []string) error {
			tree := buildCommandTree(cmd.Root())
			data, err := jsonRaw(tree)
			if err != nil {
				return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
			}
			return app.Out.Emit(&output.Result{Data: data})
		},
	}
}
