package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFileFixesPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := AtomicWriteFile(path, []byte("new")); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
}

// TestAtomicWriteFileReplacesSymlinkTargetUntouched covers the whole point of
// the os.Rename-based swap: writing to a path that is a symlink must replace
// the symlink itself, never write through it into whatever it points at.
func TestAtomicWriteFileReplacesSymlinkTargetUntouched(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("original target content"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := AtomicWriteFile(link, []byte("new content")); err != nil {
		t.Fatalf("AtomicWriteFile: %v", err)
	}

	linkInfo, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		t.Error("path should no longer be a symlink after AtomicWriteFile")
	}
	got, err := os.ReadFile(link)
	if err != nil {
		t.Fatalf("read link: %v", err)
	}
	if string(got) != "new content" {
		t.Errorf("link content = %q, want %q", got, "new content")
	}

	targetContent, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(targetContent) != "original target content" {
		t.Errorf("symlink target was modified; content = %q, want it untouched", targetContent)
	}
}
