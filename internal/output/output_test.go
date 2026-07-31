package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestEmitEmptyBody covers the HTTP 204 / empty-body path: a non-nil empty
// RawMessage must produce a success envelope with data:null, not a marshal
// error.
func TestEmitEmptyBody(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatJSON)
	// io.ReadAll yields a non-nil empty slice on a 204 — reproduce that exactly.
	if err := w.Emit(&Result{Data: json.RawMessage([]byte{}), Meta: &Meta{Endpoint: "/x"}}); err != nil {
		t.Fatalf("emit empty body failed: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Errorf("expected ok:true on empty body, got %+v", env)
	}
	if env.Data != nil {
		t.Errorf("expected data:null on empty body, got %v", env.Data)
	}
}

func TestEmitTerseEmptyBody(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatTerse)
	if err := w.Emit(&Result{Data: json.RawMessage([]byte{})}); err != nil {
		t.Fatalf("terse empty body failed: %v", err)
	}
	if strings.TrimSpace(out.String()) != "ok" {
		t.Errorf("expected 'ok', got %q", out.String())
	}
}

func TestEmitNDJSON(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatNDJSON)
	if err := w.Emit(&Result{Data: json.RawMessage(`[{"a":1},{"a":2}]`)}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 ndjson lines, got %d: %q", len(lines), out.String())
	}
}

// allCodes is every value in the error-code enumeration. A new code added to
// output.go without a codeExit entry fails TestExitForCodeCoversEnum — the
// mapping is the contract, not an afterthought at the call site.
var allCodes = []string{
	CodeBadRequest, CodeBadConfig, CodeAuthRequired, CodeAuthExpired,
	CodeTraktNotFound, CodeTraktValidation, CodeTraktRateLimited,
	CodeTraktVIPOnly, CodeTraktLockedUser, CodeTraktDeactivated,
	CodeTraktServer, CodeTransportTimeout, CodeParseError,
	CodePaginationRunaway, CodeNotApplied,
}

func TestExitForCodeCoversEnum(t *testing.T) {
	for _, c := range allCodes {
		if _, ok := codeExit[c]; !ok {
			t.Errorf("code %q has no exit-class mapping", c)
		}
	}
	if len(codeExit) != len(allCodes) {
		t.Errorf("codeExit has %d entries, enum has %d — one of them is stale", len(codeExit), len(allCodes))
	}
}

func TestExitForCode(t *testing.T) {
	want := map[string]ExitCode{
		CodeBadRequest:       ExitUser,
		CodeBadConfig:        ExitUser,
		CodeAuthRequired:     ExitAuthMissing,
		CodeAuthExpired:      ExitTrakt,
		CodeTraktNotFound:    ExitTrakt,
		CodeTransportTimeout: ExitTransport,
		CodeParseError:       ExitInternal,
		CodeNotApplied:       ExitNotApplied,
	}
	for code, exp := range want {
		if got := ExitForCode(code); got != exp {
			t.Errorf("ExitForCode(%q) = %d, want %d", code, got, exp)
		}
	}
	// A code outside the enumeration is a traktctl bug, not a user error.
	if got := ExitForCode("NOT_A_REAL_CODE"); got != ExitInternal {
		t.Errorf("ExitForCode(unknown) = %d, want %d", got, ExitInternal)
	}
}

// TestUsageErrorContract pins the usage family: BAD_REQUEST, exit 1, and an
// ok:false envelope on stdout (BUG-2's contract for consumers).
func TestUsageErrorContract(t *testing.T) {
	e := UsageErrorHint("unknown flag: --json", "run `traktctl --help`")
	if e.Code != CodeBadRequest {
		t.Errorf("code = %q, want %q", e.Code, CodeBadRequest)
	}
	if e.Exit != ExitUser {
		t.Errorf("exit = %d, want %d", e.Exit, ExitUser)
	}

	var out, errW bytes.Buffer
	code := New(&out, &errW, FormatJSON).EmitError(e)
	if code != ExitUser {
		t.Errorf("EmitError exit = %d, want %d", code, ExitUser)
	}
	if errW.Len() != 0 {
		t.Errorf("error envelope leaked to stderr: %s", errW.String())
	}
	var env Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if env.OK || env.Error == nil || env.Error.Code != CodeBadRequest || env.Error.Hint == "" {
		t.Errorf("envelope = %+v, want ok:false BAD_REQUEST with a hint", env)
	}
}

// TestEmitErrorDerivesExit covers the central mapping doing the work when a
// call site sets no exit, and an explicit exit still winning.
func TestEmitErrorDerivesExit(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatJSON)

	if got := w.EmitError(&CLIError{Code: CodeAuthRequired, Message: "x"}); got != ExitAuthMissing {
		t.Errorf("derived exit = %d, want %d", got, ExitAuthMissing)
	}
	if got := w.EmitError(&CLIError{Code: "MYSTERY", Message: "x"}); got != ExitInternal {
		t.Errorf("unknown-code exit = %d, want %d", got, ExitInternal)
	}
	// An explicit exit is a deliberate override (an operational failure under
	// a config code) and must not be rewritten by the map.
	if got := w.EmitError(&CLIError{Code: CodeBadConfig, Message: "revoke failed", Exit: ExitInternal}); got != ExitInternal {
		t.Errorf("explicit exit = %d, want %d", got, ExitInternal)
	}
}
