package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/corinthian/traktctl/internal/config"
)

// TestOAuthClientRefusesCrossOriginRedirect: NewManager's http.Client now
// routes through the same xhttp.NewClient RedirectPolicy as the API client
// (SameOrigin: true, MaxHops: 10). A device-code endpoint that redirects to
// another host must never be followed -- the bearer exchange (client_id here,
// client_secret and tokens elsewhere) must never be retargeted.
func TestOAuthClientRefusesCrossOriginRedirect(t *testing.T) {
	var otherHit bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHit = true
		w.Write([]byte(`{"device_code":"d","user_code":"u","verification_url":"v","interval":5}`))
	}))
	defer other.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/oauth/device/code", http.StatusFound)
	}))
	defer srv.Close()

	m := NewManager(&config.Config{ClientID: "cid", BaseURL: srv.URL})
	_, err := m.requestDeviceCode(context.Background())
	if err == nil {
		t.Fatal("expected an error for a cross-origin redirect, got nil")
	}
	if otherHit {
		t.Error("cross-origin target was hit; the redirect should have been refused before it was followed")
	}
}

// TestOAuthClientBoundsRedirectHops: an 11-hop same-origin chain on
// /oauth/device/code fails at the 10-hop cap rather than being followed
// indefinitely.
func TestOAuthClientBoundsRedirectHops(t *testing.T) {
	var hits int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, srv.URL+"/oauth/device/code/hop"+strconv.Itoa(hits), http.StatusFound)
	}))
	defer srv.Close()

	m := NewManager(&config.Config{ClientID: "cid", BaseURL: srv.URL})
	_, err := m.requestDeviceCode(context.Background())
	if err == nil {
		t.Fatal("expected an error for a redirect chain over the hop cap, got nil")
	}
	if hits <= 10 {
		t.Errorf("only saw %d hops before refusal; the chain should run past the 10-hop cap", hits)
	}
}
