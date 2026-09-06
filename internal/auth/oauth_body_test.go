package auth

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/xhttp"
)

// oauthServer serves h and keeps httptest's expected connection errors out of
// the test output.
func oauthServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	t.Cleanup(srv.Close)
	return srv
}

// abortMidBody promises more bytes than it writes and then kills the
// connection, so the read fails part-way.
func abortMidBody(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Length", "4096")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"access_token":"a`))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	panic(http.ErrAbortHandler)
}

func bodyHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}
}

func small(t *testing.T, m *Manager, n int64) *Manager {
	t.Helper()
	m.bodyLimit = n
	return m
}

func mustFail(t *testing.T, err error, what string) error {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error, got nil", what)
	}
	return err
}

func noTokenFile(t *testing.T, m *Manager) {
	t.Helper()
	if _, serr := os.Stat(m.store.filePath); serr == nil {
		t.Errorf("a failed read reached store.save: %s exists", m.store.filePath)
	}
}

// TestPostTokenReadFailureIsReported is the red step: postToken discarded the
// read error and decoded a partial body.
func TestPostTokenReadFailureIsReported(t *testing.T) {
	srv := oauthServer(t, abortMidBody)
	m := newTestManager(t, srv.URL)
	err := mustFail(t, m.Refresh(context.Background()), "refresh over a broken read")
	if c := xhttp.Classify(err); c.String() == "decode" || c.String() == "oversize" {
		t.Errorf("a broken read classified as %s, want a transport cause", c)
	}
	noTokenFile(t, m)
	if m.tok.AccessToken != "tok" {
		t.Errorf("the in-memory token was replaced by a partial body: %+v", m.tok)
	}
}

func TestPostTokenTruncatedBodyIsDecodeError(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, `{"access_token":"a"`))
	m := newTestManager(t, srv.URL)
	mustFail(t, m.Refresh(context.Background()), "refresh over a truncated body")
	noTokenFile(t, m)
}

func TestPostTokenValidPrefixPlusGarbageRejected(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, `{"access_token":"a"} junk`))
	m := newTestManager(t, srv.URL)
	mustFail(t, m.Refresh(context.Background()), "refresh over a garbage-suffixed body")
	noTokenFile(t, m)
}

func TestPostTokenOversizeIsRejected(t *testing.T) {
	big, _ := json.Marshal(map[string]string{"access_token": strings.Repeat("a", 500)})
	srv := oauthServer(t, bodyHandler(200, string(big)))
	m := small(t, newTestManager(t, srv.URL), 64)
	err := mustFail(t, m.Refresh(context.Background()), "refresh over an oversize body")
	if !isOversize(err) {
		t.Errorf("error %v does not wrap xhttp.ErrOversize", err)
	}
	noTokenFile(t, m)
}

func TestRequestDeviceCodeTruncatedBodyRejected(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, `{"device_code":"d"`))
	m := newTestManager(t, srv.URL)
	if _, err := m.requestDeviceCode(context.Background()); err == nil {
		t.Fatal("expected an error for a truncated device-code body")
	}
}

func TestRequestDeviceCodeOversizeRejected(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, `{"device_code":"`+strings.Repeat("d", 500)+`"}`))
	m := small(t, newTestManager(t, srv.URL), 64)
	_, err := m.requestDeviceCode(context.Background())
	if !isOversize(mustFail(t, err, "device code over the bound")) {
		t.Errorf("error %v does not wrap xhttp.ErrOversize", err)
	}
}

func TestTryDeviceTokenTruncatedBodyRejected(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, `{"access_token":"a"`))
	m := newTestManager(t, srv.URL)
	tok, _, err := m.tryDeviceToken(context.Background(), map[string]string{})
	mustFail(t, err, "device token over a truncated body")
	if tok != nil {
		t.Errorf("a truncated body produced a token: %+v", tok)
	}
}

// TestTryDeviceTokenNon200DiscardIsBounded pins the poll path: the non-200
// body is discarded under the bound, and a discard failure is an error rather
// than a clean "keep polling".
func TestTryDeviceTokenNon200DiscardIsBounded(t *testing.T) {
	srv := oauthServer(t, bodyHandler(http.StatusBadRequest, strings.Repeat("x", 500)))
	m := small(t, newTestManager(t, srv.URL), 64)
	_, _, err := m.tryDeviceToken(context.Background(), map[string]string{})
	if !isOversize(mustFail(t, err, "oversize non-200 poll body")) {
		t.Errorf("error %v does not wrap xhttp.ErrOversize", err)
	}
}

// TestRevokeDiscardIsBounded, with the existing Revoke tests, pins Revoke's
// contract: local state is cleared only once Trakt confirms a clean 200.
func TestRevokeDiscardIsBounded(t *testing.T) {
	srv := oauthServer(t, bodyHandler(200, strings.Repeat("x", 500)))
	m := small(t, newTestManager(t, srv.URL), 64)
	mustFail(t, m.Revoke(context.Background()), "revoke over an oversize body")
	if m.tok == nil || m.tok.AccessToken != "tok" {
		t.Errorf("a failed revoke cleared the in-memory token: %+v", m.tok)
	}
}

// TestNoTokenWrittenOnAnyReadFailure sweeps every read site at once.
func TestNoTokenWrittenOnAnyReadFailure(t *testing.T) {
	for name, h := range map[string]http.HandlerFunc{
		"broken":    abortMidBody,
		"truncated": bodyHandler(200, `{"access_token":"a"`),
		"garbage":   bodyHandler(200, `{"access_token":"a"} junk`),
		"oversize":  bodyHandler(200, `{"access_token":"`+strings.Repeat("a", 500)+`"}`),
	} {
		t.Run(name, func(t *testing.T) {
			srv := oauthServer(t, h)
			m := small(t, newTestManager(t, srv.URL), 64)
			mustFail(t, m.Refresh(context.Background()), name)
			noTokenFile(t, m)
		})
	}
}
