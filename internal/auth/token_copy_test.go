package auth

import "testing"

// TestManagerTokenReturnsCopy: Token()'s doc comment always promised a copy,
// but it handed back m.tok, so a caller could mutate the Manager's live
// credential from outside its mutex. Every current caller only reads -- which
// is the reason to close this before one of them stops.
func TestManagerTokenReturnsCopy(t *testing.T) {
	m := newTestManager(t, "https://example.invalid")

	got, _ := m.Token()
	if got == nil {
		t.Fatal("Token() = nil, want the loaded token")
	}
	if got == m.tok {
		t.Error("Token() returned the Manager's own pointer, want a copy")
	}

	got.AccessToken = "clobbered"
	got.RefreshToken = "clobbered"

	again, _ := m.Token()
	if again.AccessToken != "tok" || again.RefreshToken != "rtok" {
		t.Errorf("mutating the returned token changed the Manager's: got (%q, %q), want (tok, rtok)",
			again.AccessToken, again.RefreshToken)
	}
}

// TestManagerTokenNilIsSafe: no token stored means a nil result, not a panic
// dereferencing m.tok to copy it.
func TestManagerTokenNilIsSafe(t *testing.T) {
	m := newTestManager(t, "https://example.invalid")
	m.tok = nil
	if got, _ := m.Token(); got != nil {
		t.Errorf("Token() = %v, want nil when nothing is loaded", got)
	}
}
