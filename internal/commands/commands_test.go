package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// TestValidateIDType covers the global --id-type enum check (R3/BUG-3).
func TestValidateIDType(t *testing.T) {
	for _, v := range []string{"", "trakt", "slug", "imdb", "tmdb", "tvdb"} {
		if err := validateIDType(v); err != nil {
			t.Errorf("validateIDType(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"bogus", "TMDB", "id", "trakt "} {
		err := validateIDType(v)
		if err == nil {
			t.Errorf("validateIDType(%q) = nil, want error", v)
			continue
		}
		if err.Code != output.CodeBadConfig {
			t.Errorf("validateIDType(%q) code = %q, want %q", v, err.Code, output.CodeBadConfig)
		}
		if err.Exit != output.ExitUser {
			t.Errorf("validateIDType(%q) exit = %d, want %d", v, err.Exit, output.ExitUser)
		}
		if err.Hint == "" {
			t.Errorf("validateIDType(%q) missing hint", v)
		}
	}
}

// TestRequireLookupID covers the /{id}-endpoint restriction to trakt|slug|imdb
// (R3/BUG-6): tmdb/tvdb must error instead of silently returning a wrong title.
func TestRequireLookupID(t *testing.T) {
	mk := func(idType, id string) *App {
		return &App{Flags: &GlobalFlags{IDType: idType, ID: id}}
	}
	// Supported types pass through.
	for _, ty := range []string{"trakt", "slug", "imdb", ""} {
		a := mk(ty, "155")
		got, err := a.requireLookupID()
		if err != nil {
			t.Errorf("requireLookupID(%q) err = %v, want nil", ty, err)
		}
		if got != "155" {
			t.Errorf("requireLookupID(%q) id = %q, want 155", ty, got)
		}
	}
	// Unsupported external types error with a hint pointing at `search id`.
	for _, ty := range []string{"tmdb", "tvdb"} {
		a := mk(ty, "155")
		_, err := a.requireLookupID()
		if err == nil {
			t.Fatalf("requireLookupID(%q) = nil, want error", ty)
		}
		var cliErr *output.CLIError
		if !asCLIError(err, &cliErr) {
			t.Fatalf("requireLookupID(%q) err type = %T, want *output.CLIError", ty, err)
		}
		if cliErr.Exit != output.ExitUser {
			t.Errorf("requireLookupID(%q) exit = %d, want %d", ty, cliErr.Exit, output.ExitUser)
		}
		if !strings.Contains(cliErr.Hint, "search id") {
			t.Errorf("requireLookupID(%q) hint = %q, want mention of `search id`", ty, cliErr.Hint)
		}
	}
	// Missing id still errors first.
	a := mk("trakt", "")
	if _, err := a.requireLookupID(); err == nil {
		t.Error("requireLookupID with empty id should error")
	}
}

// asCLIError is a tiny errors.As shim to avoid importing errors in the test for
// a single call.
func asCLIError(err error, target **output.CLIError) bool {
	if e, ok := err.(*output.CLIError); ok {
		*target = e
		return true
	}
	return false
}

// TestSummarize covers the --terse human summarizer (R2/BUG-2) across the
// common Trakt object shapes.
func TestSummarize(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"bare movie", `{"title":"Blade Runner","year":1982,"ids":{"slug":"blade-runner-1982"}}`, "Blade Runner (1982)"},
		{"movie no year", `{"title":"Untitled","ids":{}}`, "Untitled"},
		{"watching aggregate", `{"title":"The Sheep Detectives","year":2025,"watcher_count":1302}`, "The Sheep Detectives (2025) · 1302 watching"},
		{"wrapped movie", `{"type":"movie","movie":{"title":"Heat","year":1995}}`, "Heat (1995)"},
		{"episode", `{"season":1,"number":3,"title":"Pilot"}`, "S01E03 Pilot"},
		{"episode with show", `{"show":{"title":"Breaking Bad","year":2008},"episode":{"season":2,"number":5,"title":"Breakage"}}`, "Breaking Bad (2008) - S02E05 Breakage"},
		{"person", `{"name":"Ridley Scott","ids":{}}`, "Ridley Scott"},
		{"list", `{"name":"Best of 2024","item_count":42}`, "Best of 2024 (42 items)"},
		{"single list with owner", `{"name":"All Time Favorite Movies","item_count":37,"ids":{"trakt":1625180},"user":{"username":"justin","name":"Justin"}}`, "All Time Favorite Movies (37 items)"},
		{"bare season number only", `{"number":1,"ids":{"trakt":3962}}`, "Season 1"},
		{"user", `{"username":"alice","name":"Alice"}`, "Alice (@alice)"},
		{"array of movies", `[{"title":"A","year":2000},{"title":"B","year":2001}]`, "A (2000) (+1 more)"},
		{"single-element array", `[{"title":"Solo","year":2018}]`, "Solo (2018)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarize(json.RawMessage(c.json))
			if got != c.want {
				t.Errorf("summarize(%s) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

// TestSummarizeFallback ensures unknown shapes return "" so the writer falls
// back to compact JSON rather than emitting a misleading line.
func TestSummarizeFallback(t *testing.T) {
	for _, j := range []string{``, `{}`, `{"foo":"bar"}`, `[]`, `42`} {
		if got := summarize(json.RawMessage(j)); got != "" {
			t.Errorf("summarize(%q) = %q, want \"\"", j, got)
		}
	}
}
