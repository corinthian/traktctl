// Package config resolves runtime configuration from, in precedence order:
// CLI flags > environment > config.toml (cwd or ~/.config/traktctl) > keychain.
// Token material (access/refresh) is layered separately by the auth package;
// this package supplies client credentials, base URL, and defaults.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// Config holds resolved, non-secret-by-default runtime settings. Token fields
// may be populated from env/flags but the canonical token store is the auth
// package (keychain, file fallback).
type Config struct {
	ClientID     string        `toml:"client_id"`
	ClientSecret string        `toml:"client_secret"`
	BaseURL      string        `toml:"base_url"`
	DefaultUser  string        `toml:"default_user"`
	Extended     string        `toml:"extended"`
	Timeout      time.Duration `toml:"-"`
	TimeoutStr   string        `toml:"timeout"`

	// AccessToken/RefreshToken from flags or env only (highest precedence,
	// override the token store when present).
	AccessToken  string `toml:"-"`
	RefreshToken string `toml:"-"`

	// Source records where the config file was loaded from (for `auth status`).
	Source string `toml:"-"`
}

// Flags carries the raw CLI flag values that override file/env.
type Flags struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	BaseURL      string
	ConfigPath   string // explicit --config path, optional
}

const defaultBaseURL = "https://api.trakt.tv"

// Load resolves configuration. flags win over env, env over file. The keychain
// token layer is applied later by auth.Manager, not here.
func Load(f Flags) (*Config, error) {
	c := &Config{}

	// 3. config file (lowest of the file/env/flag tiers handled here)
	path := resolveConfigPath(f.ConfigPath)
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			if err := toml.Unmarshal(b, c); err != nil {
				return nil, err
			}
			c.Source = path
		}
	}

	// 2. environment overrides file
	envOverride(&c.ClientID, "TRAKT_CLIENT_ID")
	envOverride(&c.ClientSecret, "TRAKT_CLIENT_SECRET")
	envOverride(&c.AccessToken, "TRAKT_ACCESS_TOKEN")
	envOverride(&c.RefreshToken, "TRAKT_REFRESH_TOKEN")
	envOverride(&c.BaseURL, "TRAKT_BASE_URL")

	// 1. flags override everything in this tier
	override(&c.ClientID, f.ClientID)
	override(&c.ClientSecret, f.ClientSecret)
	override(&c.AccessToken, f.AccessToken)
	override(&c.BaseURL, f.BaseURL)

	if c.BaseURL == "" {
		c.BaseURL = defaultBaseURL
	}
	c.Timeout = 30 * time.Second
	if c.TimeoutStr != "" {
		if d, err := time.ParseDuration(c.TimeoutStr); err == nil {
			c.Timeout = d
		}
	}
	return c, nil
}

// resolveConfigPath returns the first existing config.toml: explicit flag,
// $TRAKTCTL_CONFIG, ./config.toml (dev bootstrap), then ~/.config/traktctl.
func resolveConfigPath(explicit string) string {
	candidates := []string{}
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("TRAKTCTL_CONFIG"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates, "config.toml")
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".config", "traktctl", "config.toml"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// FileConfig is the on-disk config.toml shape written by `config init`. Only
// non-empty fields are serialized, so an env-held secret can be kept out.
type FileConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret,omitempty"`
	DefaultUser  string `toml:"default_user,omitempty"`
	BaseURL      string `toml:"base_url,omitempty"`
	Extended     string `toml:"extended,omitempty"`
}

// DefaultConfigPath returns ~/.config/traktctl/config.toml (does not create it).
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "traktctl", "config.toml"), nil
}

// ResolvedConfigPath returns the path Load would read for the given explicit
// --config value, or "" if none exists (used by `config path`).
func ResolvedConfigPath(explicit string) string {
	return resolveConfigPath(explicit)
}

// WriteConfigFile writes fc to path (0600), creating parent dirs. It refuses to
// overwrite an existing file unless force is set.
func WriteConfigFile(path string, fc FileConfig, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config already exists at %s (use --force to overwrite)", path)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := toml.Marshal(fc)
	if err != nil {
		return err
	}
	header := "# traktctl configuration — written by `traktctl config init`\n" +
		"# Holds client_secret in plaintext; keep private (mode 0600).\n\n"
	return os.WriteFile(path, append([]byte(header), b...), 0o600)
}

// ConfigDir returns ~/.config/traktctl, creating it if needed.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "traktctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func envOverride(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func override(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}
