package commands

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
)

// TestRevokeFailureHintsAtLogout covers the Phase 2 fix at the command layer:
// a failed remote revoke must leave the token in place and point the user at
// `auth logout` as the explicit local-only escape hatch, rather than a
// dedicated --force flag (removed during plan review to avoid duplicating
// `auth logout` behind a second, differently-truthful option).
//
// The manager here is given an explicit AccessToken, which routes ensureLoaded
// through the flag/env branch and never touches the token store -- so this
// test needs no keyring mock, unlike the auth package's own Revoke tests.
func TestRevokeFailureHintsAtLogout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{
		ClientID: "cid", ClientSecret: "sec", BaseURL: srv.URL, Timeout: 5 * time.Second,
		AccessToken: "fake-token",
	}
	app := &App{
		Flags: &GlobalFlags{Confirm: true},
		Cfg:   cfg,
		Auth:  auth.NewManager(cfg),
	}

	cmd := newAuthCmd(app)
	cmd.SetArgs([]string{"revoke"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error from revoke against a failing server")
	}
	var cliErr *output.CLIError
	if !asCLIError(err, &cliErr) {
		t.Fatalf("err type = %T, want *output.CLIError", err)
	}
	if !strings.Contains(cliErr.Hint, "auth logout") {
		t.Errorf("Hint = %q, want it to mention `auth logout`", cliErr.Hint)
	}
}
