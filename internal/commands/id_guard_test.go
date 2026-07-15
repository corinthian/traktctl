package commands

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/client"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

// fakeTokens is a minimal client.TokenSource that lets a Client complete a
// request without any real Trakt auth. Used by the RunE-level regression
// tests in this file (TC-4/TC-5), which need a command to run past its guard
// checks and reach the network to prove it was NOT rejected.
type fakeTokens struct{}

func (fakeTokens) Bearer() string                    { return "test-token" }
func (fakeTokens) HasToken() bool                    { return true }
func (fakeTokens) Refresh(ctx context.Context) error { return nil }

// newTestServerRoot builds a fresh root+App (independent GlobalFlags, so
// --changed flag state never leaks between subtests) wired to a local
// httptest server, so a command's RunE can be driven end to end without
// touching the network or real Trakt.
func newTestServerRoot(t *testing.T, handler http.HandlerFunc) (*cobra.Command, *App) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	root, app := NewRoot()
	app.Cfg = &config.Config{}
	app.Client = client.New(client.Config{
		BaseURL: srv.URL,
		Tokens:  fakeTokens{},
		Timeout: 5 * time.Second,
	})
	app.Out = output.New(io.Discard, io.Discard, output.FormatJSON)
	return root, app
}

// runDirect parses args on cmd (merging inherited persistent flags) and
// invokes its RunE directly, bypassing root.Execute()/PersistentPreRunE so
// no config/auth resolution or --llm short-circuit interferes.
func runDirect(t *testing.T, cmd *cobra.Command, args []string) error {
	t.Helper()
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags(%v) = %v", args, err)
	}
	return cmd.RunE(cmd, cmd.Flags().Args())
}

// TestUserListNoBodyWriteRejectsID covers TC-4: the no-body list writers
// (list-delete/list-like/list-unlike, built via userListNoBodyWrite) must
// reject a stray --id the same way the payload writers do, since their real
// target is --list-id and a caller passing --id would otherwise believe it
// selected something.
func TestUserListNoBodyWriteRejectsID(t *testing.T) {
	// --id set alongside --confirm -> rejectIDFlags fires before any network
	// call, so the handler must never be invoked.
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
	})
	cmd, _, err := root.Find([]string{"user", "list-delete"})
	if err != nil {
		t.Fatalf("could not find `user list-delete`: %v", err)
	}
	runErr := runDirect(t, cmd, []string{"--list-id", "X", "--id", "5", "--confirm"})
	var cliErr *output.CLIError
	if !asCLIError(runErr, &cliErr) {
		t.Fatalf("user list-delete --id 5 --confirm err type = %T (%v), want *output.CLIError", runErr, runErr)
	}
	if cliErr.Code != output.CodeBadConfig {
		t.Errorf("user list-delete --id 5 --confirm code = %q, want %q", cliErr.Code, output.CodeBadConfig)
	}
}

// TestUserListNoBodyWritePassesWithoutID is the regression half of TC-4:
// list-delete with only its legitimate target (--list-id) must pass the
// guard and reach the network.
func TestUserListNoBodyWritePassesWithoutID(t *testing.T) {
	called := false
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cmd, _, err := root.Find([]string{"user", "list-delete"})
	if err != nil {
		t.Fatalf("could not find `user list-delete`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--list-id", "X", "--confirm"}); runErr != nil {
		t.Fatalf("user list-delete --list-id X --confirm = %v, want nil", runErr)
	}
	if !called {
		t.Error("user list-delete --list-id X --confirm never reached the network; guard over-rejected")
	}
}

// TestRecommendHideStillAcceptsID is the other TC-4 regression: commands
// whose legitimate target IS --id (recommend hide-*) must NOT gain the
// rejectIDFlags guard.
func TestRecommendHideStillAcceptsID(t *testing.T) {
	called := false
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cmd, _, err := root.Find([]string{"recommend", "hide-movie"})
	if err != nil {
		t.Fatalf("could not find `recommend hide-movie`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--id", "5"}); runErr != nil {
		t.Fatalf("recommend hide-movie --id 5 = %v, want nil", runErr)
	}
	if !called {
		t.Error("recommend hide-movie --id 5 never reached the network; --id was wrongly rejected")
	}
}
