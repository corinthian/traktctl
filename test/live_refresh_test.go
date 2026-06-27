//go:build live && refresh

// Part B — OAuth refresh path. ISOLATED behind a second build tag because
// refreshing ROTATES the live refresh token: each run invalidates the previous
// refresh token and persists a new one back to the store. Running it in routine
// CI would churn the user's token, so it is opt-in:
//
//	go test -tags=live,refresh -run TestLiveRefresh ./test/...
//
// It is NOT compiled or run by the default `-tags=live` suite. The new token is
// written back to the store by `auth refresh` itself, so the next live run still
// has valid creds.
package live

import "testing"

// TestLiveRefresh verifies the refresh path end-to-end:
//  1. `auth refresh` succeeds and writes a new token (ok:true).
//  2. an authed read (`user settings`) succeeds with the refreshed token.
func TestLiveRefresh(t *testing.T) {
	requireCreds(t)

	// 1. Refresh rotates and persists a new token.
	env := run(t, "auth", "refresh")
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("auth refresh: ok!=true: %v", env["error"])
	}

	// 2. An authed read succeeds with the refreshed token.
	assertEnvelope(t, false, false, "user", "settings")
}
