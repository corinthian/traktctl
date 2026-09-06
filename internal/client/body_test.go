package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/output"
)

// smallLimit shrinks the body bound for a test so the boundary cases do not
// have to move 64 MiB over loopback. BodyLimit itself stays a constant.
func smallLimit(t *testing.T, c *Client, n int64) *Client {
	t.Helper()
	c.bodyLimit = n
	return c
}

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func wantErr(t *testing.T, cerr *output.CLIError, code string, exit output.ExitCode) {
	t.Helper()
	if cerr == nil {
		t.Fatalf("expected %s, got success", code)
	}
	if cerr.Code != code || cerr.Exit != exit {
		t.Fatalf("got %s exit %d (%s), want %s exit %d", cerr.Code, cerr.Exit, cerr.Message, code, exit)
	}
}

// TestHumanBytesRendersBodyLimitAsMiB pins the gap the previous executor
// named: BodyLimit itself (64 MiB) must render as the contract writes it,
// "64 MiB", not a raw byte count -- this is what TestOversizeBodyIsDecodeError
// asserts indirectly via a shrunk bound, but never against the real constant.
func TestHumanBytesRendersBodyLimitAsMiB(t *testing.T) {
	if got := humanBytes(BodyLimit); got != "64 MiB" {
		t.Errorf("humanBytes(BodyLimit) = %q, want %q", got, "64 MiB")
	}
}

func TestOversizeBodyIsDecodeError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"` + strings.Repeat("a", 200) + `"`))
	})
	c := smallLimit(t, newTestClient(t, srv.URL, &fakeTokens{}), 64)
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeDecodeError, output.ExitInternal)
	for _, want := range []string{"64", "GET", "/x"} {
		if !strings.Contains(cerr.Message, want) {
			t.Errorf("message %q does not name %q", cerr.Message, want)
		}
	}
}

func TestExactLimitBodySucceeds(t *testing.T) {
	body := `"` + strings.Repeat("a", 62) + `"` // exactly 64 bytes
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
	c := smallLimit(t, newTestClient(t, srv.URL, &fakeTokens{}), int64(len(body)))
	res, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	if cerr != nil {
		t.Fatalf("exactly the limit must succeed, got %s: %s", cerr.Code, cerr.Message)
	}
	if string(res.Data) != body {
		t.Errorf("body altered: %q", res.Data)
	}
}

func TestNonJSON2xxIsDecodeError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>nope</html>")) })
	_, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeDecodeError, output.ExitInternal)
}

func TestValidPrefixPlusGarbageIsDecodeError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"a":1} junk`)) })
	_, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeDecodeError, output.ExitInternal)
}

func TestTruncatedBodyIsDecodeError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"a":`)) })
	_, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeDecodeError, output.ExitInternal)
}

// TestNonJSON401KeepsHTTPStatus pins 2.3's status-first rule: an HTML 401 is an
// auth failure, never a decode failure.
func TestNonJSON401KeepsHTTPStatus(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("<html>signed out</html>"))
	})
	_, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeAuthExpired, output.ExitTrakt)
	if cerr.HTTPStatus != 401 {
		t.Errorf("http_status = %d, want 401", cerr.HTTPStatus)
	}
}

// TestOversizeOn404KeepsTraktCode pins the same rule for an oversize body: the
// status classification wins and there are no bytes for a snippet hint.
func TestOversizeOn404KeepsTraktCode(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(strings.Repeat("x", 500)))
	})
	c := smallLimit(t, newTestClient(t, srv.URL, &fakeTokens{}), 64)
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeTraktNotFound, output.ExitTrakt)
	if cerr.Hint != "" {
		t.Errorf("oversize body must not produce a snippet hint, got %q", cerr.Hint)
	}
}

func TestEmptyAnd204BodiesAreNotDecodeErrors(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"empty": func(w http.ResponseWriter, r *http.Request) {},
		"204":   func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := serve(t, h)
			res, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
			if cerr != nil {
				t.Fatalf("empty body must succeed, got %s: %s", cerr.Code, cerr.Message)
			}
			if len(strings.TrimSpace(string(res.Data))) != 0 {
				t.Errorf("expected an empty payload, got %q", res.Data)
			}
		})
	}
}

// abortMidBody promises more bytes than it writes and then kills the
// connection, so the read fails part-way.
func abortMidBody(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "4096")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"a":1`))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	panic(http.ErrAbortHandler)
}

// TestBodyReadFailureIsTransportFailed is the case that must not go through
// Classify: a truncated read wraps io.ErrUnexpectedEOF, which Classify calls
// Decode, and calling a broken connection a decode failure is wrong.
func TestBodyReadFailureIsTransportFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(abortMidBody))
	srv.Config.ErrorLog = quietLog()
	defer srv.Close()
	_, cerr := newTestClient(t, srv.URL, &fakeTokens{}).Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeTransportFailed, output.ExitTransport)
}

// TestRefreshDiscardIsBounded covers the 401 discard: it is read under the
// bound, and a failure to discard is reported rather than swallowed into a
// refresh attempt.
func TestRefreshDiscardIsBounded(t *testing.T) {
	var served int
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		served++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(strings.Repeat("x", 500)))
	})
	tok := &fakeTokens{bearer: "tok", has: true}
	c := smallLimit(t, newTestClient(t, srv.URL, tok), 64)
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeDecodeError, output.ExitInternal)
	if tok.refreshed {
		t.Error("a failed discard must not be followed by a refresh")
	}
	if served != 1 {
		t.Errorf("server hit %d times, want 1", served)
	}
}

func TestDNSTLSRefusedAllBecomeTransportFailed(t *testing.T) {
	refused := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refusedURL := refused.URL
	refused.Close()

	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	tlsSrv.Config.ErrorLog = quietLog()
	defer tlsSrv.Close()

	cases := map[string]string{
		"dns":     "http://traktctl-no-such-host.invalid",
		"refused": refusedURL,
		"tls":     tlsSrv.URL, // default transport does not trust the test CA
	}
	for name, base := range cases {
		t.Run(name, func(t *testing.T) {
			c := New(Config{BaseURL: base, ClientID: "cid", Version: "test",
				Timeout: 5 * time.Second, Tokens: &fakeTokens{}, ErrW: discard{}})
			_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
			wantErr(t, cerr, output.CodeTransportFailed, output.ExitTransport)
		})
	}
}

func TestTimeoutStaysTransportTimeout(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) })
	c := New(Config{BaseURL: srv.URL, ClientID: "cid", Version: "test",
		Timeout: 50 * time.Millisecond, Tokens: &fakeTokens{}, ErrW: discard{}})
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	wantErr(t, cerr, output.CodeTransportTimeout, output.ExitTransport)
	if !strings.Contains(cerr.Message, "timed out") {
		t.Errorf("message %q does not say the request timed out", cerr.Message)
	}
}
