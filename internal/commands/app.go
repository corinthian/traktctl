// Package commands wires the cobra command tree. Each endpoint group lives in
// its own file and self-registers via init() -> Register(...), so adding a
// group is a single new file with zero edits to shared wiring (root, registry).
// Command files stay thin: translate flags -> client.Do -> output.Emit.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/rlarsen/traktctl/internal/auth"
	"github.com/rlarsen/traktctl/internal/client"
	"github.com/rlarsen/traktctl/internal/config"
	"github.com/rlarsen/traktctl/internal/output"
)

// Version is the binary version, stamped into the User-Agent and help.
const Version = "0.1.0"

// GlobalFlags holds the persistent flags bound on the root command.
type GlobalFlags struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	BaseURL      string
	ConfigPath   string

	Extended  string
	Page      int
	Limit     int
	All       bool
	ReallyAll bool
	Filters   []string

	IDType string
	ID     string

	Confirm bool
	LLM     bool

	Raw    bool
	NDJSON bool
	Terse  bool
}

// App is the shared runtime context handed to every command factory.
type App struct {
	Flags  *GlobalFlags
	Cfg    *config.Config
	Auth   *auth.Manager
	Client *client.Client
	Out    *output.Writer
}

// NewApp builds an App with a default JSON writer; build() finishes setup in
// PersistentPreRunE once flags are parsed.
func NewApp() *App {
	return &App{
		Flags: &GlobalFlags{},
		Out:   output.New(os.Stdout, os.Stderr, output.FormatJSON),
	}
}

// build resolves config and constructs auth + client from parsed global flags.
// Called from the root PersistentPreRunE.
func (a *App) build() *output.CLIError {
	cfg, err := config.Load(config.Flags{
		ClientID:     a.Flags.ClientID,
		ClientSecret: a.Flags.ClientSecret,
		AccessToken:  a.Flags.AccessToken,
		BaseURL:      a.Flags.BaseURL,
		ConfigPath:   a.Flags.ConfigPath,
	})
	if err != nil {
		return output.NewError(output.CodeBadConfig, "loading config: "+err.Error(), output.ExitUser)
	}
	a.Cfg = cfg
	a.Auth = auth.NewManager(cfg)
	a.Client = client.New(client.Config{
		BaseURL:  cfg.BaseURL,
		ClientID: cfg.ClientID,
		Version:  Version,
		Timeout:  cfg.Timeout,
		Tokens:   a.Auth,
		ErrW:     os.Stderr,
	})
	a.Out.Format = a.resolveFormat()
	return nil
}

func (a *App) resolveFormat() output.Format {
	switch {
	case a.Flags.Raw:
		return output.FormatRaw
	case a.Flags.NDJSON:
		return output.FormatNDJSON
	case a.Flags.Terse:
		return output.FormatTerse
	default:
		return output.FormatJSON
	}
}

// requireClientID guards commands that cannot work without a client_id.
func (a *App) requireClientID() *output.CLIError {
	if a.Cfg.ClientID == "" {
		return output.NewError(output.CodeBadConfig,
			"no client_id; set TRAKT_CLIENT_ID, --client-id, or config.toml", output.ExitUser)
	}
	return nil
}

// emit runs a client call's result through the writer, applying the optional
// terse summary. It is the single success path for commands.
func (a *App) emit(res *client.Result, terse string) error {
	meta := &output.Meta{
		Endpoint:        res.Endpoint,
		DurationMS:      res.DurationMS,
		TraktAPIVersion: "2",
		Pagination:      res.Pagination,
	}
	// 204/empty bodies emit a null-data success envelope.
	return a.Out.Emit(&output.Result{Data: res.Data, Meta: meta, Terse: terse})
}

// baseOpts builds client.Options from the global list/pagination/extended flags.
func (a *App) baseOpts(auth bool) client.Options {
	return client.Options{
		Extended:  a.Flags.Extended,
		Filters:   a.Flags.Filters,
		Page:      a.Flags.Page,
		Limit:     a.Flags.Limit,
		All:       a.Flags.All,
		ReallyAll: a.Flags.ReallyAll,
		Auth:      auth,
	}
}

// ctx returns a background context; a deadline is enforced by the http client
// timeout. Centralized so a future --timeout/global cancellation hooks in once.
func (a *App) ctx() context.Context { return context.Background() }

// get is a convenience for a GET call returning the result or CLIError-as-error.
func (a *App) get(path string, opts client.Options) (*client.Result, error) {
	res, cerr := a.Client.Do(a.ctx(), http.MethodGet, path, opts)
	if cerr != nil {
		return nil, cerr
	}
	return res, nil
}

// post is a convenience for a POST call.
func (a *App) post(path string, opts client.Options) (*client.Result, error) {
	res, cerr := a.Client.Do(a.ctx(), http.MethodPost, path, opts)
	if cerr != nil {
		return nil, cerr
	}
	return res, nil
}

// confirmed reports whether a destructive mutation is authorized via --confirm
// or TRAKTCTL_CONFIRM=1.
func (a *App) confirmed() bool {
	return a.Flags.Confirm || os.Getenv("TRAKTCTL_CONFIRM") == "1"
}

// parsePayload decodes a --payload JSON string into a generic value.
func parsePayload(s string) (interface{}, error) {
	if s == "" {
		return nil, output.NewError(output.CodeBadConfig, "missing required --payload JSON", output.ExitUser)
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, output.NewError(output.CodeBadConfig, "invalid --payload JSON: "+err.Error(), output.ExitUser)
	}
	return v, nil
}
