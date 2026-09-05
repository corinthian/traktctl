package commands

import (
	"io"
	"net/http"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// TestLeafCommandsRejectPositionals covers the widest of the accepted-and-
// ignored defects: nothing in this CLI reads a positional, but cobra's default
// let every leaf swallow one, so `traktctl movie trending garbage` exited 0
// having silently dropped the word. Args validation runs before
// PersistentPreRunE, so none of this resolves config or issues a request.
func TestLeafCommandsRejectPositionals(t *testing.T) {
	for _, path := range [][]string{
		{"movie", "trending"},
		{"sync", "history", "get"},
		{"user", "watchlist"},
		{"search", "query"},
		{"commands"},
	} {
		name := ""
		for _, p := range path {
			name += p + " "
		}
		t.Run(name+"garbage", func(t *testing.T) {
			root, _ := NewRoot()
			root.SetArgs(append(append([]string{}, path...), "garbage"))
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			assertUsageEnvelope(t, name+"garbage", root.Execute(), true)
		})
	}
}

// TestGroupCommandsRejectUnknownSubcommand: the same rule one level up. A
// parent already errored on a missing subcommand; it must also error on a word
// that is not one of its subcommands rather than treating it as a positional.
func TestGroupCommandsRejectUnknownSubcommand(t *testing.T) {
	root, _ := NewRoot()
	root.SetArgs([]string{"movie", "not-a-subcommand"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	assertUsageEnvelope(t, "movie not-a-subcommand", root.Execute(), true)
}

// TestSyncIDRequiresType: Trakt only serves the item id as a segment after a
// type segment, so `--id 12345` alone was parsed, dropped, and answered with
// the full unfiltered history.
func TestSyncIDRequiresType(t *testing.T) {
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
	})
	cmd, _, err := root.Find([]string{"sync", "history", "get"})
	if err != nil {
		t.Fatalf("could not find `sync history get`: %v", err)
	}
	assertUsageEnvelope(t, "sync history get --id without --type",
		runDirect(t, cmd, []string{"--id", "12345"}), true)
}

// TestSortFlagsRequireType and TestSortFlagsRequireBoth cover the sort pair on
// every command that builds the /{type}/{sort_by}/{sort_how} tail. Trakt has no
// `all` sort path, so unlike --rating there is nothing to default to: a pair
// that cannot be placed is a usage error, not something to drop in silence.
func TestSortFlagsRequireType(t *testing.T) {
	for _, tc := range []struct {
		name string
		path []string
		args []string
	}{
		{"sync watchlist get", []string{"sync", "watchlist", "get"},
			[]string{"--sort-by", "rank", "--sort-how", "asc"}},
		{"user watchlist", []string{"user", "watchlist"},
			[]string{"--sort-by", "rank", "--sort-how", "asc"}},
		{"user list-items", []string{"user", "list-items"},
			[]string{"--list-id", "X", "--sort-by", "rank", "--sort-how", "asc"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
			})
			cmd, _, err := root.Find(tc.path)
			if err != nil {
				t.Fatalf("could not find %v: %v", tc.path, err)
			}
			assertUsageEnvelope(t, tc.name+" sort without --type", runDirect(t, cmd, tc.args), true)
		})
	}
}

func TestSortFlagsRequireBoth(t *testing.T) {
	for _, args := range [][]string{
		{"--type", "movies", "--sort-by", "rank"},
		{"--type", "movies", "--sort-how", "asc"},
	} {
		root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
			t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
		})
		cmd, _, err := root.Find([]string{"sync", "watchlist", "get"})
		if err != nil {
			t.Fatalf("could not find `sync watchlist get`: %v", err)
		}
		assertUsageEnvelope(t, "half a sort pair", runDirect(t, cmd, args), true)
	}
}

// TestSyncRatingsDefaultsTypeToAll: `sync ratings get` and `user ratings` used
// to disagree — one defaulted the type segment so --rating was reachable, the
// other dropped it. They now share ratingsSuffix.
func TestSyncRatingsDefaultsTypeToAll(t *testing.T) {
	for _, tc := range []struct {
		name, typ, rating, want string
	}{
		{"neither", "", "", ""},
		{"rating only defaults the type", "", "8", "/all/8"},
		{"type only", "movies", "", "/movies"},
		{"both", "shows", "10", "/shows/10"},
		{"comma set", "", "8,9", "/all/8,9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cerr := ratingsSuffix(tc.typ, tc.rating)
			if cerr != nil {
				t.Fatalf("ratingsSuffix(%q, %q) err = %v, want nil", tc.typ, tc.rating, cerr)
			}
			if got != tc.want {
				t.Errorf("ratingsSuffix(%q, %q) = %q, want %q", tc.typ, tc.rating, got, tc.want)
			}
		})
	}
}

// TestSyncRatingsRejectsOutOfRange: Trakt answers /ratings/all/11 with HTTP 200
// and an empty array, which a caller cannot tell from "you rated nothing 11",
// so the value is validated locally on both ratings paths.
func TestSyncRatingsRejectsOutOfRange(t *testing.T) {
	for _, bad := range []string{"0", "11", "x", "8,99", "-1"} {
		if _, cerr := ratingsSuffix("", bad); cerr == nil {
			t.Errorf("ratingsSuffix(\"\", %q) = nil error, want a usage error", bad)
		} else if cerr.Code != output.CodeBadRequest {
			t.Errorf("ratingsSuffix(\"\", %q) code = %q, want %q", bad, cerr.Code, output.CodeBadRequest)
		}
	}
}
