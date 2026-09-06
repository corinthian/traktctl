package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultHome points HOME at a temp dir and returns the default config path
// under it, with its parent created.
func defaultHome(t *testing.T) string {
	t.Helper()
	clearTraktEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "traktctl")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return filepath.Join(dir, "config.toml")
}

func wantPathError(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want a BAD_CONFIG failure, got nil", what)
	}
	if !errors.Is(err, ErrConfigPath) {
		t.Errorf("%s: error %q does not wrap ErrConfigPath, so the tolerant commands cannot forgive it", what, err)
	}
}

// TestLoadDefaultDirectoryErrors: a directory at the default path was silently
// "no config", so a real misconfiguration read as a fresh install.
func TestLoadDefaultDirectoryErrors(t *testing.T) {
	def := defaultHome(t)
	if err := os.MkdirAll(def, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := Load(Flags{})
	wantPathError(t, err, "default config is a directory")
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error = %q, want it to say the path is a directory", err)
	}
}

// TestLoadDefaultStatFailureErrors: any Stat failure that is not "missing" is
// a real problem, not an absent config.
func TestLoadDefaultStatFailureErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; mode 0000 is still traversable")
	}
	def := defaultHome(t)
	dir := filepath.Dir(def)
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	_, err := Load(Flags{})
	wantPathError(t, err, "default config Stat fails")
}

// TestLoadHomeLookupFailureErrors: a home directory that cannot be resolved is
// an error, not an empty default path.
func TestLoadHomeLookupFailureErrors(t *testing.T) {
	clearTraktEnv(t)
	t.Setenv("HOME", "")
	_, err := Load(Flags{})
	wantPathError(t, err, "os.UserHomeDir fails")
}

// TestLoadDanglingSymlinkOnDefaultIsForgiven: Stat follows the link and
// reports ErrNotExist, which is the one forgiven outcome on the default path.
func TestLoadDanglingSymlinkOnDefaultIsForgiven(t *testing.T) {
	def := defaultHome(t)
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone.toml"), def); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("a dangling default symlink = %v, want nil", err)
	}
	if cfg.Source != "" {
		t.Errorf("Source = %q, want empty", cfg.Source)
	}
}
