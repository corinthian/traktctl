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

// bigInt is 2^53+1, the smallest integer a float64 cannot represent exactly.
// A decode-and-re-encode round trip through interface{} silently rewrites it;
// every rawjson-backed path must not.
const bigInt = "9007199254740993"

// TestRawPreservesKeyOrderAndBigIntegers: --raw indents the bytes without
// decoding them, so non-alphabetical key order and the big integer both
// survive byte-exact.
func TestRawPreservesKeyOrderAndBigIntegers(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatRaw)
	body := json.RawMessage(`{"zulu":1,"alpha":` + bigInt + `,"mike":3}`)
	if err := w.Emit(&Result{Data: body}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, bigInt) {
		t.Errorf("big integer did not survive --raw:\n%s", got)
	}
	z, a, m := strings.Index(got, "zulu"), strings.Index(got, "alpha"), strings.Index(got, "mike")
	if !(z < a && a < m) {
		t.Errorf("key order was not preserved:\n%s", got)
	}
}

// TestNDJSONPreservesBigIntegers: the compacted per-row output must not lose
// precision either.
func TestNDJSONPreservesBigIntegers(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatNDJSON)
	body := json.RawMessage(`[{"n":` + bigInt + `}]`)
	if err := w.Emit(&Result{Data: body}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), bigInt) {
		t.Errorf("big integer did not survive --ndjson: %q", out.String())
	}
}

// TestNDJSONOnBareArraySplitsDirectly: no "records" unwrap step in traktctl --
// a bare top-level array is split as-is.
func TestNDJSONOnBareArraySplitsDirectly(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatNDJSON)
	if err := w.Emit(&Result{Data: json.RawMessage(`[{"a":1},{"a":2},{"a":3}]`)}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %q", len(lines), out.String())
	}
}

// TestNDJSONOnNonArrayEmitsOneLine pins the existing fallback: a non-array
// body renders as a single compact line, not an error.
func TestNDJSONOnNonArrayEmitsOneLine(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatNDJSON)
	if err := w.Emit(&Result{Data: json.RawMessage(`{"a":1}`)}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 line, got %d: %q", len(lines), out.String())
	}
}

// TestTersePreservesBigIntegers: the --terse compact fallback (no Terse
// string set) goes through writeCompactLine too.
func TestTersePreservesBigIntegers(t *testing.T) {
	var out bytes.Buffer
	w := New(&out, &out, FormatTerse)
	body := json.RawMessage(`{"n":` + bigInt + `}`)
	if err := w.Emit(&Result{Data: body}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), bigInt) {
		t.Errorf("big integer did not survive --terse fallback: %q", out.String())
	}
}

// allCodes is every value in the error-code enumeration. A new code added to
// output.go without a codeExit entry fails TestExitForCodeCoversEnum — the
// mapping is the contract, not an afterthought at the call site.
var allCodes = []string{
	CodeBadRequest, CodeBadConfig, CodeAuthRequired, CodeAuthExpired,
	CodeTraktNotFound, CodeTraktValidation, CodeTraktRateLimited,
	CodeTraktVIPOnly, CodeTraktLockedUser, CodeTraktDeactivated,
	CodeTraktServer, CodeTransportTimeout, CodeTransportFailed,
	CodeParseError, CodeDecodeError,
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
		CodeTransportFailed:  ExitTransport,
		CodeParseError:       ExitInternal,
		CodeDecodeError:      ExitInternal,
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
