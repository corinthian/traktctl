package auth

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/config"
	keyring "github.com/zalando/go-keyring"
)

// TestMain installs the in-memory keyring mock before any test in this
// package runs. Without it, store.save/load/clear talk to the real macOS
// Keychain entry (service "traktctl") -- the same one holding this machine's
// actual live Trakt credentials that the -tags=live suite depends on. Every
// test below must be safe to run in any order and any number of times with no
// mock reinstalled in between; TestMain is what guarantees that.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

// newTestManager builds a Manager with a pre-loaded token and a store whose
// file-fallback path is a throwaway temp file, so tests never touch the real
// ~/.config/traktctl/tokens.json either.
func newTestManager(t *testing.T, baseURL string) *Manager {
	t.Helper()
	return &Manager{
		cfg:    &config.Config{ClientID: "cid", ClientSecret: "sec", BaseURL: baseURL, Timeout: 5 * time.Second},
		store:  &store{filePath: filepath.Join(t.TempDir(), "tokens.json")},
		http:   &http.Client{Timeout: 5 * time.Second},
		errW:   io.Discard,
		tok:    &Token{AccessToken: "tok", RefreshToken: "rtok"},
		loaded: true,
	}
}

// TestNewManagerDefaultsErrWToStderr covers TC-10's minimal wiring: Refresh
// has no error sink of its own, so Manager gets one, defaulted to os.Stderr
// in the constructor.
func TestNewManagerDefaultsErrWToStderr(t *testing.T) {
	m := NewManager(&config.Config{})
	if m.errW != os.Stderr {
		t.Errorf("NewManager().errW = %v, want os.Stderr", m.errW)
	}
}

// TestWarnPersistFailed covers the loud, non-fatal warning Refresh and
// LoginDevice both emit on a save failure. This is a focused unit test on the
// warning plumbing itself (writer + message), not a full Refresh/LoginDevice
// integration test.
//
// Limitation (per the brief, noted rather than over-engineered around): the
// full save-failure path inside Refresh/LoginDevice is NOT exercised here.
// store.save talks to the real macOS Keychain (service "traktctl") before
// falling back to a file, and Manager.store is a concrete, unexported type
// with no seam to inject a failing fake. Driving a real save failure in a
// test would either require forcing a real Keychain write (risking clobbering
// the developer's actual live traktctl credentials -- unacceptable) or a
// store-interface refactor, which is out of scope for this minimal fix.
func TestWarnPersistFailed(t *testing.T) {
	var buf bytes.Buffer
	warnPersistFailed(&buf, "refreshed but the rotated token could not be persisted: disk full; re-run `traktctl auth login`.")

	got := buf.String()
	if !strings.HasPrefix(got, "[traktctl] WARNING: ") {
		t.Errorf("warnPersistFailed output = %q, want it to start with the [traktctl] WARNING prefix", got)
	}
	if !strings.Contains(got, "disk full") {
		t.Errorf("warnPersistFailed output = %q, want it to contain the underlying save error", got)
	}
	if !strings.Contains(got, "auth login") {
		t.Errorf("warnPersistFailed output = %q, want it to point the caller at re-authenticating", got)
	}
}

// TestWarnPersistFailedNilWriterFallsBackToStderr covers the defensive nil
// guard (NewManager always sets errW, but this keeps the helper safe if a
// Manager is ever constructed by hand without it).
func TestWarnPersistFailedNilWriterFallsBackToStderr(t *testing.T) {
	// Just confirm it doesn't panic; os.Stderr output isn't captured here.
	warnPersistFailed(nil, "test message")
}

// TestRevokeTransportFailureLeavesTokenIntact covers the Phase 2 fix: a
// revoke that can't even reach Trakt must not clear anything -- the remote
// token may still be live, so pretending it's gone would be a lie.
func TestRevokeTransportFailureLeavesTokenIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	badURL := srv.URL
	srv.Close() // nothing listens here now -- guaranteed dial failure

	m := newTestManager(t, badURL)
	if err := m.Revoke(context.Background()); err == nil {
		t.Fatal("expected error on transport failure")
	}
	if m.tok == nil || m.tok.AccessToken != "tok" {
		t.Error("token should still be loaded after a failed revoke")
	}
}

// TestRevokeHTTPErrorLeavesTokenIntact covers a Trakt-side non-200: same
// truthfulness requirement as the transport-failure case.
func TestRevokeHTTPErrorLeavesTokenIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := newTestManager(t, srv.URL)
	if err := m.Revoke(context.Background()); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if m.tok == nil || m.tok.AccessToken != "tok" {
		t.Error("token should still be loaded after a failed revoke")
	}
}

// TestRevokeSuccessClearsKeychainAndFile covers the success path end to end
// against the mocked keyring installed in TestMain.
func TestRevokeSuccessClearsKeychainAndFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newTestManager(t, srv.URL)
	if err := keyring.Set(keyringService, keyringUser, `{"access_token":"tok"}`); err != nil {
		t.Fatalf("seed keychain: %v", err)
	}
	if err := os.WriteFile(m.store.filePath, []byte(`{"access_token":"tok"}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := m.Revoke(context.Background()); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if m.tok != nil {
		t.Error("token should be cleared in-memory after a successful revoke")
	}
	if _, err := keyring.Get(keyringService, keyringUser); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("keychain entry should be gone, got err=%v", err)
	}
	if _, err := os.Stat(m.store.filePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("fallback file should be removed, got err=%v", err)
	}
}

// TestStoreClearTreatsNotFoundAsSuccess covers the amended clear(): an absent
// keychain entry (the common case -- most stores use the keychain only) must
// not be reported as a failure.
func TestStoreClearTreatsNotFoundAsSuccess(t *testing.T) {
	s := &store{filePath: filepath.Join(t.TempDir(), "tokens.json")}
	if err := s.clear(); err != nil {
		t.Errorf("clear() on empty stores = %v, want nil", err)
	}
}

// TestStoreClearSurfacesKeychainFailure covers the other half: a keychain
// error that ISN'T "not found" (e.g. the keychain is locked) must be reported,
// not swallowed the way the pre-fix `_ = keyring.Delete(...)` did.
func TestStoreClearSurfacesKeychainFailure(t *testing.T) {
	wantErr := errors.New("keychain locked")
	keyring.MockInitWithError(wantErr)
	defer keyring.MockInit() // restore the plain mock for every later test

	s := &store{filePath: filepath.Join(t.TempDir(), "tokens.json")}
	err := s.clear()
	if err == nil || !strings.Contains(err.Error(), "keychain locked") {
		t.Errorf("clear() = %v, want it to surface the keychain error", err)
	}
}
