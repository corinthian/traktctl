package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteConfigFileReplacesSymlinkLeavingTargetUntouched carries the
// documented symlink behaviour across the swap to internal/atomicfile: writing
// to a path that is a symlink replaces the symlink itself, so a link planted
// by another process cannot redirect the write.
func TestWriteConfigFileReplacesSymlinkLeavingTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("original target content"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	link := filepath.Join(dir, "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := WriteConfigFile(link, FileConfig{ClientID: "cid"}, true); err != nil {
		t.Fatalf("WriteConfigFile: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("path is still a symlink; the write went through the link")
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "cid") {
		t.Errorf("config content = %q, want it to hold the client_id", got)
	}
	tc, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(tc) != "original target content" {
		t.Errorf("symlink target was modified; content = %q", tc)
	}
}

// TestWriteConfigFileStillRefusesOverwriteWithoutForce: the --force guard is
// WriteConfigFile's, not the writer's, and the swap does not touch it.
func TestWriteConfigFileStillRefusesOverwriteWithoutForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteConfigFile(path, FileConfig{ClientID: "cid"}, false); err == nil {
		t.Fatal("WriteConfigFile over an existing file without --force = nil, want a refusal")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing\n" {
		t.Errorf("the existing file was modified: %q", got)
	}
}

// TestWriteConfigFileCreatesParentDirectory: directory creation stays in
// config. internal/atomicfile never calls MkdirAll.
func TestWriteConfigFileCreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh", "config.toml")
	if err := WriteConfigFile(path, FileConfig{ClientID: "cid"}, false); err != nil {
		t.Fatalf("WriteConfigFile into a missing directory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
}
