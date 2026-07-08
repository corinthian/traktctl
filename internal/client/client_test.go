package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/output"
)

// fakeTokens is a static TokenSource for tests.
type fakeTokens struct {
	bearer    string
	has       bool
	refreshed bool
}

func (f *fakeTokens) Bearer() string { return f.bearer }
func (f *fakeTokens) HasToken() bool { return f.has }
func (f *fakeTokens) Refresh(context.Context) error {
	f.refreshed = true
	f.bearer = "refreshed-token"
	return nil
}

func newTestClient(t *testing.T, base string, tok TokenSource) *Client {
	t.Helper()
	return New(Config{BaseURL: base, ClientID: "cid", Version: "test", Timeout: 5 * time.Second, Tokens: tok, ErrW: discard{}})
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestRequiredHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, &fakeTokens{bearer: "tok", has: true})
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
	if cerr != nil {
		t.Fatalf("unexpected error: %v", cerr)
	}
	for _, h := range []string{"Trakt-Api-Version", "Trakt-Api-Key", "User-Agent"} {
		if got.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	if got.Get("Authorization") != "Bearer tok" {
		t.Errorf("bearer not attached: %q", got.Get("Authorization"))
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
		exit   output.ExitCode
	}{
		{404, output.CodeTraktNotFound, output.ExitTrakt},
		{429, output.CodeTraktRateLimited, output.ExitTrakt},
		{403, output.CodeTraktVIPOnly, output.ExitTrakt},
		{422, output.CodeTraktValidation, output.ExitTrakt},
		{500, output.CodeTraktServer, output.ExitTrakt},
	}
	for _, tc := range cases {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(`{"error":"x"}`))
			}))
			defer srv.Close()
			c := newTestClient(t, srv.URL, &fakeTokens{})
			_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{})
			if cerr == nil {
				t.Fatal("expected error")
			}
			if cerr.Code != tc.code || cerr.Exit != tc.exit {
				t.Errorf("status %d -> got %s/%d want %s/%d", tc.status, cerr.Code, cerr.Exit, tc.code, tc.exit)
			}
		})
	}
}

func TestAutoRefreshOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()
	tok := &fakeTokens{bearer: "old", has: true}
	c := newTestClient(t, srv.URL, tok)
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{Auth: true})
	if cerr != nil {
		t.Fatalf("expected success after refresh, got %v", cerr)
	}
	if !tok.refreshed {
		t.Error("expected a refresh to occur on 401")
	}
	if calls != 2 {
		t.Errorf("expected 2 calls (401 then retry), got %d", calls)
	}
}

func TestAuthMissing(t *testing.T) {
	c := newTestClient(t, "http://127.0.0.1:0", &fakeTokens{})
	_, cerr := c.Do(context.Background(), http.MethodGet, "/x", Options{Auth: true})
	if cerr == nil || cerr.Exit != output.ExitAuthMissing {
		t.Fatalf("expected AUTH_REQUIRED exit 5, got %v", cerr)
	}
}

func TestPaginateAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		w.Header().Set("X-Pagination-Page", page)
		w.Header().Set("X-Pagination-Page-Count", "3")
		w.Header().Set("X-Pagination-Item-Count", "3")
		w.Write([]byte(`[{"p":` + page + `}]`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, &fakeTokens{})
	res, cerr := c.Do(context.Background(), http.MethodGet, "/list", Options{All: true})
	if cerr != nil {
		t.Fatalf("unexpected error: %v", cerr)
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(res.Data, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 merged items across pages, got %d", len(arr))
	}
}
