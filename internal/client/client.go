// Package client is the HTTP core: it injects required headers, applies
// extended/pagination/filter query params, maps non-2xx responses to typed
// errors with stable exit codes, auto-paginates --all, and refreshes the token
// once on a 401. All command policy that touches HTTP lives here, not in the
// per-command files.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/corinthian/traktctl/internal/cause"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/corinthian/traktctl/internal/xhttp"
)

// BodyLimit bounds every response body traktctl reads, on the API path and the
// OAuth path alike. One constant serves the whole tool: internal/auth imports
// it from here, and client does not import auth, so there is no cycle.
//
// It is the provisional 64 MiB default from contract 2.3 and was not measured
// against a live Trakt account. The bound is not a user setting: no flag, no
// env var, no config key.
const BodyLimit int64 = 64 << 20

// TokenSource supplies the bearer token and can refresh it. Satisfied by
// auth.Manager.
type TokenSource interface {
	Bearer() string
	HasToken() bool
	Refresh(ctx context.Context) error
}

// Client is a configured Trakt HTTP client.
type Client struct {
	http      *http.Client
	baseURL   string
	clientID  string
	userAgent string
	tokens    TokenSource
	bodyLimit int64
	errW      io.Writer
}

// Config configures a Client.
type Config struct {
	BaseURL  string
	ClientID string
	Version  string // traktctl version, for User-Agent
	Timeout  time.Duration
	Tokens   TokenSource
	ErrW     io.Writer
}

// pageCap is the --all runaway guard; --really-all overrides it.
const pageCap = 100

// New builds a Client.
func New(c Config) *Client {
	return &Client{
		http: xhttp.NewClient(xhttp.Options{
			Timeout:   c.Timeout,
			Redirects: xhttp.RedirectPolicy{SameOrigin: true, MaxHops: 10},
		}),
		baseURL:   strings.TrimRight(c.BaseURL, "/"),
		clientID:  c.ClientID,
		userAgent: "traktctl/" + c.Version,
		tokens:    c.Tokens,
		bodyLimit: BodyLimit,
		errW:      c.ErrW,
	}
}

// Options carries per-call request shaping.
type Options struct {
	Extended  string      // --extended value
	Filters   []string    // repeated key=value
	Query     url.Values  // command-set query params
	Page      int         // --page (0 = unset)
	Limit     int         // --limit (0 = unset)
	All       bool        // auto-paginate
	ReallyAll bool        // bypass the page cap
	Auth      bool        // request requires a bearer token
	Body      interface{} // request body for POST/PUT
}

// Result is a successful HTTP outcome.
type Result struct {
	Data       json.RawMessage
	Status     int
	Pagination *output.Pagination
	Endpoint   string
	DurationMS int64
}

// Do executes a request, handling auth refresh, error mapping, and (for GET
// with All) auto-pagination. The returned error is always *output.CLIError.
func (c *Client) Do(ctx context.Context, method, path string, opts Options) (*Result, *output.CLIError) {
	if opts.Auth && c.tokens.Bearer() == "" {
		return nil, &output.CLIError{
			Code: output.CodeAuthRequired, Message: "this command requires authentication",
			Exit: output.ExitAuthMissing, Hint: "Run: traktctl auth login", Endpoint: path,
		}
	}
	if opts.All && method == http.MethodGet {
		return c.doAll(ctx, path, opts)
	}
	return c.doOnce(ctx, method, path, opts, true)
}

// doOnce performs a single request. allowRefresh gates the one-shot 401 retry.
func (c *Client) doOnce(ctx context.Context, method, path string, opts Options, allowRefresh bool) (*Result, *output.CLIError) {
	start := time.Now()
	req, cerr := c.buildRequest(ctx, method, path, opts)
	if cerr != nil {
		return nil, cerr
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, transportError(err, path, time.Since(start))
	}
	// ReadBody closes the body, so it is called exactly once per response and
	// the bytes are reused: the 401 branch below needs them discarded, and
	// mapHTTPError needs them for its snippet.
	body, rerr := xhttp.ReadBody(resp, c.bodyLimit)
	dur := time.Since(start).Milliseconds()
	nonOK := resp.StatusCode < 200 || resp.StatusCode >= 300

	if resp.StatusCode == http.StatusUnauthorized && allowRefresh && c.tokens.HasToken() {
		// A discard that failed is reported, not swallowed: refreshing after a
		// broken read would report the retry's outcome for a request whose own
		// outcome is unknown.
		if rerr != nil {
			return nil, c.bodyError(rerr, method, path, dur)
		}
		if refErr := c.tokens.Refresh(ctx); refErr == nil {
			fmt.Fprintln(c.errW, "[traktctl] access token refreshed")
			return c.doOnce(ctx, method, path, opts, false)
		}
	}

	if rerr != nil {
		// An oversize body on a non-2xx changes nothing about the status: the
		// status classification wins and there are simply no bytes for the
		// snippet hint. A read that broke part-way is different — the response
		// is not reliably complete, and the transport fact is the useful one.
		if nonOK && errors.Is(rerr, xhttp.ErrOversize) {
			return nil, mapHTTPError(resp, nil, path, dur)
		}
		return nil, c.bodyError(rerr, method, path, dur)
	}
	if nonOK {
		return nil, mapHTTPError(resp, body, path, dur)
	}
	// A 2xx that does not hold exactly one JSON value fails here, at response
	// time, rather than at output time as an internal marshal failure.
	var sink json.RawMessage
	if derr := xhttp.DecodeOne(body, &sink); derr != nil {
		return nil, &output.CLIError{
			Code:    output.CodeDecodeError,
			Message: fmt.Sprintf("invalid JSON in response from %s %s: %s", method, path, derr.Error()),
			Exit:    output.ExitInternal, Endpoint: path, DurationMS: dur,
		}
	}
	return &Result{
		Data:       json.RawMessage(body),
		Status:     resp.StatusCode,
		Pagination: parsePagination(resp.Header),
		Endpoint:   path,
		DurationMS: dur,
	}, nil
}

