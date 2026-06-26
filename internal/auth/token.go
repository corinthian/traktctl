// Package auth implements the OAuth device flow, token storage (keychain with
// file fallback), refresh, and status. It also provides the token source the
// HTTP client uses to inject the bearer header and auto-refresh once on 401.
package auth

import "time"

// Token is the OAuth token bundle as returned by Trakt and persisted locally.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
}

// ExpiresAt returns the absolute expiry instant.
func (t *Token) ExpiresAt() time.Time {
	return time.Unix(t.CreatedAt+t.ExpiresIn, 0)
}

// Expired reports whether the access token is past its lifetime. A small skew
// margin avoids racing the boundary.
func (t *Token) Expired() bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	return time.Now().After(t.ExpiresAt().Add(-60 * time.Second))
}
