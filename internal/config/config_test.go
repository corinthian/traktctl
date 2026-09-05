package config

import (
	"os"
	"path/filepath"
	"strings"
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
	// An empty config file: no base_url set, nothing else to resolve. (A
	// non-existent explicit path is no longer a way to say "load nothing" --
	// see TestLoadExplicitMissingPathErrors.)
	cfg, err := Load(Flags{ConfigPath: writeTempConfig(t, "")})
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
	if _, err := Load(Flags{BaseURL: "http://evil.example"}); err == nil {
		t.Fatal("expected error for non-loopback http base_url")
	}
}

func TestLoadAcceptsLoopbackHTTP(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(Flags{BaseURL: "http://127.0.0.1:9999"})
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
	if _, err := Load(Flags{BaseURL: "https://user:pass@api.trakt.tv"}); err == nil {
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

// TestLoadExplicitMissingPathErrors covers the behaviour break: a typo'd
// --config used to exit 0, silently reading ~/.config/traktctl instead. The
// command "worked" -- against the wrong account, with no way to notice.
func TestLoadExplicitMissingPathErrors(t *testing.T) {
	clearTraktEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.toml")
	_, err := Load(Flags{ConfigPath: missing})
	if err == nil {
		t.Fatal("Load with a missing explicit --config = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error = %q, want it to name the offending path %q", err, missing)
	}
	if !strings.Contains(err.Error(), "--config") {
		t.Errorf("error = %q, want it to name --config as the source", err)
	}
}

// TestLoadEnvConfigMissingPathErrors: TRAKTCTL_CONFIG is explicit too, and gets
// the same authority (and the same failure) as the flag.
func TestLoadEnvConfigMissingPathErrors(t *testing.T) {
	clearTraktEnv(t)
	missing := filepath.Join(t.TempDir(), "nope.toml")
	t.Setenv("TRAKTCTL_CONFIG", missing)
	_, err := Load(Flags{})
	if err == nil {
		t.Fatal("Load with a missing TRAKTCTL_CONFIG = nil error, want a failure")
	}
	if !strings.Contains(err.Error(), "TRAKTCTL_CONFIG") {
		t.Errorf("error = %q, want it to name TRAKTCTL_CONFIG as the source", err)
	}
}

// TestLoadExplicitDirectoryErrors: a directory stats fine but is not a config.
func TestLoadExplicitDirectoryErrors(t *testing.T) {
	clearTraktEnv(t)
	if _, err := Load(Flags{ConfigPath: t.TempDir()}); err == nil {
		t.Fatal("Load with --config pointing at a directory = nil error, want a failure")
	}
}

// TestLoadReadErrorSurfaces: the path stat'd, so failing to read it is a real
// problem. Discarding it yielded an empty config that failed much later as a
// baffling "no client_id".
func TestLoadReadErrorSurfaces(t *testing.T) {
	clearTraktEnv(t)
	if os.Geteuid() == 0 {
		t.Skip("running as root; mode 0000 is still readable")
	}
	path := writeTempConfig(t, "client_id = \"x\"\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })

	cfg, err := Load(Flags{ConfigPath: path})
	if err == nil {
		t.Fatalf("Load on an unreadable config = nil error (cfg.ClientID=%q), want a failure", cfg.ClientID)
	}
}

// TestLoadDefaultMissingIsNotAnError is the other half of the rule: only the
// explicit candidate is authoritative. With no flag and no env, an absent
// ~/.config/traktctl/config.toml is normal -- env and flags may supply
// everything.
func TestLoadDefaultMissingIsNotAnError(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load with no config anywhere = %v, want nil", err)
	}
	if cfg.Source != "" {
		t.Errorf("Source = %q, want empty", cfg.Source)
	}
}

// TestLoadEmptyEnvIsUnset: TRAKTCTL_CONFIG set but blank is unset, not an
// explicit empty path. Otherwise `TRAKTCTL_CONFIG= traktctl ...` would fail.
func TestLoadEmptyEnvIsUnset(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load with an empty TRAKTCTL_CONFIG = %v, want nil", err)
	}
	if cfg.Source != "" {
		t.Errorf("Source = %q, want empty", cfg.Source)
	}
}
