package auth

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

// fakeKeyring is an in-memory keychain with per-operation failure injection.
// keyring.MockInit can only fail (or succeed) every call at once, which cannot
// express the scenario this fix exists for: a Set that fails while a later Get
// succeeds. Every store built here also has an explicit filePath under
// t.TempDir(), so nothing touches ~/.config/traktctl.
type fakeKeyring struct {
	blob    string
	present bool

	getErr error
	setErr error
	delErr error
}

func (f *fakeKeyring) get(string, string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	if !f.present {
		return "", keyring.ErrNotFound
	}
	return f.blob, nil
}

func (f *fakeKeyring) set(_, _, blob string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.blob, f.present = blob, true
	return nil
}

func (f *fakeKeyring) del(string, string) error {
	if f.delErr != nil {
		return f.delErr
	}
	if !f.present {
		return keyring.ErrNotFound
	}
	f.blob, f.present = "", false
	return nil
}

// newFakeStore wires a store to an isolated fake keychain and temp file path.
func newFakeStore(t *testing.T) (*store, *fakeKeyring) {
	t.Helper()
	fk := &fakeKeyring{}
	return &store{
		filePath: filepath.Join(t.TempDir(), "tokens.json"),
		kGet:     fk.get,
		kSet:     fk.set,
		kDelete:  fk.del,
		errW:     io.Discard,
	}, fk
}

func mustMarshal(t *testing.T, tok *Token) string {
	t.Helper()
	b, err := json.Marshal(tok)
	if err != nil {
		t.Fatalf("marshal token: %v", err)
	}
	return string(b)
}

func seedKeychain(t *testing.T, fk *fakeKeyring, tok *Token) {
	t.Helper()
	fk.blob, fk.present = mustMarshal(t, tok), true
}

func seedFile(t *testing.T, s *store, tok *Token) {
	t.Helper()
	if err := os.WriteFile(s.filePath, []byte(mustMarshal(t, tok)), 0o600); err != nil {
		t.Fatalf("seed token file: %v", err)
	}
}

// TestStoreLoadPrefersFresherFile inverts the old keychain-first rule. A file
// copy issued later than the keychain copy is the live credential -- that is
// what a rotation persisted during a keychain outage looks like -- and
// preferring the keychain there hands back a token Trakt has already retired.
func TestStoreLoadPrefersFresherFile(t *testing.T) {
	s, fk := newFakeStore(t)
	seedKeychain(t, fk, &Token{AccessToken: "old", CreatedAt: 100})
	seedFile(t, s, &Token{AccessToken: "new", CreatedAt: 200})

	tok, loc, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "new" {
		t.Errorf("access token = %q, want the fresher file copy %q", tok.AccessToken, "new")
	}
	if loc != s.filePath {
		t.Errorf("location = %q, want %q", loc, s.filePath)
	}
}

// TestStoreLoadPrefersKeychainOnTie covers the tie-break: equal created_at (or
// two zero stamps) goes to the keychain, which is still the primary store.
func TestStoreLoadPrefersKeychainOnTie(t *testing.T) {
	s, fk := newFakeStore(t)
	seedKeychain(t, fk, &Token{AccessToken: "kc", CreatedAt: 100})
	seedFile(t, s, &Token{AccessToken: "file", CreatedAt: 100})

	tok, loc, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "kc" || loc != "keychain" {
		t.Errorf("load() = (%q, %q), want the keychain copy on a tie", tok.AccessToken, loc)
	}
}

// TestStoreLoadIgnoresIncompleteFile guards the candidate test: a newer but
// half-written file (parses, no access_token) must never beat a complete
// keychain bundle, or a truncated write silently logs the user out.
func TestStoreLoadIgnoresIncompleteFile(t *testing.T) {
	s, fk := newFakeStore(t)
	seedKeychain(t, fk, &Token{AccessToken: "kc", CreatedAt: 100})
	seedFile(t, s, &Token{AccessToken: "", CreatedAt: 900})

	tok, loc, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "kc" || loc != "keychain" {
		t.Errorf("load() = (%q, %q), want the complete keychain copy", tok.AccessToken, loc)
	}
}

