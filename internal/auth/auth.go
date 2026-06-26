package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/rlarsen/traktctl/internal/config"
)

// Manager owns the token lifecycle and satisfies the client's TokenSource
// interface (Bearer/HasToken/Refresh).
type Manager struct {
	cfg   *config.Config
	store *store
	http  *http.Client

	mu       sync.Mutex
	tok      *Token
	location string
	loaded   bool
}

// NewManager builds a token manager. Tokens are loaded lazily on first use.
func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		cfg:   cfg,
		store: newStore(),
		http:  &http.Client{Timeout: cfg.Timeout},
	}
}

// ensureLoaded resolves the active token once: an explicit flag/env access
// token wins; otherwise the stored token (keychain or file) is used.
func (m *Manager) ensureLoaded() {
	if m.loaded {
		return
	}
	m.loaded = true
	if m.cfg.AccessToken != "" {
		m.tok = &Token{
			AccessToken:  m.cfg.AccessToken,
			RefreshToken: m.cfg.RefreshToken,
			TokenType:    "bearer",
			// No expiry known for an injected token; treat as long-lived.
			ExpiresIn: int64((365 * 24 * time.Hour).Seconds()),
			CreatedAt: time.Now().Unix(),
		}
		m.location = "flag/env"
		return
	}
	if t, loc, err := m.store.load(); err == nil {
		m.tok, m.location = t, loc
	}
}

// Bearer returns the current access token, or "" if none is available.
func (m *Manager) Bearer() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	if m.tok == nil {
		return ""
	}
	return m.tok.AccessToken
}

// HasToken reports whether any token (valid or not) is available to refresh.
func (m *Manager) HasToken() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	return m.tok != nil && m.tok.RefreshToken != ""
}

// Token returns a copy of the active token and its storage location.
func (m *Manager) Token() (*Token, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	return m.tok, m.location
}

// Refresh exchanges the refresh token for a new access token and persists it.
// Safe for concurrent callers; only one refresh runs at a time.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	if m.tok == nil || m.tok.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}
	body := map[string]string{
		"refresh_token": m.tok.RefreshToken,
		"client_id":     m.cfg.ClientID,
		"client_secret": m.cfg.ClientSecret,
		"redirect_uri":  "urn:ietf:wg:oauth:2.0:oob",
		"grant_type":    "refresh_token",
	}
	tok, err := m.postToken(ctx, "/oauth/token", body)
	if err != nil {
		return err
	}
	m.tok = tok
	if loc, serr := m.store.save(tok); serr == nil {
		m.location = loc
	}
	return nil
}

// DeviceCode is the response from POST /oauth/device/code.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// LoginDevice runs the full device flow: request a code, prompt the user on
// stderr (optionally opening a browser), poll until authorized, persist the
// token, and return its storage location. progress is written to errW.
func (m *Manager) LoginDevice(ctx context.Context, errW io.Writer, openBrowser bool) (*Token, string, error) {
	dc, err := m.requestDeviceCode(ctx)
	if err != nil {
		return nil, "", err
	}
	fmt.Fprintf(errW, "To authorize traktctl, visit:\n  %s\nand enter code: %s\n", dc.VerificationURL, dc.UserCode)
	if openBrowser {
		_ = openURL(dc.VerificationURL)
	}
	tok, err := m.pollDeviceToken(ctx, dc, errW)
	if err != nil {
		return nil, "", err
	}
	m.mu.Lock()
	m.tok = tok
	m.loaded = true
	loc, _ := m.store.save(tok)
	m.location = loc
	m.mu.Unlock()
	return tok, loc, nil
}

func (m *Manager) requestDeviceCode(ctx context.Context) (*DeviceCode, error) {
	b, _ := json.Marshal(map[string]string{"client_id": m.cfg.ClientID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.BaseURL+"/oauth/device/code", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: HTTP %d", resp.StatusCode)
	}
	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	return &dc, nil
}

// pollDeviceToken polls /oauth/device/token until the user authorizes or the
// code expires. Status mapping per Trakt: 200 ok, 400 pending, 404 not-found,
// 409 already-used, 410 expired, 418 denied, 429 slow-down.
func (m *Manager) pollDeviceToken(ctx context.Context, dc *DeviceCode, errW io.Writer) (*Token, error) {
	interval := time.Duration(dc.Interval) * time.Second
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	body := map[string]string{
		"code":          dc.DeviceCode,
		"client_id":     m.cfg.ClientID,
		"client_secret": m.cfg.ClientSecret,
	}
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device code expired before authorization")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		tok, status, err := m.tryDeviceToken(ctx, body)
		if err != nil {
			return nil, err
		}
		switch status {
		case http.StatusOK:
			return tok, nil
		case http.StatusBadRequest:
			// authorization_pending — keep waiting.
		case http.StatusTooManyRequests:
			interval += time.Second // slow_down
		case http.StatusNotFound:
			return nil, fmt.Errorf("invalid device code")
		case http.StatusConflict:
			return nil, fmt.Errorf("device code already used")
		case http.StatusGone:
			return nil, fmt.Errorf("device code expired")
		case 418:
			return nil, fmt.Errorf("authorization denied by user")
		default:
			return nil, fmt.Errorf("unexpected status while polling: HTTP %d", status)
		}
	}
}

func (m *Manager) tryDeviceToken(ctx context.Context, body map[string]string) (*Token, int, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.BaseURL+"/oauth/device/token", bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, resp.StatusCode, nil
	}
	var t Token
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, resp.StatusCode, err
	}
	return &t, resp.StatusCode, nil
}

// postToken is the shared helper for token/refresh exchanges.
func (m *Manager) postToken(ctx context.Context, path string, body map[string]string) (*Token, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint %s failed: HTTP %d", path, resp.StatusCode)
	}
	var t Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke calls POST /oauth/revoke for the active access token, then clears
// local storage.
func (m *Manager) Revoke(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	if m.tok != nil && m.tok.AccessToken != "" {
		body := map[string]string{
			"token":         m.tok.AccessToken,
			"client_id":     m.cfg.ClientID,
			"client_secret": m.cfg.ClientSecret,
		}
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.BaseURL+"/oauth/revoke", bytes.NewReader(b))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if resp, derr := m.http.Do(req); derr == nil {
				resp.Body.Close()
			}
		}
	}
	m.tok = nil
	return m.store.clear()
}

// Logout clears local token storage without contacting Trakt.
func (m *Manager) Logout() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tok = nil
	return m.store.clear()
}

func openURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	default:
		return nil
	}
}
