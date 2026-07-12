// Package commands wires the cobra command tree. Each endpoint group lives in
// its own file and self-registers via init() -> Register(...), so adding a
// group is a single new file with zero edits to shared wiring (root, registry).
// Command files stay thin: translate flags -> client.Do -> output.Emit.
package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/client"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

// Version is the binary version, stamped into the User-Agent and help.
const Version = "1.0.1"

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
	// Under --terse, when the caller did not supply an explicit summary, derive
	// a human one-liner from the response shape (falls back to compact JSON in
	// the writer when summarize returns "").
	if terse == "" && a.Out.Format == output.FormatTerse {
		terse = summarize(res.Data)
	}
	// 204/empty bodies emit a null-data success envelope.
	return a.Out.Emit(&output.Result{Data: res.Data, Meta: meta, Terse: terse})
}

// mutationBuckets is the subset of a Trakt write response that says whether the
// request actually changed anything. The buckets vary by endpoint: adds return
// added/existing, removes return deleted, reorders return updated/skipped_ids.
// Every field is optional — an endpoint we do not model simply decodes to zeros.
type mutationBuckets struct {
	Added      map[string]int             `json:"added"`
	Existing   map[string]int             `json:"existing"`
	Deleted    map[string]int             `json:"deleted"`
	Updated    *int                       `json:"updated"`
	NotFound   map[string]json.RawMessage `json:"not_found"`
	SkippedIDs []json.RawMessage          `json:"skipped_ids"`
}

// applied counts everything the request actually accomplished. `existing` counts
// as applied on purpose: an idempotent re-add of an item already present is a
// true success — the desired state holds — and must not read as a no-op.
func (b *mutationBuckets) applied() int {
	n := sumCounts(b.Added) + sumCounts(b.Existing) + sumCounts(b.Deleted)
	if b.Updated != nil {
		n += *b.Updated
	}
	return n
}

// unresolved counts what Trakt refused to act on. Verified against the live API
// (2026-07-12): on a remove, `not_found` means "could not resolve this id", NOT
// "this item was not in your list" — deleting an absent-but-resolvable item
// returns not_found:[] and deleted:0. That is what keeps idempotent removal a
// success here, exactly as `existing` keeps idempotent adds one.
// `skipped_ids` is reorder's equivalent signal.
func (b *mutationBuckets) unresolved() int {
	n := len(b.SkippedIDs)
	for _, raw := range b.NotFound {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			continue // non-array bucket: not a not-found list, ignore
		}
		n += len(arr)
	}
	return n
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// emitMutation is the success path for commands that can change account state.
// It exists because a 2xx from Trakt does not mean the mutation did anything:
// a body whose ids do not resolve returns 200 with every count at zero. Plain
// emit() would render that as unqualified ok:true — a no-op that reads as
// success, which is the failure this command class must never have.
//
// Reads keep using emit(); only the payload writers call this.
func (a *App) emitMutation(res *client.Result, terse string) error {
	meta := &output.Meta{
		Endpoint:        res.Endpoint,
		DurationMS:      res.DurationMS,
		TraktAPIVersion: "2",
		Pagination:      res.Pagination,
	}

	// Decode leniently and fail toward success: an unmodeled shape, an array
	// body, or a 204 with no body must never manufacture a NOT_APPLIED.
	var b mutationBuckets
	if len(res.Data) > 0 {
		_ = json.Unmarshal(res.Data, &b)
	}
	applied, unresolved := b.applied(), b.unresolved()

	switch {
	case unresolved == 0:
		// Nothing was refused. Includes the idempotent cases and every
		// endpoint whose response carries no buckets at all.

	case applied == 0:
		// Trakt accepted the request and applied none of it. Fail loudly.
		return &output.CLIError{
			Code: output.CodeNotApplied,
			Message: fmt.Sprintf(
				"nothing was applied: Trakt could not resolve %d item(s) in --payload; no changes were made",
				unresolved),
			Hint:       "check the ids/slugs in --payload (resolve them with `traktctl search`)",
			Exit:       output.ExitNotApplied,
			Endpoint:   res.Endpoint,
			DurationMS: res.DurationMS,
			RawBody:    res.Data,
		}

	default:
		// Partial: some items landed, some did not. Still a success, but it
		// must not read as a total one — surface exactly what Trakt refused.
		meta.Partial = true
		if b.NotFound != nil {
			if raw, err := json.Marshal(b.NotFound); err == nil {
				meta.NotFound = raw
			}
		}
		if b.SkippedIDs != nil {
			if raw, err := json.Marshal(b.SkippedIDs); err == nil {
				meta.SkippedIDs = raw
			}
		}
		if terse == "" && a.Out.Format == output.FormatTerse {
			terse = fmt.Sprintf("partial: %d applied, %d unresolved", applied, unresolved)
		}
	}

	if terse == "" && a.Out.Format == output.FormatTerse {
		terse = summarize(res.Data)
	}
	return a.Out.Emit(&output.Result{Data: res.Data, Meta: meta, Terse: terse})
}

// rejectIDFlags fails a payload-only mutation that was handed --id/--id-type.
// Those are lookup flags, hoisted to root as persistent flags, so every command
// inherits them; the payload writers never read them. Silently ignoring them let
// a caller believe --id selected the item while the body said otherwise.
// Detect with Changed(), not by value: --id-type defaults to "trakt", so a value
// comparison would either miss `--id-type trakt` or false-positive on the default.
func rejectIDFlags(cmd *cobra.Command) *output.CLIError {
	if !cmd.Flags().Changed("id") && !cmd.Flags().Changed("id-type") {
		return nil
	}
	return output.NewError(output.CodeBadConfig,
		"this command ignores --id/--id-type; a mutation takes its targeting from --payload "+
			`(e.g. --payload '{"movies":[{"ids":{"slug":"gilda-1946"}}]}')`,
		output.ExitUser)
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

// del is a convenience for a DELETE call.
func (a *App) del(path string, opts client.Options) (*client.Result, error) {
	res, cerr := a.Client.Do(a.ctx(), http.MethodDelete, path, opts)
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
		// Name the lookup-vs-mutation split here: this is the error a caller
		// following the old (wrong) --id examples actually hits.
		return nil, output.NewError(output.CodeBadConfig,
			"missing required --payload JSON; a mutation takes its targeting from --payload, "+
				"not --id/--id-type (those are lookup flags)", output.ExitUser)
	}
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, output.NewError(output.CodeBadConfig, "invalid --payload JSON: "+err.Error(), output.ExitUser)
	}
	return v, nil
}
