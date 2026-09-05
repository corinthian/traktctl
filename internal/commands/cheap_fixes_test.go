package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
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
	if err := os.WriteFile(path, []byte("client_id = \"old\"\ntimeout = \"90s\"\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	if _, err := runRoot(t, "config", "init", "--config", path, "--client-id", "new", "--force"); err != nil {
		t.Fatalf("config init --force = %v, want success", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten config: %v", err)
	}
	if !strings.Contains(string(b), `timeout = '90s'`) && !strings.Contains(string(b), `timeout = "90s"`) {
		t.Errorf("rewritten config dropped the timeout:\n%s", b)
	}

	cfg, err := config.Load(config.Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load rewritten config: %v", err)
	}
	if cfg.Timeout.Seconds() != 90 {
		t.Errorf("Timeout = %v, want 90s", cfg.Timeout)
	}
	if cfg.ClientID != "new" {
		t.Errorf("ClientID = %q, want the newly written %q", cfg.ClientID, "new")
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
