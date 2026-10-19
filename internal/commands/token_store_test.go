package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
)

func TestConfigInitForcePreservesTokenStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	t.Setenv("TRAKTCTL_TOKEN_STORE", "")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("client_id = \"old\"\ntoken_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, err := runRoot(t, "config", "init", "--config", path, "--client-id", "new", "--force"); err != nil {
		t.Fatalf("config init --force = %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "token_store = 'file'") && !strings.Contains(string(b), `token_store = "file"`) {
		t.Errorf("rewritten config dropped token_store:\n%s", b)
	}
	cfg, err := config.Load(config.Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TokenStore != config.TokenStoreFile {
		t.Errorf("TokenStore = %q, want file", cfg.TokenStore)
	}
}

func TestConfigPathReportsTokenStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	t.Setenv("TRAKTCTL_TOKEN_STORE", "file")
	env, err := runRoot(t, "config", "path")
	if err != nil {
		t.Fatalf("config path = %v", err)
	}
	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data = %T, want an object", env.Data)
	}
	if data["token_store"] != "file" {
		t.Errorf("token_store = %v, want file", data["token_store"])
	}
}

// TestTolerantPathHonoursTokenStoreEnv: a bad --config still leaves `config
// path` running, and it must not fall back to the keychain when the env says
// file.
func TestTolerantPathHonoursTokenStoreEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	t.Setenv("TRAKTCTL_TOKEN_STORE", "file")
	env, err := runRoot(t, "config", "path", "--config", filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatalf("config path = %v", err)
	}
	data, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data = %T, want an object", env.Data)
	}
	if data["config_error"] == nil {
		t.Fatalf("expected a tolerated config_error, got %v", data)
	}
	if data["token_store"] != "file" {
		t.Errorf("token_store = %v, want file", data["token_store"])
	}
}
