package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/output"
)

// TestBaseOptsUsesConfiguredExtended: `extended` in config.toml was a dead key
// -- parsed, reported by `config path`, and never read. The flag still wins.
func TestBaseOptsUsesConfiguredExtended(t *testing.T) {
	t.Run("config supplies it when the flag is empty", func(t *testing.T) {
		a := &App{Flags: &GlobalFlags{}, Cfg: &config.Config{Extended: "full"}}
		if got := a.baseOpts(false).Extended; got != "full" {
			t.Errorf("Extended = %q, want the configured %q", got, "full")
		}
	})
	t.Run("flag wins over config", func(t *testing.T) {
		a := &App{Flags: &GlobalFlags{Extended: "images"}, Cfg: &config.Config{Extended: "full"}}
		if got := a.baseOpts(false).Extended; got != "images" {
			t.Errorf("Extended = %q, want the flag value %q", got, "images")
		}
	})
}

// TestBaseOptsNilConfig guards the nil check. Several tests (and any caller
// before build() runs) construct an App from flags alone, so reading a.Cfg
// unguarded is a panic across the suite, not a theoretical one.
func TestBaseOptsNilConfig(t *testing.T) {
	a := &App{Flags: &GlobalFlags{Extended: "full"}}
	if got := a.baseOpts(false).Extended; got != "full" {
		t.Errorf("Extended = %q, want %q", got, "full")
	}
	b := &App{Flags: &GlobalFlags{}}
	if got := b.baseOpts(false).Extended; got != "" {
		t.Errorf("Extended = %q, want empty", got)
	}
}

// TestConfigInitForcePreservesTimeout: `timeout` is the one config field
// `config init` cannot be told about, so rewriting the file with --force used
// to drop a hand-edited value in silence.
func TestConfigInitForcePreservesTimeout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("client_id = \"old\"\ntimeout = 45\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := runRoot(t, "config", "init", "--config", path, "--client-id", "new", "--force"); err != nil {
		t.Fatalf("config init --force = %v, want success", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten config: %v", err)
	}
	if !strings.Contains(string(b), "timeout = 45") {
		t.Errorf("rewritten config dropped the timeout:\n%s", b)
	}

	cfg, err := config.Load(config.Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load rewritten config: %v", err)
	}
	if cfg.Timeout.Seconds() != 45 {
		t.Errorf("Timeout = %v, want 45s", cfg.Timeout)
	}
	if cfg.ClientID != "new" {
		t.Errorf("ClientID = %q, want the newly written %q", cfg.ClientID, "new")
	}
}

// TestConfigInitForceOverLegacyStringTimeoutIsBadConfig: once `timeout` is an
// integer, a config carrying the old string form fails to decode. Per 2.7's
// tolerance table ("valid file with an invalid timeout ... not forgiven under
// tolerance, because the file parsed and the value is wrong"), that is a
// parse-time failure like any other -- app.build()'s own config.Load rejects
// it before `config init --force`'s RunE ever runs, exactly like the sibling
// TestConfigInitRejectsUnparseableExplicitConfig. The file is left untouched;
// there is no rewrite-and-drop path for this case, only the repair path of
// hand-editing the file (or moving it aside) before rerunning `config init`.
func TestConfigInitForceOverLegacyStringTimeoutIsBadConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	seed := []byte("client_id = \"old\"\ntimeout = \"30s\"\n")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	_, err := runRoot(t, "config", "init", "--config", path, "--client-id", "new", "--force")
	var cerr *output.CLIError
	if !errors.As(err, &cerr) {
		t.Fatalf("config init --force over a legacy string timeout = %v, want *output.CLIError", err)
	}
	if cerr.Code != output.CodeBadConfig {
		t.Fatalf("code = %q, want %q", cerr.Code, output.CodeBadConfig)
	}
	if !strings.Contains(cerr.Message, "config timeout ("+path+")") {
		t.Errorf("message %q does not name config timeout (%s)", cerr.Message, path)
	}

	got, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != string(seed) {
		t.Errorf("config init overwrote a file it should have rejected; contents now %q", got)
	}
}

// TestConfigInitForceOnFreshPathIsFine: --force against a path with nothing to
// carry forward must not fail on the absent file.
func TestConfigInitForceOnFreshPathIsFine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	path := filepath.Join(t.TempDir(), "fresh.toml")

	env, err := runRoot(t, "config", "init", "--config", path, "--client-id", "cid", "--force")
	if err != nil {
		t.Fatalf("config init --force on a fresh path = %v, want success", err)
	}
	if !env.OK {
		t.Fatalf("ok = false (error: %+v)", env.Error)
	}
	if _, serr := os.Stat(path); serr != nil {
		t.Errorf("config was not written: %v", serr)
	}
}
