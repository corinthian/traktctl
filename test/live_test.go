//go:build live

// Package live is the live smoke suite (R7c). It exercises the compiled binary
// and the live Trakt API using credentials resolved exactly as the production
// CLI does — client_id/client_secret from config.toml (or TRAKT_CLIENT_ID /
// TRAKT_CLIENT_SECRET), and the access token from the auth token store
// (macOS Keychain, then ./tokens.json, then ~/.config/traktctl/tokens.json).
// The whole suite t.Skip()s when credentials are absent, so it is a no-op on a
// machine without creds. Excluded from the default `go test` by the `live`
// build tag.
//
//	go test -tags=live ./test/...
//
// Coverage:
//   - Part A: one read per v1 command group (table-driven, subtest per group);
//     asserts shape only (ok:true, data present, meta where applicable, array
//     for list reads) — never volatile titles/ids/counts.
//   - Part B: the OAuth refresh path. Because refreshing ROTATES the live
//     refresh token, Part B is isolated behind a second build tag and lives in
//     live_refresh_test.go (`go test -tags=live,refresh ./test/...`). It is NOT
//     run by the default `-tags=live` invocation.
//   - Part C: the existence probes ported from test/live_probes.sh — bogus-token
//     /oauth/revoke and trakt:0 no-op sync mutations. Safe by construction: the
//     real token is never revoked and no account state changes.
//
// Account-mutating verbs (sync add/remove with --confirm, settings/reorder/
// update-item) are NOT exercised here; those are covered by the Phase 4
// mutation-verification work with throwaway-list data.
package live

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/auth"
	"github.com/corinthian/traktctl/internal/config"
)

// repoRoot resolves the repo root from this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller")
	}
	return filepath.Dir(filepath.Dir(file)) // test/ -> repo root
}

// creds bundles the resolved live credentials.
type creds struct {
	clientID     string
	clientSecret string
	bearer       string
}

// loadCreds resolves credentials the same way the production CLI does: config
// (file/env) for client_id/secret, the auth token store for the bearer. It
// loads config relative to the repo root so config.toml/tokens.json in the repo
// are found regardless of the test's working directory.
func loadCreds(t *testing.T) creds {
	t.Helper()
	root := repoRoot(t)
	// config.Load and the token store both resolve ./config.toml and
	// ./tokens.json relative to the process working directory, so anchor there.
	restore := chdir(t, root)
	defer restore()

	cfg, err := config.Load(config.Flags{})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	mgr := auth.NewManager(cfg)
	return creds{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		bearer:       mgr.Bearer(),
	}
}

// requireCreds skips the calling test unless full live credentials are present.
func requireCreds(t *testing.T) creds {
	t.Helper()
	c := loadCreds(t)
	if c.clientID == "" || c.bearer == "" {
		t.Skip("live credentials absent (need client_id + stored access token); skipping live suite")
	}
	return c
}

// chdir changes the working directory and returns a restore func.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() { _ = os.Chdir(prev) }
}

// run executes the built binary from the repo root and returns the parsed
// envelope. The binary must be built first (build.sh or `go build`).
func run(t *testing.T, args ...string) map[string]any {
	t.Helper()
	root := repoRoot(t)
	bin := filepath.Join(root, "dist", "traktctl")
	cmd := exec.Command(bin, args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("%v exited %d: %s", args, ee.ExitCode(), ee.Stderr)
		}
		t.Fatalf("%v: %v (is dist/traktctl built?)", args, err)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("%v: bad JSON: %v", args, err)
	}
	return env
}

// assertEnvelope enforces the R7c assertion contract: exit 0 (run would have
// failed otherwise), ok:true with non-nil data, meta present unless local, and
// a JSON array when isList.
func assertEnvelope(t *testing.T, isLocal, isList bool, args ...string) {
	t.Helper()
	env := run(t, args...)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("%v: ok!=true: %v", args, env["error"])
	}
	if env["data"] == nil {
		t.Fatalf("%v: expected non-nil data", args)
	}
	if !isLocal {
		if env["meta"] == nil {
			t.Errorf("%v: expected meta present", args)
		}
	}
	if isList {
		// Contract: list reads return a JSON array (len >= 0); count is never
		// asserted because it drifts.
		if _, ok := env["data"].([]any); !ok {
			t.Fatalf("%v: expected data to be a JSON array, got %T", args, env["data"])
		}
	}
}

// smokeCase is one Part A read: one v1 command group, shape-only assertions.
type smokeCase struct {
	name    string   // subtest name (the v1 group)
	args    []string // CLI args
	isLocal bool     // local command (no Trakt call, no meta block)
	isList  bool     // data is a JSON array
}

