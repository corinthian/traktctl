package commands

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/client"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
)

// TestCancelledContextIsTransportFailed pins 2.5's final edge case: cancelling
// the invocation context mid-request reports TRANSPORT_FAILED 3 (folded from
// the never-added TRANSPORT_CANCELLED, per review note 2), not
// TRANSPORT_TIMEOUT. The context is set directly on App's runCtx field, same
// package, rather than relying on Cobra to supply a cancellable one --
// production still runs on context.Background() (Execute(), not
// ExecuteContext()), so a test that went through root.ExecuteContext would be
// testing a code path traktctl never actually takes.
func TestCancelledContextIsTransportFailed(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	defer close(release)

	cfg := &config.Config{ClientID: "cid", BaseURL: srv.URL, Timeout: 5 * time.Second}
	app := &App{
		Flags:  &GlobalFlags{},
		Cfg:    cfg,
		Auth:   auth.NewManager(cfg),
		Client: client.New(client.Config{BaseURL: cfg.BaseURL, ClientID: cfg.ClientID, Version: "test", Timeout: cfg.Timeout, Tokens: auth.NewManager(cfg), ErrW: io.Discard}),
		Out:    output.New(io.Discard, io.Discard, output.FormatJSON),
	}
	ctx, cancel := context.WithCancel(context.Background())
	app.runCtx = ctx
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	cmd := newSearchCmd(app)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"query", "--type", "movie", "--q", "x"})
	err := cmd.Execute()

	if err == nil {
		t.Fatal("expected an error for a cancelled context, got nil")
	}
	var cerr *output.CLIError
	if !asCLIError(err, &cerr) {
		t.Fatalf("err type = %T, want *output.CLIError", err)
	}
	if cerr.Code != output.CodeTransportFailed {
		t.Errorf("code = %q, want %q", cerr.Code, output.CodeTransportFailed)
	}
	if cerr.Exit != output.ExitTransport {
		t.Errorf("exit = %v, want %v", cerr.Exit, output.ExitTransport)
	}
}
