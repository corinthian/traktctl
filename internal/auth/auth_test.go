package auth

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
)

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
