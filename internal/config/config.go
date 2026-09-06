// Package config resolves runtime configuration from, in precedence order:
// CLI flags > environment > config.toml (~/.config/traktctl) > keychain.
// Token material (access/refresh) is layered separately by the auth package;
// this package supplies client credentials, base URL, and defaults.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/corinthian/traktctl/internal/atomicfile"
	"github.com/corinthian/traktctl/internal/configpath"
	"github.com/corinthian/traktctl/internal/xduration"
	toml "github.com/pelletier/go-toml/v2"
)

// Config holds resolved, non-secret-by-default runtime settings. Token fields
// may be populated from env/flags but the canonical token store is the auth
// package (keychain, file fallback).
type Config struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	BaseURL      string `toml:"base_url"`
	DefaultUser  string `toml:"default_user"`
	Extended     string `toml:"extended"`
	// Timeout is the resolved value: xduration.Parse/Resolve over --timeout,
	// $TRAKTCTL_TIMEOUT and TimeoutFile, in that order. Never set from TOML
	// directly -- TimeoutFile is the raw decoded field, kept as a pointer so
	// "absent" and "zero" are distinguishable and so a wrong TOML type (a
	// string, a float, a bool, an array, a table) is a decode error naming
	// `timeout` rather than a silently ignored key.
	Timeout     time.Duration `toml:"-"`
	TimeoutFile *int64        `toml:"timeout"`

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

	// Timeout and TimeoutSet carry --timeout. TimeoutSet distinguishes an
	// absent flag from an explicitly empty one (--timeout ""), which cobra's
	// Changed() reports but a bare string cannot -- Load cannot ask cobra
	// itself, so the caller (root.go's PersistentPreRunE) sets this.
	Timeout    string
	TimeoutSet bool
}

const defaultBaseURL = "https://api.trakt.tv"

// Load resolves configuration. flags win over env, env over file. The keychain
// token layer is applied later by auth.Manager, not here.
func Load(f Flags) (*Config, error) {
	c := &Config{}

	// 3. config file (lowest of the file/env/flag tiers handled here)
	path, _, err := resolveConfigPath(f.ConfigPath)
	if err != nil {
		return nil, err
	}
	if path != "" {
		// The path came back only after a successful stat, so a read failure
		// here is a real problem (permissions, a race, a bad mount) and must
		// not be swallowed into an empty config that then fails much later
		// as a confusing "no client_id".
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("reading config %s: %w: %w", path, rerr, ErrConfigPath)
		}
		if err := toml.Unmarshal(b, c); err != nil {
			return nil, timeoutDecodeError(err, path)
		}
		c.Source = path
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
	if err := validateBaseURL(c.BaseURL); err != nil {
		return nil, err
	}
	// --timeout is not a Resolve candidate: Resolve skips an empty Value, so
	// an explicitly empty flag (--timeout "") would read as unset instead of
	// the error the contract requires. TimeoutSet is what makes that
	// distinguishable from an absent flag.
	if f.TimeoutSet {
		d, err := xduration.Parse(f.Timeout, "--timeout")
		if err != nil {
			return nil, err
		}
		c.Timeout = d
		return c, nil
	}

	fileTimeout := ""
	if c.TimeoutFile != nil {
		fileTimeout = strconv.FormatInt(*c.TimeoutFile, 10)
	}
	d, err := xduration.Resolve(30*time.Second,
		xduration.Candidate{Source: "$TRAKTCTL_TIMEOUT", Value: os.Getenv("TRAKTCTL_TIMEOUT")},
		xduration.Candidate{Source: "config timeout (" + path + ")", Value: fileTimeout},
	)
	if err != nil {
		return nil, err
	}
	c.Timeout = d
	return c, nil
}

// timeoutDecodeError catches a TOML type mismatch on the `timeout` key --
// go-toml's own message names the Go field and type ("cannot decode TOML
// string into struct field ... int64"), which is not the source-named message
// 2.1 requires. Everything else from Unmarshal passes through unchanged.
func timeoutDecodeError(err error, path string) error {
	var de *toml.DecodeError
	if errors.As(err, &de) {
		key := de.Key()
		if len(key) > 0 && key[len(key)-1] == "timeout" {
			return &xduration.Error{
				Source: "config timeout (" + path + ")",
				Value:  "",
				Reason: "must be a whole number of seconds (1-86400): " + de.Error(),
			}
		}
	}
	return err
}

// ErrConfigPath marks a config failure that is about the *path* -- the named
// file is missing, unreadable or a directory -- as opposed to the file's
// contents. The tolerateBadConfig commands forgive only this class: `config
// path` must still report a bad path and `config init` must still create a
// missing file, but neither may run over TOML that does not parse or values
// that fail validation, or `config init` would write a file every later
// command rejects.
var ErrConfigPath = errors.New("config path unusable")

