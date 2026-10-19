package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/corinthian/traktctl/internal/atomicfile"
	"github.com/corinthian/traktctl/internal/config"
	"github.com/corinthian/traktctl/internal/keychain"
)

const (
	keyringService = "traktctl"
	keyringUser    = "tokens"
)

// store persists a Token. Primary: the OS keychain via internal/keychain, whose
// calls are bounded. Fallback: a JSON file (used on CI/Linux or when the
// keychain is unavailable). In file mode the keychain is never called.
//
// kGet/kSet/kDelete are seams for per-operation keychain failure injection --
// something keyring.MockInit cannot give, since it fails (or succeeds) every
// call at once. A nil field falls back to the real keychain function, so a
// zero-value store{filePath: ...} behaves exactly as it did before.
type store struct {
	filePath string // resolved fallback file path

	kGet    func(service, user string) (string, error)
	kSet    func(service, user, password string) error
	kDelete func(service, user string) error

	// errW receives non-fatal reconciliation warnings. nil means os.Stderr.
	errW io.Writer
}

// errFileMode makes save take the file path when token_store is "file".
var errFileMode = errors.New("token_store is file; keychain not used")

// newStore resolves the file-fallback path: ~/.config/traktctl/tokens.json.
// mode is config.TokenStoreFile or anything else (keychain).
func newStore(mode string) *store {
	path := "tokens.json"
	if dir, err := config.ConfigDir(); err == nil {
		path = filepath.Join(dir, "tokens.json")
	}
	s := &store{filePath: path, errW: os.Stderr}
	setKeychainFuncs(s, mode, keychain.Get, keychain.Set, keychain.Delete)
	return s
}

// setKeychainFuncs wires the keychain seams for mode. File mode installs stubs
// that never call get/set/del: load sees nothing stored, save falls through to
// the file, and delete has nothing to remove, so no keychain warning is issued.
func setKeychainFuncs(s *store, mode string,
	get func(string, string) (string, error),
	set func(string, string, string) error,
	del func(string, string) error) {
	if mode == config.TokenStoreFile {
		s.kGet = func(string, string) (string, error) { return "", keychain.ErrNotFound }
		s.kSet = func(string, string, string) error { return errFileMode }
		s.kDelete = func(string, string) error { return keychain.ErrNotFound }
		return
	}
	s.kGet, s.kSet, s.kDelete = get, set, del
}

func (s *store) keyGet() (string, error) {
	if s.kGet != nil {
		return s.kGet(keyringService, keyringUser)
	}
	return keychain.Get(keyringService, keyringUser)
}

func (s *store) keySet(blob string) error {
	if s.kSet != nil {
		return s.kSet(keyringService, keyringUser, blob)
	}
	return keychain.Set(keyringService, keyringUser, blob)
}

func (s *store) keyDelete() error {
	if s.kDelete != nil {
		return s.kDelete(keyringService, keyringUser)
	}
	return keychain.Delete(keyringService, keyringUser)
}

func (s *store) warn(msg string) { warn(s.errW, msg) }

// load returns the stored token and a human label of where it came from.
//
// Precedence is freshest-complete-bundle-wins, not keychain-first. A copy is a
// candidate only if it parses AND carries an access token, so a truncated or
// half-written file never beats a complete keychain entry. Among candidates the
// higher created_at wins -- that is Trakt's issuance stamp, not a local clock,
// so it tracks rotation across a keychain outage that left a stale primary
// entry behind. An exact tie (or two zero stamps, e.g. a hand-edited or legacy
// tokens.json) goes to the keychain, which is still the primary store.
//
// This is a read path: it never writes. Reconciling the two copies is save's
// job, and this ordering is the backstop for when reconciliation could not run.
func (s *store) load() (*Token, string, error) {
	var kTok, fTok *Token
	var kErr error

	if blob, err := s.keyGet(); err != nil {
		if !errors.Is(err, keychain.ErrNotFound) {
			kErr = err
		}
	} else if t, perr := parseToken([]byte(blob)); perr == nil && t.AccessToken != "" {
		kTok = t
	}

	if b, err := os.ReadFile(s.filePath); err == nil {
		if t, perr := parseToken(b); perr == nil && t.AccessToken != "" {
			fTok = t
		}
	}

	switch {
	case kTok != nil && fTok != nil:
		if fTok.CreatedAt > kTok.CreatedAt {
			return fTok, s.filePath, nil
		}
		return kTok, "keychain", nil
	case kTok != nil:
		return kTok, "keychain", nil
	case fTok != nil:
		if kErr != nil {
			// A bound timeout already reads "keychain unavailable: ..."; only
			// other keychain errors need the prefix.
			msg := kErr.Error()
			if !errors.Is(kErr, keychain.ErrUnavailable) {
				msg = "keychain unavailable (" + msg + ")"
			}
			s.warn(msg + "; falling back to " + s.filePath)
		}
		return fTok, s.filePath, nil
	}

	// No usable copy anywhere. A keychain failure is reported as itself so the
	// caller can tell "the keychain is locked" from "you are not logged in".
	if kErr != nil {
		return nil, "", fmt.Errorf("reading keychain: %w", kErr)
	}
	return nil, "", errNoToken
}

// save writes the token to the keychain (primary). If the keychain is
// unavailable it falls back to the file. Returns the location label used.
//
// Whichever copy wins, the other is best-effort removed so a later load cannot
// resurrect a superseded bundle. A reconciliation failure is a warning, never a
// command failure: the authoritative copy is already written, and load's
// freshest-wins rule covers whatever was left behind.
func (s *store) save(t *Token) (string, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	if err := s.keySet(string(b)); err == nil {
		if rerr := os.Remove(s.filePath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			s.warn("token stored in the keychain but the fallback file " + s.filePath +
				" could not be removed: " + rerr.Error())
		}
		return "keychain", nil
	}
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0o700); err != nil {
		return "", err
	}
	if err := atomicfile.Write(s.filePath, b); err != nil {
		return "", err
	}
	if derr := s.keyDelete(); derr != nil && !errors.Is(derr, keychain.ErrNotFound) {
		s.warn("token stored in " + s.filePath + " but the superseded keychain entry" +
			" could not be removed: " + derr.Error())
	}
	return s.filePath, nil
}

// clear removes the token from both keychain and file fallback. A keychain
// entry that was never there (ErrNotFound) is not a failure -- the goal state
// (nothing stored) is already met -- but any other keychain error is surfaced
// so a caller like Revoke doesn't report success while a stale token remains
// readable. Both stores are attempted regardless of the other's outcome.
func (s *store) clear() error {
	var errs []error
	if err := s.keyDelete(); err != nil && !errors.Is(err, keychain.ErrNotFound) {
		errs = append(errs, fmt.Errorf("keychain: %w", err))
	}
	if err := os.Remove(s.filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("file: %w", err))
	}
	return errors.Join(errs...)
}

var errNoToken = errors.New("no stored token")

func parseToken(b []byte) (*Token, error) {
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
