package commands

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// TestListTrendingAndPopular covers the two no-id discovery reads: path,
// pagination flags, and the trending/popular wrapper shape
// ({like_count,comment_count,list}) rendering as "Name (N items)" under
// --terse via the existing summarize() unwrap loop.
func TestListTrendingAndPopular(t *testing.T) {
	body := `[{"like_count":5,"comment_count":812,"list":{"name":"Sci-Fi Essentials","item_count":42}}]`

	tests := []struct {
		name     string
		group    []string
		wantPath string
	}{
		{"trending", []string{"list", "trending"}, "/lists/trending"},
		{"popular", []string{"list", "popular"}, "/lists/popular"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotQuery url.Values
			root, app := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotQuery = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			})
			cmd, _, err := root.Find(tc.group)
			if err != nil {
				t.Fatalf("could not find `%v`: %v", tc.group, err)
			}
			var out bytes.Buffer
			app.Out = output.New(&out, &out, output.FormatTerse)
			if runErr := runDirect(t, cmd, []string{"--limit", "3", "--page", "1"}); runErr != nil {
				t.Fatalf("%v = %v, want nil", tc.group, runErr)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotQuery.Get("limit") != "3" || gotQuery.Get("page") != "1" {
				t.Errorf("pagination query = %v, want limit=3 page=1", gotQuery)
			}
			if got := out.String(); got != "Sci-Fi Essentials (42 items)\n" {
				t.Errorf("--terse output = %q, want %q", got, "Sci-Fi Essentials (42 items)\n")
			}
		})
	}
}

// TestListGetItemsLikes covers the three --list-id-scoped reads: path
// construction (including optional --type on items) and that --list-id is
// required.
func TestListGetItemsLikes(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantPath string
	}{
		{"get", []string{"get", "--list-id", "1225"}, "/lists/1225"},
		{"items no type", []string{"items", "--list-id", "1225"}, "/lists/1225/items"},
		{"items with type", []string{"items", "--list-id", "1225", "--type", "movie"}, "/lists/1225/items/movie"},
		{"likes", []string{"likes", "--list-id", "1225"}, "/lists/1225/likes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"name":"Sci-Fi Essentials","item_count":42}`))
			})
			cmd, _, err := root.Find([]string{"list", tc.args[0]})
			if err != nil {
				t.Fatalf("could not find `list %s`: %v", tc.args[0], err)
			}
			if runErr := runDirect(t, cmd, tc.args[1:]); runErr != nil {
				t.Fatalf("list %v = %v, want nil", tc.args, runErr)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// TestListMissingListID covers the usage-error half: get/items/likes must all
// refuse a missing --list-id as BAD_REQUEST, before any network call.
func TestListMissingListID(t *testing.T) {
	for _, use := range []string{"get", "items", "likes"} {
		t.Run(use, func(t *testing.T) {
			root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("unexpected network call for `list %s` with no --list-id: %s %s", use, r.Method, r.URL.Path)
			})
			cmd, _, err := root.Find([]string{"list", use})
			if err != nil {
				t.Fatalf("could not find `list %s`: %v", use, err)
			}
			runErr := runDirect(t, cmd, nil)
			var cliErr *output.CLIError
			if !asCLIError(runErr, &cliErr) {
				t.Fatalf("list %s (no --list-id) err type = %T (%v), want *output.CLIError", use, runErr, runErr)
			}
			if cliErr.Code != output.CodeBadRequest {
				t.Errorf("list %s (no --list-id) code = %q, want %q", use, cliErr.Code, output.CodeBadRequest)
			}
		})
	}
}

// TestListItemsPagination confirms the global pagination flags reach
// `list items` and `list likes` the same way they reach trending/popular
// (all five B2 commands share baseOpts()).
func TestListItemsPagination(t *testing.T) {
	var gotQuery url.Values
	root, _ := newTestServerRoot(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	})
	cmd, _, err := root.Find([]string{"list", "items"})
	if err != nil {
		t.Fatalf("could not find `list items`: %v", err)
	}
	if runErr := runDirect(t, cmd, []string{"--list-id", "1225", "--limit", "10", "--page", "2"}); runErr != nil {
		t.Fatalf("list items = %v, want nil", runErr)
	}
	if gotQuery.Get("limit") != "10" || gotQuery.Get("page") != "2" {
		t.Errorf("pagination query = %v, want limit=10 page=2", gotQuery)
	}
}

// TestListGroupInCommandTree covers the `commands` tree assertion: the new
// `list` group and its five verbs must be discoverable via
// `traktctl commands --llm`.
func TestListGroupInCommandTree(t *testing.T) {
	root, _ := NewRoot()
	tree := buildCommandTree(root)

	var listNode *commandNode
	for i := range tree.Subcommands {
		if tree.Subcommands[i].Name == "list" {
			listNode = &tree.Subcommands[i]
			break
		}
	}
	if listNode == nil {
		t.Fatal("`list` group not found in command tree")
	}
	want := map[string]bool{"trending": false, "popular": false, "get": false, "items": false, "likes": false}
	for _, c := range listNode.Subcommands {
		if _, ok := want[c.Name]; ok {
			want[c.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("`list %s` not found under the `list` group in the command tree", name)
		}
	}
}
