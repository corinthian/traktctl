// Package output defines the JSON envelope, meta block, exit-code mapping, and
// the formatters (default JSON, --raw, --ndjson, --terse) every command emits
// through. It is the single contract: commands never write to stdout directly.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// ExitCode is the process exit status. Stable per the spec's error model.
type ExitCode int

const (
	ExitOK          ExitCode = 0 // success
	ExitUser        ExitCode = 1 // bad flags, missing args, invalid config
	ExitTrakt       ExitCode = 2 // Trakt returned non-2xx
	ExitTransport   ExitCode = 3 // TLS, DNS, timeout
	ExitInternal    ExitCode = 4 // internal traktctl error
	ExitAuthMissing ExitCode = 5 // auth required and never logged in
	ExitNotApplied  ExitCode = 6 // Trakt returned 2xx but applied nothing
)

// Error-code enum values (the error envelope's `code` field).
const (
	// CodeBadRequest is the usage family: anything the caller typed wrong —
	// unknown flag, unknown command, missing subcommand, missing/invalid
	// required flag. Matches plexctl v2's BAD_REQUEST. Distinct from
	// CodeBadConfig so a consumer routing on `error.code` sends a typo'd flag
	// to usage help, not to config/auth troubleshooting.
	CodeBadRequest = "BAD_REQUEST"
	// CodeBadConfig is the config family only: an unreadable/invalid config
	// file, a missing credential in env/config.toml. Never a usage error.
	CodeBadConfig         = "BAD_CONFIG"
	CodeAuthRequired      = "AUTH_REQUIRED"
	CodeAuthExpired       = "AUTH_EXPIRED"
	CodeTraktNotFound     = "TRAKT_NOT_FOUND"
	CodeTraktValidation   = "TRAKT_VALIDATION"
	CodeTraktRateLimited  = "TRAKT_RATE_LIMITED"
	CodeTraktVIPOnly      = "TRAKT_VIP_ONLY"
	CodeTraktLockedUser   = "TRAKT_LOCKED_USER"
	CodeTraktDeactivated  = "TRAKT_DEACTIVATED"
	CodeTraktServer       = "TRAKT_SERVER_ERROR"
	CodeTransportTimeout  = "TRANSPORT_TIMEOUT"
	CodeParseError        = "PARSE_ERROR"
	CodePaginationRunaway = "PAGINATION_RUNAWAY"
	// CodeNotApplied: Trakt accepted the request (2xx) and applied none of it.
	// Distinct from TRAKT_NOT_FOUND, which is HTTP-404 semantics.
	CodeNotApplied = "NOT_APPLIED"
)

// codeExit is the central code -> exit-class mapping: the one place the
// pairing lives, so a new call site cannot invent its own exit number for an
// existing code. A code absent from this map is a bug and exits ExitInternal.
//
// A few call sites still pass an explicit Exit that deliberately differs (an
// operational failure reported under a config code, e.g. a failed revoke),
// and an explicit non-zero Exit always wins — the map is the default, not a
// veto. New code should prefer the constructors below.
var codeExit = map[string]ExitCode{
	CodeBadRequest:        ExitUser,
	CodeBadConfig:         ExitUser,
	CodePaginationRunaway: ExitUser,
	CodeAuthRequired:      ExitAuthMissing,
	CodeAuthExpired:       ExitTrakt,
	CodeTraktNotFound:     ExitTrakt,
	CodeTraktValidation:   ExitTrakt,
	CodeTraktRateLimited:  ExitTrakt,
	CodeTraktVIPOnly:      ExitTrakt,
	CodeTraktLockedUser:   ExitTrakt,
	CodeTraktDeactivated:  ExitTrakt,
	CodeTraktServer:       ExitTrakt,
	CodeTransportTimeout:  ExitTransport,
	CodeParseError:        ExitInternal,
	CodeNotApplied:        ExitNotApplied,
}

// ExitForCode returns the exit class an error code maps to. Unknown codes are
// ExitInternal: emitting a code outside the enumeration is a traktctl bug.
func ExitForCode(code string) ExitCode {
	if e, ok := codeExit[code]; ok {
		return e
	}
	return ExitInternal
}

// UsageError builds a usage-family (BAD_REQUEST) error for anything the caller
// typed wrong. The exit class comes from codeExit, so no call site names an
// exit number.
func UsageError(msg string) *CLIError {
	return &CLIError{Code: CodeBadRequest, Message: msg, Exit: ExitForCode(CodeBadRequest)}
}

// UsageErrorHint is UsageError with the recovery hint filled in.
func UsageErrorHint(msg, hint string) *CLIError {
	e := UsageError(msg)
	e.Hint = hint
	return e
}

// Envelope is the standard success/error wrapper.
type Envelope struct {
	OK    bool        `json:"ok"`
	Data  interface{} `json:"data,omitempty"`
	Error *ErrorBody  `json:"error,omitempty"`
	Meta  *Meta       `json:"meta,omitempty"`
}

// ErrorBody is the structured error payload.
type ErrorBody struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

// Meta is the per-call metadata block.
type Meta struct {
	Endpoint        string      `json:"endpoint,omitempty"`
	DurationMS      int64       `json:"duration_ms"`
	TraktAPIVersion string      `json:"trakt_api_version,omitempty"`
	Pagination      *Pagination `json:"pagination,omitempty"`

	// Partial marks a mutation that applied some items but not all. The items
	// Trakt refused are surfaced here — NotFound for add/remove, SkippedIDs for
	// reorder — so a partial success cannot read as a total one.
	Partial    bool            `json:"partial,omitempty"`
	NotFound   json.RawMessage `json:"not_found,omitempty"`
	SkippedIDs json.RawMessage `json:"skipped_ids,omitempty"`
}

