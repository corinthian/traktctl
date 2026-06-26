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
