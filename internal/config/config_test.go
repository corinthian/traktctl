package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearTraktEnv removes the env vars Load consults so a test's machine state
// (the developer's real TRAKT_* exports) cannot leak into precedence checks.
func clearTraktEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"TRAKT_CLIENT_ID", "TRAKT_CLIENT_SECRET", "TRAKT_ACCESS_TOKEN",
		"TRAKT_REFRESH_TOKEN", "TRAKT_BASE_URL", "TRAKTCTL_CONFIG",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

// writeTempConfig writes a config.toml into a temp dir and returns its path.
func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadDefaultsBaseURL(t *testing.T) {
	clearTraktEnv(t)
	// Point at a non-existent explicit path so the cwd config.toml is skipped.
	cfg, err := Load(Flags{ConfigPath: filepath.Join(t.TempDir(), "none.toml")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != defaultBaseURL {
		t.Errorf("BaseURL default = %q, want %q", cfg.BaseURL, defaultBaseURL)
	}
}

func TestLoadFileValues(t *testing.T) {
	clearTraktEnv(t)
	path := writeTempConfig(t, `client_id = "file_id"
client_secret = "file_secret"
default_user = "alice"
base_url = "https://file.example"
`)
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "file_id" {
		t.Errorf("ClientID = %q, want file_id", cfg.ClientID)
	}
	if cfg.DefaultUser != "alice" {
		t.Errorf("DefaultUser = %q, want alice", cfg.DefaultUser)
	}
	if cfg.BaseURL != "https://file.example" {
		t.Errorf("BaseURL = %q, want file value", cfg.BaseURL)
	}
	if cfg.Source != path {
		t.Errorf("Source = %q, want %q", cfg.Source, path)
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearTraktEnv(t)
	path := writeTempConfig(t, "client_id = \"file_id\"\n")
	t.Setenv("TRAKT_CLIENT_ID", "env_id")
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "env_id" {
		t.Errorf("env should override file: ClientID = %q, want env_id", cfg.ClientID)
	}
}

func TestFlagOverridesEnvAndFile(t *testing.T) {
	clearTraktEnv(t)
	path := writeTempConfig(t, "client_id = \"file_id\"\n")
	t.Setenv("TRAKT_CLIENT_ID", "env_id")
	cfg, err := Load(Flags{ConfigPath: path, ClientID: "flag_id"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID != "flag_id" {
		t.Errorf("flag should win: ClientID = %q, want flag_id", cfg.ClientID)
	}
}

func TestTimeoutParsing(t *testing.T) {
	clearTraktEnv(t)
	path := writeTempConfig(t, "client_id = \"x\"\ntimeout = \"5s\"\n")
	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Timeout.Seconds() != 5 {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestResolveConfigPathIgnoresCwd(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("client_id = \"cwd_id\"\n"), 0o600); err != nil {
		t.Fatalf("write cwd config: %v", err)
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ClientID == "cwd_id" {
		t.Error("cwd config.toml was loaded; it should be ignored")
	}
	if cfg.Source != "" {
		t.Errorf("Source = %q, want empty (cwd ignored, no home config)", cfg.Source)
	}
}

func TestLoadRejectsNonLoopbackHTTP(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "none.toml")
	if _, err := Load(Flags{ConfigPath: path, BaseURL: "http://evil.example"}); err == nil {
		t.Fatal("expected error for non-loopback http base_url")
	}
}

func TestLoadAcceptsLoopbackHTTP(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "none.toml")
	cfg, err := Load(Flags{ConfigPath: path, BaseURL: "http://127.0.0.1:9999"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != "http://127.0.0.1:9999" {
		t.Errorf("BaseURL = %q, want loopback URL preserved", cfg.BaseURL)
	}
}

func TestLoadRejectsUserinfo(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "none.toml")
	if _, err := Load(Flags{ConfigPath: path, BaseURL: "https://user:pass@api.trakt.tv"}); err == nil {
		t.Fatal("expected error for base_url containing userinfo")
	}
}

func TestWriteConfigFileRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	fc := FileConfig{ClientID: "x"}
	if err := WriteConfigFile(path, fc, false); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteConfigFile(path, fc, false); err == nil {
		t.Error("expected refusal to overwrite without force")
	}
	if err := WriteConfigFile(path, fc, true); err != nil {
		t.Errorf("force overwrite should succeed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config perms = %v, want 0600", info.Mode().Perm())
	}
}
