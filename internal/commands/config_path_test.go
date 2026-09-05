package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// runRoot drives the real command tree the way main does and returns the parsed
// stdout envelope, so a test asserts what a consumer sees rather than an
// in-process value. HOME is redirected at every call site so nothing reads or
// creates anything under the developer's ~/.config/traktctl.
func runRoot(t *testing.T, args ...string) (output.Envelope, error) {
	t.Helper()
	root, app := NewRoot()
	var out bytes.Buffer
	app.Out = output.New(&out, io.Discard, output.FormatJSON)
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	err := root.Execute()

	var env output.Envelope
	if out.Len() > 0 {
		if uerr := json.Unmarshal(out.Bytes(), &env); uerr != nil {
			t.Fatalf("stdout is not valid JSON: %v\n%s", uerr, out.String())
		}
	}
	return env, err
}

// TestConfigPathToleratesBadExplicitPath covers the diagnostic exemption. Once
// an explicit --config is authoritative, hard-failing every command on a bad
// one would take out the read-only command you use to diagnose that exact
// problem. `config path` must still exit 0 and say what went wrong.
func TestConfigPathToleratesBadExplicitPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	missing := filepath.Join(t.TempDir(), "nope.toml")

	env, err := runRoot(t, "config", "path", "--config", missing)
	if err != nil {
		t.Fatalf("`config path --config <missing>` = %v, want success", err)
	}
	if !env.OK {
		t.Fatalf("ok = false, want true (error: %+v)", env.Error)
	}

	payload, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %T", env.Data)
	}
	if found, _ := payload["config_found"].(bool); found {
		t.Error("config_found = true, want false for a path that does not exist")
	}
	if explicit, _ := payload["config_explicit"].(bool); !explicit {
		t.Error("config_explicit = false, want true when --config was given")
	}
	msg, _ := payload["config_error"].(string)
	if msg == "" {
		t.Error("config_error is empty; the whole point of the exemption is reporting it")
	}
}

// TestNonExemptCommandFailsOnBadExplicitPath is the other half: the exemption
// is two commands, not a general softening. A read command handed a typo'd
// --config must fail, not quietly run against the default config.
func TestNonExemptCommandFailsOnBadExplicitPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	missing := filepath.Join(t.TempDir(), "nope.toml")

	env, err := runRoot(t, "movie", "trending", "--config", missing)
	if err == nil {
		t.Fatal("`movie trending --config <missing>` = nil error, want BAD_CONFIG")
	}
	cliErr := classifyError(err)
	if cliErr.Code != output.CodeBadConfig {
		t.Errorf("code = %q, want %q (message: %s)", cliErr.Code, output.CodeBadConfig, cliErr.Message)
	}
	if _, code := decodeEnvelope(t, cliErr); code != output.ExitUser {
		t.Errorf("exit = %d, want %d", code, output.ExitUser)
	}
	if env.OK {
		t.Error("ok = true on a failed command")
	}
}

// TestConfigInitToleratesNewExplicitPath: `config init --config <new path>`
// targets a file that does not exist yet. Without the exemption this item would
// break the bootstrap command outright.
func TestConfigInitToleratesNewExplicitPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	target := filepath.Join(t.TempDir(), "fresh", "config.toml")

	env, err := runRoot(t, "config", "init", "--config", target, "--client-id", "cid")
	if err != nil {
		t.Fatalf("`config init --config <new>` = %v, want success", err)
	}
	if !env.OK {
		t.Fatalf("ok = false, want true (error: %+v)", env.Error)
	}
	payload, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %T", env.Data)
	}
	if got, _ := payload["written_to"].(string); got != target {
		t.Errorf("written_to = %q, want %q", got, target)
	}
}
