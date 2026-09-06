package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestTokenFileWrittenAtMode0600 pins the token file's mode across the swap to
// internal/atomicfile.
func TestTokenFileWrittenAtMode0600(t *testing.T) {
	s, fk := newFakeStore(t)
	fk.setErr = errors.New("keychain unavailable")
	if _, err := s.save(&Token{AccessToken: "tok"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(s.filePath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}
}

// TestTokenSaveIsAtomic: a reader never sees a partial token file, and a write
// that fails leaves the previous token readable rather than truncated.
func TestTokenSaveIsAtomic(t *testing.T) {
	s, fk := newFakeStore(t)
	fk.setErr = errors.New("keychain unavailable")
	if _, err := s.save(&Token{AccessToken: "first"}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// A directory the writer cannot create a temp file in fails the write
	// before anything touches the existing file.
	if os.Geteuid() == 0 {
		t.Skip("running as root; a 0500 directory is still writable")
	}
	dir := filepath.Dir(s.filePath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	if _, err := s.save(&Token{AccessToken: "second"}); err == nil {
		t.Fatal("save into an unwritable directory = nil error, want a failure")
	}

	b, err := os.ReadFile(s.filePath)
	if err != nil {
		t.Fatalf("the previous token is no longer readable: %v", err)
	}
	var tok Token
	if err := json.Unmarshal(b, &tok); err != nil {
		t.Fatalf("the previous token file is not intact JSON: %v (%q)", err, b)
	}
	if tok.AccessToken != "first" {
		t.Errorf("access token = %q, want the previous %q", tok.AccessToken, "first")
	}
	if leftovers := tempFiles(t, dir); len(leftovers) != 0 {
		t.Errorf("a failed write left temp files behind: %v", leftovers)
	}
}

func tempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var out []string
	for _, e := range entries {
		if len(e.Name()) > 5 && e.Name()[:5] == ".tmp-" {
			out = append(out, e.Name())
		}
	}
	return out
}
