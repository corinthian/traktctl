//go:build live

// Package live is the live smoke suite. It exercises the compiled binary
// against api.trakt.tv using the repo-root config.toml + tokens.json, one read
// per command group plus the auth-refresh path. Excluded from the default
// `go test` by the `live` build tag.
//
//	go test -tags=live ./test/...
//
// Mutations are NOT fired here; the destructive paths are covered by the
// account-safe trakt:0 no-op probes in live_probes.sh.
package live

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
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

// assertOK fails unless the envelope reports ok:true.
func assertOK(t *testing.T, args ...string) {
	t.Helper()
	env := run(t, args...)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("%v: ok!=true: %v", args, env["error"])
	}
}

func TestLiveReads(t *testing.T) {
	cases := [][]string{
		{"search", "query", "--type", "movie", "--q", "the matrix", "--limit", "2"},
		{"movie", "get", "--id", "tron-legacy-2010"},
		{"movie", "trending", "--limit", "2"},
		{"show", "get", "--id", "breaking-bad"},
		{"season", "summary", "--show", "breaking-bad"},
		{"episode", "summary", "--show", "breaking-bad", "--season", "1", "--episode", "1"},
		{"calendar", "all-movies", "--start", "2026-06-26", "--days", "3"},
		{"recommend", "movies", "--limit", "2"},
		{"sync", "activities"},
		{"sync", "collection", "get", "--type", "movies", "--limit", "1"},
		{"user", "stats"},
		{"user", "settings"},
		{"user", "watchlist", "--type", "movies", "--limit", "2"},
	}
	for _, c := range cases {
		c := c
		t.Run(c[0]+"/"+c[1], func(t *testing.T) { assertOK(t, c...) })
	}
}

// TestLiveSearchByID exercises the by-id lookup path.
func TestLiveSearchByID(t *testing.T) {
	assertOK(t, "search", "id", "--id-type", "imdb", "--id", "tt0468569")
}
