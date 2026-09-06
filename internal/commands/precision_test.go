package commands

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// bigIntLiteral2 is 2^53+1, the smallest integer a float64 cannot represent
// exactly. payload_test.go already has bigIntLiteral for the request-body
// ingress path; this file covers the response-body egress path instead, so a
// distinct name avoids a duplicate declaration in the same package.
const bigIntLiteral2 = "9007199254740993"

// runFormatted drives the real command tree the way main does, capturing raw
// stdout rather than decoding an envelope: --raw and --ndjson output is not a
// single JSON envelope, so runRoot's json.Unmarshal would fail before the
// literal could even be inspected.
func runFormatted(t *testing.T, args ...string) string {
	t.Helper()
	root, app := NewRoot()
	var out bytes.Buffer
	app.Out = output.New(&out, io.Discard, output.FormatJSON)
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return out.String()
}

// bigIntServer answers any request with a single object holding the 2^53+1
// literal, standing in for a Trakt response id field.
func bigIntServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":` + bigIntLiteral2 + `}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestRawFormatPreservesBigIntegerThroughRealCommand pins E19/E12: a 2^53+1
// value in a Trakt response survives client.Do's decode and output.Emit's
// --raw rendering byte-for-byte, exercised through the real command tree
// (rawjson end to end) rather than by constructing output.Result by hand as
// the output package's own unit tests do.
func TestRawFormatPreservesBigIntegerThroughRealCommand(t *testing.T) {
	srv := bigIntServer(t)
	got := runFormatted(t, "--client-id", "cid", "--base-url", srv.URL, "--raw",
		"search", "query", "--type", "movie", "--q", "x")
	if !strings.Contains(got, bigIntLiteral2) {
		t.Fatalf("--raw stdout = %q, want it to contain the literal %s", got, bigIntLiteral2)
	}
	if strings.Contains(got, "9007199254740992") {
		t.Fatalf("--raw stdout = %q, the literal rounded through float64", got)
	}
}

// TestNDJSONFormatPreservesBigIntegerThroughRealCommand is the --ndjson
// counterpart. The endpoint returns a bare object, not an array, so per
// TestNDJSONOnNonArrayEmitsOneLine (output package) it renders as one line.
func TestNDJSONFormatPreservesBigIntegerThroughRealCommand(t *testing.T) {
	srv := bigIntServer(t)
	got := runFormatted(t, "--client-id", "cid", "--base-url", srv.URL, "--ndjson",
		"search", "query", "--type", "movie", "--q", "x")
	if !strings.Contains(got, bigIntLiteral2) {
		t.Fatalf("--ndjson stdout = %q, want it to contain the literal %s", got, bigIntLiteral2)
	}
	if strings.Contains(got, "9007199254740992") {
		t.Fatalf("--ndjson stdout = %q, the literal rounded through float64", got)
	}
}

// TestTerseFormatPreservesBigIntegerThroughRealCommand is the compact/terse
// counterpart: no explicit summary is available for a search hit shaped this
// way, so the writer falls back to compact JSON, which must still carry the
// literal precisely.
func TestTerseFormatPreservesBigIntegerThroughRealCommand(t *testing.T) {
	srv := bigIntServer(t)
	got := runFormatted(t, "--client-id", "cid", "--base-url", srv.URL, "--terse",
		"search", "query", "--type", "movie", "--q", "x")
	if !strings.Contains(got, bigIntLiteral2) {
		t.Fatalf("--terse stdout = %q, want it to contain the literal %s", got, bigIntLiteral2)
	}
	if strings.Contains(got, "9007199254740992") {
		t.Fatalf("--terse stdout = %q, the literal rounded through float64", got)
	}
}
