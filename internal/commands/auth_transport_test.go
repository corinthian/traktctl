package commands

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
)

// TestAuthRefreshClassifiesTransportAndDecode pins the Part 3 rows for the
// OAuth token exchange: a read that broke part-way is TRANSPORT_FAILED 3 and a
// body traktctl cannot decode is DECODE_ERROR 4, where both used to arrive as
// AUTH_EXPIRED 2 and told the user to log in again over a network fault.
func TestAuthRefreshClassifiesTransportAndDecode(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		code    string
		exit    output.ExitCode
	}{
		{"read failure", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"access_token":"a`))
			panic(http.ErrAbortHandler)
		}, output.CodeTransportFailed, output.ExitTransport},
		{"truncated body", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"access_token":"a"`))
		}, output.CodeDecodeError, output.ExitInternal},
		{"bad status", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, output.CodeAuthExpired, output.ExitTrakt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			srv.Config.ErrorLog = log.New(io.Discard, "", 0)
			defer srv.Close()

			cfg := &config.Config{
				ClientID: "cid", ClientSecret: "sec", BaseURL: srv.URL, Timeout: 5 * time.Second,
				AccessToken: "fake-token", RefreshToken: "fake-refresh",
			}
			app := &App{Flags: &GlobalFlags{}, Cfg: cfg, Auth: auth.NewManager(cfg)}
			cmd := newAuthCmd(app)
			cmd.SetArgs([]string{"refresh"})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected an error")
			}
			var cliErr *output.CLIError
			if !asCLIError(err, &cliErr) {
				t.Fatalf("err type = %T, want *output.CLIError", err)
			}
			if cliErr.Code != tc.code || cliErr.Exit != tc.exit {
				t.Errorf("got %s exit %d (%s), want %s exit %d",
					cliErr.Code, cliErr.Exit, cliErr.Message, tc.code, tc.exit)
			}
		})
	}
}
