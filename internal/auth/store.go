package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/rlarsen/traktctl/internal/config"
	keyring "github.com/zalando/go-keyring"
)

const (
	keyringService = "traktctl"
	keyringUser    = "tokens"
)

// store persists a Token. Primary: macOS Keychain via go-keyring. Fallback:
// a JSON file (used on CI/Linux or when the keychain is unavailable, and for
// the repo-root dev bootstrap tokens.json).
type store struct {
	filePath string // resolved fallback file path
}

// newStore resolves the file-fallback path: ./tokens.json (dev bootstrap, if
// present) else ~/.config/traktctl/tokens.json.
func newStore() *store {
	if st, err := os.Stat("tokens.json"); err == nil && !st.IsDir() {
		return &store{filePath: "tokens.json"}
	}
	path := "tokens.json"
	if dir, err := config.ConfigDir(); err == nil {
		path = filepath.Join(dir, "tokens.json")
	}
	return &store{filePath: path}
}

// load returns the stored token and a human label of where it came from.
// Keychain is tried first, then the fallback file.
func (s *store) load() (*Token, string, error) {
	if blob, err := keyring.Get(keyringService, keyringUser); err == nil && blob != "" {
		t, perr := parseToken([]byte(blob))
		if perr == nil {
			return t, "keychain", nil
		}
	}
	if b, err := os.ReadFile(s.filePath); err == nil {
		t, perr := parseToken(b)
		if perr != nil {
			return nil, "", perr
		}
		return t, s.filePath, nil
	}
	return nil, "", errNoToken
}

// save writes the token to the keychain (primary). If the keychain is
// unavailable it falls back to the file. Returns the location label used.
func (s *store) save(t *Token) (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	if err := keyring.Set(keyringService, keyringUser, string(b)); err == nil {
		return "keychain", nil
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.filePath, b, 0o600); err != nil {
		return "", err
	}
	return s.filePath, nil
}

// clear removes the token from both keychain and file fallback.
func (s *store) clear() error {
	_ = keyring.Delete(keyringService, keyringUser)
	if err := os.Remove(s.filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

var errNoToken = errors.New("no stored token")

func parseToken(b []byte) (*Token, error) {
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
