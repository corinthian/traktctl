// Package commands wires the cobra command tree. Each endpoint group lives in
// its own file and self-registers via init() -> Register(...), so adding a
// group is a single new file with zero edits to shared wiring (root, registry).
// Command files stay thin: translate flags -> client.Do -> output.Emit.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/client"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/corinthian/traktctl/internal/xduration"
	"github.com/corinthian/traktctl/internal/xhttp"
	"github.com/spf13/cobra"
)

// Version is the binary version, stamped into the User-Agent and help.
var Version = "1.3.0"

// defaultBaseURL mirrors config's default. Used where a Config is built
// without going through config.Load (config init, the tolerant build path).
const defaultBaseURL = "https://api.trakt.tv"

// GlobalFlags holds the persistent flags bound on the root command.
type GlobalFlags struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	BaseURL      string
	ConfigPath   string
	Timeout      string
	// TimeoutSet is cmd.Flags().Changed("timeout"), set in PersistentPreRunE
	// before build() -- it is what makes --timeout "" distinguishable from an
	// absent flag.
	TimeoutSet bool

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

	// CfgErr holds a BAD_CONFIG failure that was tolerated rather than fatal,
	// for the commands annotated tolerateBadConfig. Nil on the normal path.
	CfgErr *output.CLIError

	// runCtx is cmd.Context(), captured in PersistentPreRunE. In production
	// this is context.Background() (main.go calls Execute(), not
	// ExecuteContext()) -- see ctx()'s doc comment. Exported access is
	// through ctx(), not this field directly, so a test can set it without a
	// live cobra command.
	runCtx context.Context
}

// tolerateBadConfig marks the commands that must still run when the config
// *path* is unusable (config.ErrConfigPath). It does not forgive a file that
// exists but fails to parse or validate -- `config init` over one of those
// would write a config every later command rejects. There are exactly two
// annotated commands, and both are structural:
//
//   - `config path` is the read-only diagnostic you reach for to debug a bad
//     config path. Hard-failing it removes the tool for the very problem it
//     exists to report.
//   - `config init --config /new/path.toml` names a file that does not exist
//     yet, by definition.
//
// An annotated command continues against a minimal default Config so Auth and
// Client still construct, with the error stashed on App.CfgErr.
const tolerateBadConfig = "tolerate_bad_config"

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
		Timeout:      a.Flags.Timeout,
		TimeoutSet:   a.Flags.TimeoutSet,
	})
	if err != nil {
		// A rejected --timeout is a usage error (BAD_REQUEST); a rejected
		// $TRAKTCTL_TIMEOUT or config timeout is a config error (BAD_CONFIG).
		// xduration itself names no tool's codes -- the Source is what
		// distinguishes them here.
		var derr *xduration.Error
		if errors.As(err, &derr) && derr.Source == "--timeout" {
			return output.NewError(output.CodeBadRequest, "invalid --timeout: "+err.Error(),
				output.ExitForCode(output.CodeBadRequest)).WithCause(err)
		}
		return output.NewError(output.CodeBadConfig, "loading config: "+err.Error(), output.ExitUser).WithCause(err)
	}
	a.wire(cfg)
	return nil
}

// buildTolerant is build()'s fallback for a tolerateBadConfig command: keep the
// config error for the command to report, and wire everything against minimal
// defaults so the command body still has an Auth and a Client to talk to.
func (a *App) buildTolerant(cerr *output.CLIError) {
	a.CfgErr = cerr
	a.wire(&config.Config{BaseURL: defaultBaseURL, Timeout: 30 * time.Second})
}

// wire is the shared construction step, so the tolerant path cannot drift from
// the normal one (notably the output format, which --raw/--terse depend on).
func (a *App) wire(cfg *config.Config) {
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
	return output.UsageError(
		"this command ignores --id/--id-type; a mutation takes its targeting from --payload " +
			`(e.g. --payload '{"movies":[{"ids":{"slug":"gilda-1946"}}]}')`)
}

// baseOpts builds client.Options from the global list/pagination/extended flags.
//
// `extended` in config.toml was a dead key: it parsed, it appeared in `config
// path`, and nothing ever read it. The flag still wins; the file is the
// fallback. The a.Cfg nil check is load-bearing, not defensive — several tests
// build an App from flags alone, without going through build().
func (a *App) baseOpts(auth bool) client.Options {
	extended := a.Flags.Extended
	if extended == "" && a.Cfg != nil {
		extended = a.Cfg.Extended
	}
	return client.Options{
		Extended:  extended,
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
// runCtx is the invocation context, set from cmd.Context() in
// PersistentPreRunE. Nil until then (e.g. in a test that builds an App
// directly), so ctx() falls back to context.Background() rather than
// panicking on a nil Context.
func (a *App) ctx() context.Context {
	if a.runCtx != nil {
		return a.runCtx
	}
	return context.Background()
}

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

// parsePayload validates a --payload JSON string and returns the caller's own
// bytes verbatim.
func parsePayload(s string) (json.RawMessage, error) {
	if s == "" {
		// Name the lookup-vs-mutation split here: this is the error a caller
		// following the old (wrong) --id examples actually hits.
		return nil, output.UsageError(
			"missing required --payload JSON; a mutation takes its targeting from --payload, " +
				"not --id/--id-type (those are lookup flags)")
	}
	return decodeJSON([]byte(s), "--payload")
}

// resolvePayload is the single entry point every payload-taking command uses:
// exactly one of --payload / --payload-file may be set. --payload-file reads
// from the named path, or stdin when the path is "-". The returned bytes are
// exactly what the caller supplied -- decodeJSON validates them (2.4: a
// number like 2^53+1 must reach Trakt exactly as typed) but never re-encodes
// them, so client.go's json.Marshal(opts.Body) on the resulting
// json.RawMessage emits those same bytes unchanged.
func resolvePayload(payload, payloadFile string) (json.RawMessage, error) {
	if payload != "" && payloadFile != "" {
		return nil, output.UsageError(
			"--payload and --payload-file are mutually exclusive")
	}
	if payloadFile == "" {
		return parsePayload(payload)
	}
	data, err := readPayloadFile(payloadFile)
	if err != nil {
		return nil, output.UsageError("reading --payload-file: " + err.Error())
	}
	if len(data) == 0 {
		return nil, output.UsageError(
			"missing required --payload/--payload-file JSON; a mutation takes its targeting from --payload, " +
				"not --id/--id-type (those are lookup flags)")
	}
	return decodeJSON(data, "--payload-file")
}

// readPayloadFile reads path's contents, or stdin when path is "-".
func readPayloadFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// decodeJSON is the shared JSON validation behind both --payload and
// --payload-file, with the source named in the error for a caller who mixed
// up which one they used. It validates through xhttp.DecodeOne (UseNumber, and
// strict about trailing content -- '{"a":1} junk' is still rejected) but
// returns data itself, unmodified: the caller's bytes are what reaches Trakt,
// not a re-encoded value, so a 2^53+1 literal survives exactly as typed.
func decodeJSON(data []byte, source string) (json.RawMessage, error) {
	var v interface{}
	if err := xhttp.DecodeOne(data, &v); err != nil {
		return nil, output.UsageError("invalid " + source + " JSON: " + err.Error())
	}
	return json.RawMessage(data), nil
}