// doAll auto-paginates a list endpoint, merging the per-page arrays.
func (c *Client) doAll(ctx context.Context, path string, opts Options) (*Result, *output.CLIError) {
	merged := []json.RawMessage{}
	page := 1
	if opts.Page > 0 {
		page = opts.Page
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	start := time.Now()
	var lastPag *output.Pagination
	for {
		if !opts.ReallyAll && page-(firstPage(opts))+1 > pageCap {
			return nil, &output.CLIError{
				Code:    output.CodePaginationRunaway,
				Message: fmt.Sprintf("--all exceeded %d pages; pass --really-all to override", pageCap),
				Exit:    output.ExitUser, Endpoint: path,
			}
		}
		pageOpts := opts
		pageOpts.All = false
		pageOpts.Page = page
		pageOpts.Limit = limit
		res, cerr := c.doOnce(ctx, http.MethodGet, path, pageOpts, true)
		if cerr != nil {
			return nil, cerr
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(res.Data, &arr); err != nil {
			// Not an array — return the single payload as-is.
			res.DurationMS = time.Since(start).Milliseconds()
			return res, nil
		}
		merged = append(merged, arr...)
		lastPag = res.Pagination
		if lastPag == nil || lastPag.PageCount == 0 || page >= lastPag.PageCount {
			break
		}
		page++
	}
	out, _ := json.Marshal(merged)
	// The merged result starts where the run started: firstPage(opts), not the
	// mutated pageOpts. Reporting page 1 for a `--page 3 --all` run described a
	// window the caller never asked for.
	pag := &output.Pagination{Page: firstPage(opts), Limit: limit, ItemCount: len(merged)}
	if lastPag != nil {
		pag.PageCount = lastPag.PageCount
		if lastPag.ItemCount > 0 {
			pag.ItemCount = lastPag.ItemCount
		}
	}
	return &Result{Data: out, Status: 200, Pagination: pag, Endpoint: path, DurationMS: time.Since(start).Milliseconds()}, nil
}

// bodyError maps a ReadBody failure. It deliberately does not go through
// Classify: a read that fails part-way wraps io.ErrUnexpectedEOF, which
// Classify reports as Decode, and a broken connection is not a malformed
// response.
func (c *Client) bodyError(err error, method, path string, dur int64) *output.CLIError {
	if errors.Is(err, xhttp.ErrOversize) {
		return &output.CLIError{
			Code:    output.CodeDecodeError,
			Message: fmt.Sprintf("response body from %s %s exceeds the %s bound", method, path, humanBytes(c.bodyLimit)),
			Exit:    output.ExitInternal, Endpoint: path, DurationMS: dur,
		}
	}
	return &output.CLIError{
		Code:    output.CodeTransportFailed,
		Message: fmt.Sprintf("reading response body from %s %s: %s", method, path, err.Error()),
		Exit:    output.ExitTransport, Endpoint: path, DurationMS: dur,
	}
}

// humanBytes renders the bound the way the contract writes it, "64 MiB", and
// falls back to a byte count for anything that is not a whole MiB.
func humanBytes(n int64) string {
	const mib = 1 << 20
	if n >= mib && n%mib == 0 {
		return strconv.FormatInt(n/mib, 10) + " MiB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

func firstPage(opts Options) int {
	if opts.Page > 0 {
		return opts.Page
	}
	return 1
}

// reservedFilterKeys are the query params the paginator owns. A --filter on one
// of these used to be applied after the paginator set it, so `--filter page=2`
// either did nothing or silently fought auto-pagination -- the same
// accepted-and-ignored defect class as the dead `extended` key. `extended` is
// deliberately not reserved: it is a display option, not paginator state.
var reservedFilterKeys = map[string]bool{"page": true, "limit": true}

// buildRequest is the single choke point for doOnce, doAll and every POST/PUT,
// which is why the reserved-key check lives here rather than in Do.
func (c *Client) buildRequest(ctx context.Context, method, path string, opts Options) (*http.Request, *output.CLIError) {
	u := c.baseURL + path
	q := url.Values{}
	for k, vs := range opts.Query {
		for _, v := range vs {
			q.Add(k, v)
		}
	}
	if opts.Extended != "" {
		q.Set("extended", opts.Extended)
	}
	if opts.Page > 0 {
		q.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	for _, f := range opts.Filters {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			return nil, output.UsageError("invalid --filter (want key=value): " + f)
		}
		if reservedFilterKeys[k] {
			return nil, output.UsageError("--filter " + k + " is reserved; use --page/--limit")
		}
		q.Set(k, v)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}

	var bodyReader io.Reader
	if opts.Body != nil {
		b, err := json.Marshal(opts.Body)
		if err != nil {
			return nil, &output.CLIError{Code: output.CodeParseError, Message: "encoding request body: " + err.Error(), Exit: output.ExitInternal}
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, &output.CLIError{Code: output.CodeBadConfig, Message: "building request: " + err.Error(), Exit: output.ExitUser}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.clientID)
	req.Header.Set("User-Agent", c.userAgent)
	// Always attach the bearer when we have one (private profiles need it even
	// for "public" reads); opts.Auth only gates the hard requirement + refresh.
	if tok := c.tokens.Bearer(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func parsePagination(h http.Header) *output.Pagination {
	get := func(k string) int { n, _ := strconv.Atoi(h.Get(k)); return n }
	if h.Get("X-Pagination-Page") == "" {
		return nil
	}
	return &output.Pagination{
		Page:      get("X-Pagination-Page"),
		Limit:     get("X-Pagination-Limit"),
		PageCount: get("X-Pagination-Page-Count"),
		ItemCount: get("X-Pagination-Item-Count"),
	}
}

// transportError classifies a failure to get a response at all. Only a genuine
// deadline keeps TRANSPORT_TIMEOUT; DNS, TLS, refused, a refused redirect and a
// cancelled request are all TRANSPORT_FAILED, which is what stops "connection
// timed out" being reported for a name that does not resolve.
//
// The timeout message says nothing about whether the request was applied: a
// mutation that timed out may well have landed.
func transportError(err error, path string, d time.Duration) *output.CLIError {
	code, msg := output.CodeTransportFailed, "transport error: "+err.Error()
	exit := output.ExitTransport
	switch xhttp.Classify(err) {
	case cause.Timeout:
		code, msg = output.CodeTransportTimeout, "request timed out"
	case cause.Oversize, cause.Decode:
		code, exit = output.CodeDecodeError, output.ExitInternal
	}
	return &output.CLIError{Code: code, Message: msg, Exit: exit, Endpoint: path, DurationMS: d.Milliseconds()}
}

// mapHTTPError converts a non-2xx response into a typed CLIError.
func mapHTTPError(resp *http.Response, body []byte, path string, dur int64) *output.CLIError {
	e := &output.CLIError{HTTPStatus: resp.StatusCode, Exit: output.ExitTrakt, Endpoint: path, DurationMS: dur}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		e.Code = output.CodeAuthExpired
		e.Message = "unauthorized; token invalid or refresh failed"
		e.Hint = "Run: traktctl auth login"
	case http.StatusForbidden:
		e.Code = output.CodeTraktVIPOnly
		e.Message = "forbidden; endpoint may require Trakt VIP or broader scope"
	case http.StatusNotFound:
		e.Code = output.CodeTraktNotFound
		e.Message = "resource not found"
	case 423:
		e.Code = output.CodeTraktLockedUser
		e.Message = "account locked"
	case http.StatusPreconditionFailed, http.StatusUnprocessableEntity:
		e.Code = output.CodeTraktValidation
		e.Message = "request rejected by Trakt (validation)"
	case http.StatusTooManyRequests:
		e.Code = output.CodeTraktRateLimited
		e.Message = "rate limited by Trakt"
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			e.Hint = "Retry-After: " + ra + "s"
		}
	default:
		if resp.StatusCode >= 500 {
			e.Code = output.CodeTraktServer
			e.Message = fmt.Sprintf("Trakt server error (HTTP %d)", resp.StatusCode)
		} else {
			e.Code = output.CodeTraktValidation
			e.Message = fmt.Sprintf("Trakt returned HTTP %d", resp.StatusCode)
		}
	}
	if snippet := strings.TrimSpace(string(body)); snippet != "" && len(snippet) < 300 {
		// Surface a short error body in the hint when present and not noisy.
		if e.Hint == "" {
			e.Hint = snippet
		}
	}
	return e
}