// TestStoreLoadIgnoresEmptyCandidates: both copies present, neither usable.
// The answer is "nothing stored", not a half bundle and not a panic.
func TestStoreLoadIgnoresEmptyCandidates(t *testing.T) {
	s, fk := newFakeStore(t)
	fk.blob, fk.present = `{"access_token":""}`, true
	if err := os.WriteFile(s.filePath, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	tok, loc, err := s.load()
	if !errors.Is(err, errNoToken) {
		t.Errorf("load() err = %v, want errNoToken", err)
	}
	if tok != nil || loc != "" {
		t.Errorf("load() = (%v, %q), want (nil, \"\")", tok, loc)
	}
}

// TestStoreSaveClearsFallbackFile: once the keychain holds the new bundle, the
// file copy is superseded and must go, so a later load cannot resurrect it.
func TestStoreSaveClearsFallbackFile(t *testing.T) {
	s, _ := newFakeStore(t)
	seedFile(t, s, &Token{AccessToken: "old", CreatedAt: 100})

	loc, err := s.save(&Token{AccessToken: "new", CreatedAt: 200})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if loc != "keychain" {
		t.Errorf("save location = %q, want keychain", loc)
	}
	if _, err := os.Stat(s.filePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fallback file still present after a keychain save, err = %v", err)
	}
}

// TestStoreSaveFallbackClearsKeychain is the mirror: when the keychain write
// fails and the file becomes authoritative, the stale keychain entry is what
// must go.
func TestStoreSaveFallbackClearsKeychain(t *testing.T) {
	s, fk := newFakeStore(t)
	seedKeychain(t, fk, &Token{AccessToken: "old", CreatedAt: 100})
	fk.setErr = errors.New("keychain locked")

	loc, err := s.save(&Token{AccessToken: "new", CreatedAt: 200})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if loc != s.filePath {
		t.Errorf("save location = %q, want %q", loc, s.filePath)
	}
	if fk.present {
		t.Error("superseded keychain entry still present after a file-fallback save")
	}
	b, rerr := os.ReadFile(s.filePath)
	if rerr != nil {
		t.Fatalf("read token file: %v", rerr)
	}
	var got Token
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("token file is not valid JSON: %v", err)
	}
	if got.AccessToken != "new" {
		t.Errorf("file access token = %q, want new", got.AccessToken)
	}
}

// TestStoreLoadKeychainErrorSurfaces: a keychain that errors for a reason
// other than "not found", with no file to fall back to, is not the same thing
// as "not logged in" and must not be reported as it.
func TestStoreLoadKeychainErrorSurfaces(t *testing.T) {
	s, fk := newFakeStore(t)
	fk.getErr = errors.New("keychain locked")

	_, _, err := s.load()
	if err == nil {
		t.Fatal("load() = nil error, want the keychain failure")
	}
	if errors.Is(err, errNoToken) {
		t.Errorf("load() err = %v, want a keychain error, not errNoToken", err)
	}
}

// TestStoreLoadKeychainErrorFallsBackToFile: the same failure with a usable
// file copy is recoverable -- the command proceeds on the file token.
func TestStoreLoadKeychainErrorFallsBackToFile(t *testing.T) {
	s, fk := newFakeStore(t)
	fk.getErr = errors.New("keychain locked")
	seedFile(t, s, &Token{AccessToken: "file", CreatedAt: 100})

	tok, loc, err := s.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "file" || loc != s.filePath {
		t.Errorf("load() = (%q, %q), want the file copy", tok.AccessToken, loc)
	}
}

// TestStoreRotationSurvivesKeychainOutage is the review's actual scenario, end
// to end. A refresh rotates the token while the keychain is unwritable AND
// undeletable, so the file gets the new bundle and the stale keychain entry
// stays put -- reconciliation could not run. The next command, keychain
// recovered, must still see the rotated token. This is what makes the load
// precedence rule the backstop rather than a nicety.
func TestStoreRotationSurvivesKeychainOutage(t *testing.T) {
	s, fk := newFakeStore(t)
	seedKeychain(t, fk, &Token{AccessToken: "stale", CreatedAt: 100})

	fk.setErr = errors.New("keychain locked")
	fk.delErr = errors.New("keychain locked")
	loc, err := s.save(&Token{AccessToken: "rotated", CreatedAt: 200})
	if err != nil {
		t.Fatalf("save during keychain outage: %v", err)
	}
	if loc != s.filePath {
		t.Fatalf("save location = %q, want the file fallback %q", loc, s.filePath)
	}
	if !fk.present {
		t.Fatal("test setup: the stale keychain entry should have survived a failing delete")
	}

	// Keychain recovers on the next invocation.
	fk.setErr, fk.delErr = nil, nil
	tok, loadLoc, err := s.load()
	if err != nil {
		t.Fatalf("load after outage: %v", err)
	}
	if tok.AccessToken != "rotated" {
		t.Errorf("access token = %q, want the rotated one; the stale keychain copy won", tok.AccessToken)
	}
	if loadLoc != s.filePath {
		t.Errorf("location = %q, want %q", loadLoc, s.filePath)
	}
}

// TestStoreNilInjectionUsesRealKeyring keeps the zero-value store working: the
// injected fields are a test seam, not a required constructor argument, and a
// store{filePath: ...} literal must still route through the (mocked, see
// TestMain) keyring rather than panicking on a nil func.
func TestStoreNilInjectionUsesRealKeyring(t *testing.T) {
	s := &store{filePath: filepath.Join(t.TempDir(), "tokens.json")}
	if err := s.clear(); err != nil {
		t.Errorf("clear() on a zero-value store = %v, want nil", err)
	}
	if _, _, err := s.load(); err == nil {
		t.Error("load() on empty stores = nil error, want errNoToken")
	}
}
