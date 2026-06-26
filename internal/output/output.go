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
)

// Error-code enum values (the error envelope's `code` field).
const (
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
)

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

// Emit renders a successful result per the configured format.
func (w *Writer) Emit(r *Result) error {
	switch w.Format {
	case FormatRaw:
		return w.writeRaw(r.Data)
	case FormatNDJSON:
		return w.writeNDJSON(r.Data)
	case FormatTerse:
		return w.writeTerse(r)
	default:
		env := Envelope{OK: true, Data: json.RawMessage(r.Data), Meta: r.Meta}
		return w.writeJSON(env)
	}
}

// EmitError renders a CLIError as an error envelope and returns the exit code.
// --raw still emits the structured error (there is no upstream body to pass
// through on most failures); errors are always machine-readable.
func (w *Writer) EmitError(e *CLIError) ExitCode {
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
	if e.Exit == ExitOK {
		return ExitInternal
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
	// Fallback: compact single-line JSON.
	return w.writeCompactLine(r.Data)
}
