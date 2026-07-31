package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// decodeEnvelope runs a CLIError through the writer the way Execute does and
// returns the parsed stdout envelope plus the exit code, so a test asserts the
// contract a consumer actually sees rather than the in-process error value.
func decodeEnvelope(t *testing.T, e *output.CLIError) (output.Envelope, output.ExitCode) {
	t.Helper()
	var out bytes.Buffer
	code := output.New(&out, io.Discard, output.FormatJSON).EmitError(e)
	var env output.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("error output is not valid JSON: %v\n%s", err, out.String())
	}
	return env, code
}

// assertUsageEnvelope checks the BAD_REQUEST contract: ok:false on stdout, the
// usage code, and exit 1. wantHint covers the shapes whose recovery is not
// obvious from the message; the "missing required --X" guards name the flag in
// the message itself and carry none.
func assertUsageEnvelope(t *testing.T, shape string, err error, wantHint bool) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected an error, got nil", shape)
	}
	cliErr := classifyError(err)
	env, code := decodeEnvelope(t, cliErr)
	if env.OK {
		t.Errorf("%s: ok = true, want false", shape)
	}
	if env.Error == nil {
		t.Fatalf("%s: envelope has no error body", shape)
	}
	if env.Error.Code != output.CodeBadRequest {
		t.Errorf("%s: code = %q, want %q (message: %s)", shape, env.Error.Code, output.CodeBadRequest, env.Error.Message)
	}
	if code != output.ExitUser {
		t.Errorf("%s: exit = %d, want %d", shape, code, output.ExitUser)
	}
	if wantHint && env.Error.Hint == "" {
		t.Errorf("%s: missing hint", shape)
	}
}

// TestUsageShapesAreBadRequest covers BUG-2: all four confirmed usage shapes
// used to land on BAD_CONFIG, sending a consumer that routes on error.code to
// config/auth troubleshooting for a typo.
func TestUsageShapesAreBadRequest(t *testing.T) {
	// Shape 1: unknown flag, on a subcommand — proves the root's
	// FlagErrorFunc propagates down the tree, not just at the root.
	t.Run("unknown flag", func(t *testing.T) {
		root, _ := NewRoot()
		root.SetArgs([]string{"user", "ratings", "--json"})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		assertUsageEnvelope(t, "unknown flag", root.Execute(), true)
	})

	// Shape 2: unknown command.
	t.Run("unknown command", func(t *testing.T) {
		root, _ := NewRoot()
		root.SetArgs([]string{"version"})
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		assertUsageEnvelope(t, "unknown command", root.Execute(), true)
	})

	// Shape 3: missing subcommand. Driven through the hardened group's RunE
	// directly so no config/auth resolution runs.
	t.Run("missing subcommand", func(t *testing.T) {
		root, _ := NewRoot()
		cmd, _, err := root.Find([]string{"user"})
		if err != nil {
			t.Fatalf("could not find `user`: %v", err)
		}
		assertUsageEnvelope(t, "missing subcommand", cmd.RunE(cmd, nil), true)
	})

	// Shape 4: missing required flag — the register's exemplar, `search query`
	// without --q.
	t.Run("missing required flag", func(t *testing.T) {
		root, _ := NewRoot()
		cmd, _, err := root.Find([]string{"search", "query"})
		if err != nil {
			t.Fatalf("could not find `search query`: %v", err)
		}
		assertUsageEnvelope(t, "missing required flag", runDirect(t, cmd, []string{"--type", "movie"}), false)
	})
}

// TestGenuineConfigStaysBadConfig is the other half of BUG-2: narrowing the
// usage family must not drag real config/env failures along with it.
// A missing config file is deliberately tolerated (env/flags may supply
// everything), so the genuine-failure case is an unparseable one.
func TestGenuineConfigStaysBadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("this is not = valid = toml ][\n"), 0o600); err != nil {
		t.Fatalf("writing broken config: %v", err)
	}
	app := NewApp()
	app.Flags.ConfigPath = path
	cerr := app.build()
	if cerr == nil {
		t.Fatal("build() on an unparseable config.toml = nil, want a BAD_CONFIG error")
	}
	if cerr.Code != output.CodeBadConfig {
		t.Errorf("unreadable config code = %q, want %q", cerr.Code, output.CodeBadConfig)
	}
	if _, code := decodeEnvelope(t, cerr); code != output.ExitUser {
		t.Errorf("unreadable config exit = %d, want %d", code, output.ExitUser)
	}
}

// TestConfirmGateIsBadRequest covers the confirm gate, which is not one of
// BUG-2's four confirmed shapes but follows from the same rule: refusing a
// destructive call that arrived without --confirm is a bad invocation, not a
// config-file problem, and plexctl's enumeration puts "bad flags/args/
// invocation" squarely in BAD_REQUEST.
func TestConfirmGateIsBadRequest(t *testing.T) {
	t.Setenv("TRAKTCTL_CONFIRM", "")
	root, _ := NewRoot()
	cmd, _, err := root.Find([]string{"sync", "collection", "remove"})
	if err != nil {
		t.Skipf("confirm-gated command not found: %v", err)
	}
	runErr := runDirect(t, cmd, []string{"--payload", `{"movies":[]}`})
	var cliErr *output.CLIError
	if !asCLIError(runErr, &cliErr) {
		t.Fatalf("err type = %T (%v), want *output.CLIError", runErr, runErr)
	}
	if cliErr.Code != output.CodeBadRequest {
		t.Errorf("confirm-gate code = %q, want %q (message: %s)", cliErr.Code, output.CodeBadRequest, cliErr.Message)
	}
}
