package commands

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// bigIntLiteral is 2^53+1, the smallest integer a float64 cannot represent
// exactly.
const bigIntLiteral = "9007199254740993"

// TestResolvePayloadFileFromStdin covers --payload-file - reading stdin.
func TestResolvePayloadFileFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()

	go func() {
		w.Write([]byte(`{"a":1}`))
		w.Close()
	}()

	got, err := resolvePayload("", "-")
	if err != nil {
		t.Fatalf("resolvePayload: %v", err)
	}
	want, _ := parsePayload(`{"a":1}`)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolvePayload(stdin) = %v, want %v", got, want)
	}
}

// TestResolvePayloadMutuallyExclusive covers --payload and --payload-file
// both set: that's a usage error, not a silent pick-one.
func TestResolvePayloadMutuallyExclusive(t *testing.T) {
	if _, err := resolvePayload(`{"a":1}`, "somefile.json"); err == nil {
		t.Fatal("expected error when both --payload and --payload-file are set")
	}
}

// TestResolvePayloadFileRoundTripsSameBodyAsPayload covers the file variant
// producing the identical decoded body as the equivalent --payload string.
func TestResolvePayloadFileRoundTripsSameBodyAsPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.json")
	body := `{"movies":[{"ids":{"slug":"gilda-1946"}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp payload file: %v", err)
	}

	fromFile, err := resolvePayload("", path)
	if err != nil {
		t.Fatalf("resolvePayload(file): %v", err)
	}
	fromInline, err := resolvePayload(body, "")
	if err != nil {
		t.Fatalf("resolvePayload(inline): %v", err)
	}
	if !reflect.DeepEqual(fromFile, fromInline) {
		t.Errorf("file variant = %v, inline variant = %v; want equal", fromFile, fromInline)
	}
}

// TestPayloadRejectsTrailingContent: the UseNumber/DecodeOne migration must
// not newly accept what plain json.Unmarshal always rejected -- an object
// followed by trailing content is still BAD_REQUEST.
func TestPayloadRejectsTrailingContent(t *testing.T) {
	if _, err := parsePayload(`{"a":1} junk`); err == nil {
		t.Fatal("parsePayload with trailing content = nil, want an error")
	}
}

// TestNonObjectPayloadStillRejected: a --payload that is not a JSON object.
// traktctl's decodeJSON never enforced an object shape (no analogue of
// arrctl's g_command.go:155 check exists here), so a valid non-object JSON
// value is accepted and passed through verbatim -- pinned so a later change
// does not silently start rejecting it without a corresponding contract note.
func TestNonObjectPayloadStillRejected(t *testing.T) {
	got, err := parsePayload(`[1,2,3]`)
	if err != nil {
		t.Fatalf("parsePayload(non-object) = %v, want success (no object check exists in traktctl)", err)
	}
	if string(got) != `[1,2,3]` {
		t.Errorf("parsePayload(non-object) = %s, want the bytes unchanged", got)
	}
}

// TestPayloadPreservesBigIntegers asserts the actual wire body: a --payload
// carrying 2^53+1 must reach Trakt byte-exact, not re-encoded through a
// float64. This is the assertion that failed before the RawMessage migration.
func TestPayloadPreservesBigIntegers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"added":{"movies":1}}`))
	}))
	defer srv.Close()

	payload := `{"movies":[{"ids":{"trakt":` + bigIntLiteral + `}}]}`
	if _, err := runRoot(t, "--client-id", "cid", "--access-token", "tok", "--base-url", srv.URL,
		"sync", "collection", "add", "--payload", payload); err != nil {
		t.Fatalf("sync collection add: %v", err)
	}
	if !strings.Contains(gotBody, bigIntLiteral) {
		t.Errorf("wire body lost precision: %s", gotBody)
	}
}

// TestPayloadFileSameGuarantees: --payload-file must carry the same
// byte-exact, trailing-content-strict guarantees as --payload.
func TestPayloadFileSameGuarantees(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")

	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"added":{"movies":1}}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "body.json")
	payload := `{"movies":[{"ids":{"trakt":` + bigIntLiteral + `}}]}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runRoot(t, "--client-id", "cid", "--access-token", "tok", "--base-url", srv.URL,
		"sync", "collection", "add", "--payload-file", path); err != nil {
		t.Fatalf("sync collection add --payload-file: %v", err)
	}
	if !strings.Contains(gotBody, bigIntLiteral) {
		t.Errorf("wire body lost precision: %s", gotBody)
	}

	if _, err := runRoot(t, "--client-id", "cid", "--access-token", "tok", "--base-url", srv.URL,
		"sync", "collection", "add", "--payload-file", path+".trailing"); err == nil {
		t.Fatal("expected an error for a missing --payload-file path")
	}
}