// TestLiveSmokeGroups exercises one read for every v1 command group. Shapes
// (isLocal/isList) were confirmed empirically against the live API; values are
// never asserted because they drift. The calendar date is a fixed literal so
// the test is deterministic.
func TestLiveSmokeGroups(t *testing.T) {
	requireCreds(t)
	cases := []smokeCase{
		{"auth", []string{"auth", "status"}, true, false},
		{"config", []string{"config", "path"}, true, false},
		{"search", []string{"search", "query", "--q", "blade runner", "--type", "movie", "--limit", "2"}, false, true},
		{"movie", []string{"movie", "trending", "--limit", "2"}, false, true},
		{"show", []string{"show", "trending", "--limit", "2"}, false, true},
		{"season", []string{"season", "summary", "--show", "breaking-bad"}, false, true},
		{"episode", []string{"episode", "summary", "--show", "breaking-bad", "--season", "1", "--episode", "1"}, false, false},
		{"calendar", []string{"calendar", "all-shows", "--start", "2026-06-01", "--days", "1"}, false, true},
		{"user", []string{"user", "settings"}, false, false},
		{"sync", []string{"sync", "activities"}, false, false},
		{"recommend", []string{"recommend", "movies", "--limit", "2"}, false, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			assertEnvelope(t, c.isLocal, c.isList, c.args...)
		})
	}
}

// TestLiveSearchByID exercises the by-id lookup path.
func TestLiveSearchByID(t *testing.T) {
	requireCreds(t)
	assertEnvelope(t, false, true, "search", "id", "--id-type", "imdb", "--id", "tt0468569")
}

// TestLive204 covers the empty-body path: an ended show has no next episode, so
// Trakt returns 204 — which must surface as ok:true / data:null, not an error.
func TestLive204(t *testing.T) {
	requireCreds(t)
	env := run(t, "show", "next-episode", "--id", "breaking-bad")
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("204 path: expected ok:true, got %v", env["error"])
	}
	if env["data"] != nil {
		t.Errorf("204 path: expected data:null, got %v", env["data"])
	}
}

// --- Part C: existence probes ported from test/live_probes.sh ---------------
//
// SAFE BY DESIGN, identical to the shell script:
//   - /oauth/revoke is sent a BOGUS token, so the real token is never revoked.
//   - sync mutations use trakt id 0 -> not_found -> nothing added/removed.
// Discriminator (per the script): 200/201/400/401/403 -> route exists;
// 404/405/412 -> no such route.

const apiBase = "https://api.trakt.tv"

// probeNoOp is the trakt:0 no-op body: a non-existent id, so the mutation is a
// guaranteed no-op against the real account.
const probeNoOp = `{"movies":[{"ids":{"trakt":0}}]}`

// routeExists reports whether a status code indicates the route is present.
func routeExists(code int) bool {
	switch code {
	case 200, 201, 400, 401, 403:
		return true
	default: // 404, 405, 412
		return false
	}
}

// postProbe issues a POST and returns its status code. The caller supplies the
// body; the Authorization header is attached only when withAuth is set, to
// mirror live_probes.sh exactly (revoke uses api-key only; sync uses bearer).
func postProbe(t *testing.T, c creds, path, body string, withAuth bool) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, apiBase+path, bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build request %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.clientID)
	if withAuth && c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestLiveProbes ports the existence probes: each subtest proves a route exists
// without mutating the account.
func TestLiveProbes(t *testing.T) {
	c := requireCreds(t)

	t.Run("oauth/revoke-bogus", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{
			"token":         "DEADBEEFnot-a-real-token",
			"client_id":     c.clientID,
			"client_secret": c.clientSecret,
		})
		code := postProbe(t, c, "/oauth/revoke", string(body), false)
		// Real token untouched: a bogus token yields 200 (idempotent) or 400.
		if !routeExists(code) {
			t.Errorf("/oauth/revoke: route missing (HTTP %d)", code)
		}
	})

	syncRoutes := []string{
		"/sync/collection", "/sync/collection/remove",
		"/sync/watchlist", "/sync/watchlist/remove",
		"/sync/favorites", "/sync/favorites/remove",
	}
	for _, path := range syncRoutes {
		path := path
		t.Run("noop"+path, func(t *testing.T) {
			code := postProbe(t, c, path, probeNoOp, true)
			if !routeExists(code) {
				t.Errorf("%s: route missing (HTTP %d)", path, code)
			}
		})
	}
}
