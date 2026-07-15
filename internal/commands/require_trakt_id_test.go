package commands

import (
	"net/http"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// TestRequireTraktID covers TC-5's helper directly: recommend hide-* and sync
// playback remove interpolate a bare trakt id into the path, so a non-trakt
// --id-type must be rejected instead of silently building a path Trakt 404s
// on. search id is unaffected (it stays on requireID) since --id-type
// legitimately routes it.
func TestRequireTraktID(t *testing.T) {
	mk := func(idType, id string) *App {
		return &App{Flags: &GlobalFlags{IDType: idType, ID: id}}
	}
	// trakt (explicit or default-empty) passes through.
	for _, ty := range []string{"trakt", ""} {
		a := mk(ty, "155")
		got, err := a.requireTraktID()
		if err != nil {
			t.Errorf("requireTraktID(%q) err = %v, want nil", ty, err)
		}
		if got != "155" {
			t.Errorf("requireTraktID(%q) id = %q, want 155", ty, got)
		}
	}
	// Any other id-type errors with a hint pointing at `search id`.
	for _, ty := range []string{"imdb", "slug", "tmdb", "tvdb"} {
		a := mk(ty, "155")
		_, err := a.requireTraktID()
		if err == nil {
			t.Fatalf("requireTraktID(%q) = nil, want error", ty)
		}
		var cliErr *output.CLIError
		if !asCLIError(err, &cliErr) {
			t.Fatalf("requireTraktID(%q) err type = %T, want *output.CLIError", ty, err)
		}
		if cliErr.Code != output.CodeBadConfig {
			t.Errorf("requireTraktID(%q) code = %q, want %q", ty, cliErr.Code, output.CodeBadConfig)
		}
		if !strings.Contains(cliErr.Hint, "search id") {
			t.Errorf("requireTraktID(%q) hint = %q, want mention of `search id`", ty, cliErr.Hint)
		}
	}
	// Missing id still errors first (delegates to requireID).
	a := mk("trakt", "")
	if _, err := a.requireTraktID(); err == nil {
		t.Error("requireTraktID with empty id should error")
	}
}

// TestRecommendHideRejectsNonTraktIDType and TestSyncPlaybackRemoveRejects
// NonTraktIDType cover the swap at the command level: --id-type imdb must be
// rejected before any network call.
func TestRecommendHideRejectsNonTraktIDType(t *testing.T) {
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
	})
	cmd, _, err := root.Find([]string{"recommend", "hide-movie"})
	if err != nil {
		t.Fatalf("could not find `recommend hide-movie`: %v", err)
	}
	runErr := runDirect(t, cmd, []string{"--id", "5", "--id-type", "imdb"})
	var cliErr *output.CLIError
	if !asCLIError(runErr, &cliErr) {
		t.Fatalf("recommend hide-movie --id 5 --id-type imdb err type = %T (%v), want *output.CLIError", runErr, runErr)
	}
	if cliErr.Code != output.CodeBadConfig {
		t.Errorf("recommend hide-movie --id 5 --id-type imdb code = %q, want %q", cliErr.Code, output.CodeBadConfig)
	}
}

func TestRecommendHideDefaultTraktPasses(t *testing.T) {
	called := false
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cmd, _, err := root.Find([]string{"recommend", "hide-movie"})
	if err != nil {
		t.Fatalf("could not find `recommend hide-movie`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--id", "5"}); runErr != nil {
		t.Fatalf("recommend hide-movie --id 5 = %v, want nil", runErr)
	}
	if !called {
		t.Error("recommend hide-movie --id 5 never reached the network; default trakt id-type was wrongly rejected")
	}
}

func TestSyncPlaybackRemoveRejectsNonTraktIDType(t *testing.T) {
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
	})
	cmd, _, err := root.Find([]string{"sync", "playback", "remove"})
	if err != nil {
		t.Fatalf("could not find `sync playback remove`: %v", err)
	}
	runErr := runDirect(t, cmd, []string{"--id", "5", "--id-type", "imdb", "--confirm"})
	var cliErr *output.CLIError
	if !asCLIError(runErr, &cliErr) {
		t.Fatalf("sync playback remove --id 5 --id-type imdb --confirm err type = %T (%v), want *output.CLIError", runErr, runErr)
	}
	if cliErr.Code != output.CodeBadConfig {
		t.Errorf("sync playback remove --id 5 --id-type imdb --confirm code = %q, want %q", cliErr.Code, output.CodeBadConfig)
	}
}

func TestSyncPlaybackRemoveDefaultTraktPasses(t *testing.T) {
	called := false
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	cmd, _, err := root.Find([]string{"sync", "playback", "remove"})
	if err != nil {
		t.Fatalf("could not find `sync playback remove`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--id", "5", "--confirm"}); runErr != nil {
		t.Fatalf("sync playback remove --id 5 --confirm = %v, want nil", runErr)
	}
	if !called {
		t.Error("sync playback remove --id 5 --confirm never reached the network; default trakt id-type was wrongly rejected")
	}
}
