package auth

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/keychain"
)

// countingKeychain fails the test's purpose if any call reaches it.
type countingKeychain struct{ get, set, del int }

func (c *countingKeychain) wire(s *store, mode string) {
	setKeychainFuncs(s, mode,
		func(string, string) (string, error) { c.get++; return "", keychain.ErrNotFound },
		func(string, string, string) error { c.set++; return nil },
		func(string, string) error { c.del++; return keychain.ErrNotFound },
	)
}

func TestFileModeNeverCallsKeychain(t *testing.T) {
	var warnings bytes.Buffer
	s := &store{filePath: filepath.Join(t.TempDir(), "tokens.json"), errW: &warnings}
	ck := &countingKeychain{}
	ck.wire(s, config.TokenStoreFile)

	if _, _, err := s.load(); !errors.Is(err, errNoToken) {
		t.Fatalf("load on empty store = %v, want errNoToken", err)
	}
	loc, err := s.save(&Token{AccessToken: "a", CreatedAt: 1})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if loc != s.filePath {
		t.Errorf("save location = %q, want %q", loc, s.filePath)
	}
	tok, loc, err := s.load()
	if err != nil || tok.AccessToken != "a" || loc != s.filePath {
		t.Errorf("load = %v, %q, %v; want the file token", tok, loc, err)
	}
	if err := s.clear(); err != nil {
		t.Errorf("clear: %v", err)
	}
	if ck.get+ck.set+ck.del != 0 {
		t.Errorf("keychain calls in file mode: get=%d set=%d del=%d, want none", ck.get, ck.set, ck.del)
	}
	if warnings.Len() != 0 {
		t.Errorf("file mode warned: %q", warnings.String())
	}
}

func TestKeychainModeUsesKeychain(t *testing.T) {
	s := &store{filePath: filepath.Join(t.TempDir(), "tokens.json"), errW: &bytes.Buffer{}}
	ck := &countingKeychain{}
	ck.wire(s, config.TokenStoreKeychain)
	if _, err := s.save(&Token{AccessToken: "a"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if ck.set != 1 {
		t.Errorf("keychain set calls = %d, want 1", ck.set)
	}
}

// TestLoadFallsBackWhenKeychainTimesOut: the bound itself is proven in
// internal/keychain; here a kGet that reports the timeout must yield the file
// token with the warning.
func TestLoadFallsBackWhenKeychainTimesOut(t *testing.T) {
	var warnings bytes.Buffer
	s, _ := newFakeStore(t)
	s.errW = &warnings
	s.kGet = func(string, string) (string, error) {
		return "", fmt.Errorf("%w: timed out after 5s", keychain.ErrUnavailable)
	}
	seedFile(t, s, &Token{AccessToken: "file", CreatedAt: 10})

	tok, loc, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "file" || loc != s.filePath {
		t.Errorf("load = %q from %q, want the file token", tok.AccessToken, loc)
	}
	if n := strings.Count(warnings.String(), "keychain unavailable"); n != 1 {
		t.Errorf("warning = %q, want exactly one keychain unavailable notice, got %d", warnings.String(), n)
	}
}

func TestManagerTokenStore(t *testing.T) {
	for in, want := range map[string]string{"": "keychain", "keychain": "keychain", "file": "file"} {
		m := &Manager{cfg: &config.Config{TokenStore: in}}
		if got := m.TokenStore(); got != want {
			t.Errorf("TokenStore(%q) = %q, want %q", in, got, want)
		}
	}
}