// resolveConfigPath resolves the config.toml to read and reports whether the
// caller named it explicitly.
//
// An explicit path -- `--config`, or a non-empty TRAKTCTL_CONFIG -- is
// authoritative: if it is missing or unstattable this returns an error rather
// than quietly falling through to ~/.config/traktctl. A typo'd --config used to
// exit 0 against the default config, which is the worst possible answer: the
// command "worked", against the wrong account. An empty TRAKTCTL_CONFIG counts
// as unset, not as an explicit empty path.
//
// Only the default candidate tolerates being absent -- env and flags may supply
// everything a command needs. Absent is the *only* forgiven outcome on the
// default path: a directory there, or a Stat that fails for any other reason,
// or a home directory that cannot be resolved, used to read as "no config",
// which reported a real misconfiguration as a fresh install.
func resolveConfigPath(explicit string) (string, bool, error) {
	source := "explicit --config path"
	if strings.TrimSpace(explicit) == "" && os.Getenv("TRAKTCTL_CONFIG") != "" {
		source = "TRAKTCTL_CONFIG"
	}

	// The home lookup happens before the resolve, not inside it: a home
	// directory that cannot be resolved is a failure to work out where the
	// default lives, and returning an empty default path for it is what used
	// to report a real config as absent.
	var def string
	if strings.TrimSpace(explicit) == "" && os.Getenv("TRAKTCTL_CONFIG") == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", false, fmt.Errorf("cannot resolve the default config path: %w: %w", herr, ErrConfigPath)
		}
		def = filepath.Join(home, ".config", "traktctl", "config.toml")
		source = "default config path " + def
	}

	path, isExplicit, err := configpath.Resolve(explicit, "TRAKTCTL_CONFIG", def)
	if err == nil {
		return path, isExplicit, nil
	}

	// The wording stays traktctl's, and everything is re-wrapped in traktctl's
	// own ErrConfigPath so root.go's tolerance test is unchanged.
	named := strings.TrimSpace(explicit)
	if named == "" {
		named = os.Getenv("TRAKTCTL_CONFIG")
	}
	if named == "" {
		named = def
	}
	switch {
	case errors.Is(err, configpath.ErrIsDirectory):
		return "", isExplicit, fmt.Errorf("%s is a directory, not a config file: %s: %w", source, named, ErrConfigPath)
	case errors.Is(err, os.ErrNotExist):
		if source == "TRAKTCTL_CONFIG" {
			return "", isExplicit, fmt.Errorf("TRAKTCTL_CONFIG points at a missing file: %s: %w", named, ErrConfigPath)
		}
		return "", isExplicit, fmt.Errorf("explicit --config path does not exist: %s: %w", named, ErrConfigPath)
	}
	return "", isExplicit, fmt.Errorf("%s is unreadable: %s: %w: %w", source, named, err, ErrConfigPath)
}

// validateBaseURL enforces the trust boundary on where API traffic can go:
// https anywhere, or plain http only to loopback (for local dev/test servers).
// Userinfo in the URL is rejected outright — it has no legitimate use here
// and is a classic SSRF/credential-leak vector.
func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url %q: %w", raw, err)
	}
	if u.User != nil {
		return fmt.Errorf("base_url %q must not contain userinfo", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("base_url %q must use https (http is only allowed for localhost/127.0.0.1/::1)", raw)
	default:
		return fmt.Errorf("base_url %q must use https or http", raw)
	}
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// FileConfig is the on-disk config.toml shape written by `config init`. Only
// non-empty fields are serialized, so an env-held secret can be kept out.
type FileConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret,omitempty"`
	DefaultUser  string `toml:"default_user,omitempty"`
	BaseURL      string `toml:"base_url,omitempty"`
	Extended     string `toml:"extended,omitempty"`
	// Timeout mirrors Config.TimeoutFile. `config init` never sets it, but it
	// belongs in the shape so `--force` can carry a hand-edited value forward
	// instead of silently discarding it. A legacy string value ("30s") fails
	// to decode here -- that is ReadFileConfig's job to report, not to migrate.
	Timeout *int64 `toml:"timeout,omitempty"`
}

// ReadFileConfig parses an existing config.toml into the on-disk shape. Used by
// `config init --force` to carry forward the fields it does not itself write.
func ReadFileConfig(path string) (FileConfig, error) {
	var fc FileConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return fc, err
	}
	if err := toml.Unmarshal(b, &fc); err != nil {
		return fc, err
	}
	return fc, nil
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
// --config value (or "" if none resolves) and whether the caller named a path
// explicitly. Used by `config path`, which reports the resolution rather than
// failing on it -- the error itself reaches that command via App.CfgErr.
func ResolvedConfigPath(explicit string) (string, bool) {
	path, isExplicit, err := resolveConfigPath(explicit)
	if err != nil {
		return "", isExplicit
	}
	return path, isExplicit
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
	return atomicfile.Write(path, append([]byte(header), b...))
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