// Pagination mirrors Trakt's X-Pagination-* response headers.
type Pagination struct {
	Page      int `json:"page"`
	Limit     int `json:"limit"`
	PageCount int `json:"page_count"`
	ItemCount int `json:"item_count"`
}

// CLIError is a typed error carrying everything needed to build an error
// envelope and pick an exit code. Every failure path returns one of these.
type CLIError struct {
	Code       string
	Message    string
	HTTPStatus int
	Hint       string
	Exit       ExitCode
	Endpoint   string
	DurationMS int64

	// RawBody is an upstream 2xx body that --raw must still pass through
	// verbatim even though this is an error. Only NOT_APPLIED sets it: Trakt
	// answered successfully and the body is the evidence a --raw caller wants.
	// Under --raw the verdict rides the exit code, which is raw mode's only
	// verdict channel (it has no `ok:` field).
	RawBody json.RawMessage
}

func (e *CLIError) Error() string { return e.Message }

// NewError builds a CLIError.
func NewError(code, msg string, exit ExitCode) *CLIError {
	return &CLIError{Code: code, Message: msg, Exit: exit}
}

// Format selects how command data is rendered.
type Format int

const (
	FormatJSON   Format = iota // default: full envelope, pretty
	FormatRaw                  // Trakt response untouched
	FormatNDJSON               // one object per line (list commands)
	FormatTerse                // one-line plain-English summary
)

// Writer renders results and errors to the given streams honoring the format.
type Writer struct {
	Out    io.Writer
	Err    io.Writer
	Format Format
}

// New returns a Writer.
func New(out, errW io.Writer, f Format) *Writer {
	return &Writer{Out: out, Err: errW, Format: f}
}

// Result is a successful command outcome ready to render.
type Result struct {
	Data json.RawMessage // raw Trakt body (object or array)
	Meta *Meta
	// Terse is an optional one-line human summary used by FormatTerse. When
	// empty under --terse, Writer falls back to a compact JSON line.
	Terse string
}

// Emit renders a successful result per the configured format. An empty body
// (e.g. HTTP 204) is normalized to nil so the JSON path emits `data:null`
// rather than failing to marshal an empty RawMessage.
// A write failure is returned as a typed CLIError so it stays inside the error
// model: the caller's catch-all treats an untyped error as a usage error, and
// a broken stdout is not the caller's typo.
func (w *Writer) Emit(r *Result) error {
	if len(r.Data) == 0 {
		r.Data = nil
	}
	var err error
	switch w.Format {
	case FormatRaw:
		err = w.writeRaw(r.Data)
	case FormatNDJSON:
		err = w.writeNDJSON(r.Data)
	case FormatTerse:
		err = w.writeTerse(r)
	default:
		env := Envelope{OK: true, Data: json.RawMessage(r.Data), Meta: r.Meta}
		err = w.writeJSON(env)
	}
	if err != nil {
		return NewError(CodeParseError, "writing output: "+err.Error(), ExitInternal)
	}
	return nil
}

// EmitError renders a CLIError as an error envelope and returns the exit code.
// --raw still emits the structured error (there is no upstream body to pass
// through on most failures); errors are always machine-readable.
//
// The exception is an error carrying RawBody (NOT_APPLIED): there Trakt did
// answer 2xx, so --raw keeps its passthrough promise and emits the upstream
// body untouched, leaving the exit code to carry the verdict.
func (w *Writer) EmitError(e *CLIError) ExitCode {
	if w.Format == FormatRaw && len(e.RawBody) > 0 {
		_ = w.writeRaw(e.RawBody)
		return e.exitOrInternal()
	}
	env := Envelope{
		OK: false,
		Error: &ErrorBody{
			Code:       e.Code,
			Message:    e.Message,
			HTTPStatus: e.HTTPStatus,
			Hint:       e.Hint,
		},
	}
	if e.Endpoint != "" || e.DurationMS != 0 {
		env.Meta = &Meta{Endpoint: e.Endpoint, DurationMS: e.DurationMS}
	}
	_ = w.writeJSON(env)
	return e.exitOrInternal()
}

// exitOrInternal guards against a zero-value Exit silently meaning success:
// an error that never set one derives it from its code via the central map,
// and an unmapped code lands on ExitInternal.
func (e *CLIError) exitOrInternal() ExitCode {
	if e.Exit == ExitOK {
		return ExitForCode(e.Code)
	}
	return e.Exit
}

func (w *Writer) writeJSON(v interface{}) error {
	enc := json.NewEncoder(w.Out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeRaw passes the Trakt body through verbatim (pretty-printed if it parses).
func (w *Writer) writeRaw(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		_, werr := w.Out.Write(append([]byte(data), '\n'))
		return werr
	}
	return w.writeJSON(v)
}

// writeNDJSON emits one line per element of a top-level array. A non-array body
// is emitted as a single line.
func (w *Writer) writeNDJSON(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return w.writeCompactLine(data)
	}
	for _, el := range arr {
		if err := w.writeCompactLine(el); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) writeCompactLine(raw json.RawMessage) error {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	enc := json.NewEncoder(w.Out)
	enc.SetEscapeHTML(false)
	return enc.Encode(v) // Encode appends a newline
}

func (w *Writer) writeTerse(r *Result) error {
	if r.Terse != "" {
		_, err := fmt.Fprintln(w.Out, r.Terse)
		return err
	}
	if len(r.Data) == 0 {
		_, err := fmt.Fprintln(w.Out, "ok")
		return err
	}
	// Fallback: compact single-line JSON.
	return w.writeCompactLine(r.Data)
}
