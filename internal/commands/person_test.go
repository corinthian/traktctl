package commands

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestPersonCommands covers B1: the four id-scoped GETs added by the `person`
// group (get, movies, shows, lists), table-driven against the fake-transport
// harness (newTestServerRoot/runDirect, from id_guard_test.go). Each verb must
// hit the right path and pass the response through untouched.
func TestPersonCommands(t *testing.T) {
	tests := []struct {
		verb     string
		wantPath string
		body     string
	}{
		{"get", "/people/bryan-cranston", `{"name":"Bryan Cranston","ids":{"slug":"bryan-cranston"}}`},
		{"movies", "/people/bryan-cranston/movies",
			`{"cast":[{"character":"Walt","movie":{"title":"Argo","year":2012}}],"crew":{}}`},
		{"shows", "/people/bryan-cranston/shows",
			`{"cast":[{"character":"Walter White","show":{"title":"Breaking Bad","year":2008}}],"crew":{}}`},
		{"lists", "/people/bryan-cranston/lists", `[{"name":"Best Actors","item_count":10}]`},
	}
	for _, tc := range tests {
		t.Run(tc.verb, func(t *testing.T) {
			var gotPath string
			root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Write([]byte(tc.body))
			})
			cmd, _, err := root.Find([]string{"person", tc.verb})
			if err != nil {
				t.Fatalf("could not find `person %s`: %v", tc.verb, err)
			}
			if runErr := runDirect(t, cmd, []string{"--id", "bryan-cranston", "--id-type", "slug"}); runErr != nil {
				t.Fatalf("person %s = %v, want nil", tc.verb, runErr)
			}
			if gotPath != tc.wantPath {
				t.Errorf("person %s hit %q, want %q", tc.verb, gotPath, tc.wantPath)
			}
		})
	}
}

// TestPersonExtendedPassthrough covers --extended full passing through to the
// query string unchanged, same as every other getByID command.
func TestPersonExtendedPassthrough(t *testing.T) {
	var gotQuery string
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"name":"Bryan Cranston"}`))
	})
	cmd, _, err := root.Find([]string{"person", "get"})
	if err != nil {
		t.Fatalf("could not find `person get`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--id", "bryan-cranston", "--id-type", "slug", "--extended", "full"}); runErr != nil {
		t.Fatalf("person get --extended full = %v, want nil", runErr)
	}
	if !strings.Contains(gotQuery, "extended=full") {
		t.Errorf("person get query = %q, want it to contain extended=full", gotQuery)
	}
}

// TestPersonRequiresID covers the missing-id error path shared with every
// other getByID command (requireLookupID fires before any network call).
func TestPersonRequiresID(t *testing.T) {
	for _, verb := range []string{"get", "movies", "shows", "lists"} {
		t.Run(verb, func(t *testing.T) {
			root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
			})
			cmd, _, err := root.Find([]string{"person", verb})
			if err != nil {
				t.Fatalf("could not find `person %s`: %v", verb, err)
			}
			if runErr := runDirect(t, cmd, nil); runErr == nil {
				t.Fatalf("person %s with no --id = nil, want error", verb)
			}
		})
	}
}

// TestPersonRejectsTmdb covers the requireLookupID restriction shared with
// every other single-item GET: tmdb/tvdb are not served on /{id} endpoints,
// only via `search id`.
func TestPersonRejectsTmdb(t *testing.T) {
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call: %s %s", r.Method, r.URL.Path)
	})
	cmd, _, err := root.Find([]string{"person", "get"})
	if err != nil {
		t.Fatalf("could not find `person get`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--id", "17419", "--id-type", "tmdb"}); runErr == nil {
		t.Fatal("person get --id-type tmdb = nil, want error")
	}
}

// TestPersonInCommandTree covers B1's `commands` requirement: `traktctl
// commands` (the tree `--llm` consumers read) must list the person group and
// its four verbs.
func TestPersonInCommandTree(t *testing.T) {
	root, _ := NewRoot()
	tree := buildCommandTree(root)
	var person *commandNode
	for i := range tree.Subcommands {
		if tree.Subcommands[i].Name == "person" {
			person = &tree.Subcommands[i]
			break
		}
	}
	if person == nil {
		t.Fatal("`person` group missing from the commands tree")
	}
	want := map[string]bool{"get": false, "movies": false, "shows": false, "lists": false}
	for _, c := range person.Subcommands {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("`person %s` missing from the commands tree", name)
		}
	}
}

// TestSummarizeFilmography covers B1's --terse summariser requirement: a
// person row summarises to its name (already covered by TestSummarize's
// "person" case), and a filmography response (the cast/crew map returned by
// /people/{id}/movies and /people/{id}/shows) must not collapse to an empty
// string the way an unrecognized shape normally would.
func TestSummarizeFilmography(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{
			"cast and crew",
			`{"cast":[{"character":"Walt","movie":{"title":"Argo"}},{"character":"X","movie":{"title":"Y"}}],
			  "crew":{"production":[{"job":"Producer","movie":{"title":"Z"}}]}}`,
			"3 credits (2 cast, 1 crew)",
		},
		{
			"cast only, singular",
			`{"cast":[{"character":"Walt","movie":{"title":"Argo"}}],"crew":{}}`,
			"1 cast credit",
		},
		{
			"crew only, multiple departments",
			`{"cast":[],"crew":{"production":[{"job":"Producer"}],"writing":[{"job":"Writer"},{"job":"Story"}]}}`,
			"3 crew credits",
		},
		{
			"empty filmography",
			`{"cast":[],"crew":{}}`,
			"no credits",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarize(json.RawMessage(c.json))
			if got != c.want {
				t.Errorf("summarize(%s) = %q, want %q", c.name, got, c.want)
			}
			if got == "" {
				t.Errorf("summarize(%s) collapsed to empty string", c.name)
			}
		})
	}
}
