package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// exportServer is the fake Trakt for the export tests: it answers every
// /sync/... path from `bodies` (default: an empty array), always sets the
// pagination headers the client's --all loop reads, and records every request
// so a test can assert what was asked for.
type exportServer struct {
	mu       sync.Mutex
	requests []*url.URL
	bodies   map[string]string // path -> single-page JSON array
	fail     map[string]int    // path -> HTTP status to fail with
	handler  func(w http.ResponseWriter, r *http.Request) bool
}

func newExportServer(t *testing.T, s *exportServer) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.URL)
		s.mu.Unlock()

		if s.handler != nil && s.handler(w, r) {
			return
		}
		if status, ok := s.fail[r.URL.Path]; ok {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":"nope"}`)
			return
		}
		body, ok := s.bodies[r.URL.Path]
		if !ok {
			body = "[]"
		}
		writePage(w, body, 1, 1, countRows(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writePage answers with one page plus the X-Pagination-* headers. Without
// those headers the client's doAll breaks after page one, so every fake page
// must carry them or a pagination test passes for the wrong reason.
func writePage(w http.ResponseWriter, body string, page, pageCount, itemCount int) {
	w.Header().Set("X-Pagination-Page", strconv.Itoa(page))
	w.Header().Set("X-Pagination-Limit", "250")
	w.Header().Set("X-Pagination-Page-Count", strconv.Itoa(pageCount))
	w.Header().Set("X-Pagination-Item-Count", strconv.Itoa(itemCount))
	fmt.Fprint(w, body)
}

func countRows(body string) int {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		return 0
	}
	return len(arr)
}

// runExportRoot drives `sync export` end to end against srv.
func runExportRoot(t *testing.T, srv *httptest.Server, extra ...string) (output.Envelope, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	args := append([]string{"--client-id", "cid", "--access-token", "tok", "--base-url", srv.URL,
		"sync", "export"}, extra...)
	return runRoot(t, args...)
}

func exportData(t *testing.T, env output.Envelope) map[string]interface{} {
	t.Helper()
	m, ok := env.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %T", env.Data)
	}
	return m
}

// TestSyncExportHappyPath: every kind in exportKinds lands in the envelope
// under its own key, each fetch hits its own endpoint, and a run with no
// failure carries neither `errors` nor meta.partial.
func TestSyncExportHappyPath(t *testing.T) {
	fake := &exportServer{bodies: map[string]string{
		"/sync/watched/movies":  `[{"plays":2,"movie":{"title":"Heat","year":1995}}]`,
		"/sync/history/movies":  `[{"id":1,"watched_at":"2026-01-01T00:00:00.000Z"},{"id":2}]`,
		"/sync/ratings/seasons": `[{"rating":9}]`,
	}}
	srv := newExportServer(t, fake)

	env, err := runExportRoot(t, srv)
	if err != nil {
		t.Fatalf("sync export = %v, want success", err)
	}
	if !env.OK {
		t.Fatalf("ok = false (%+v), want true", env.Error)
	}
	if env.Meta != nil && env.Meta.Partial {
		t.Error("meta.partial = true on a clean run, want false")
	}

	data := exportData(t, env)
	for _, k := range exportKinds {
		v, ok := data[k.Name]
		if !ok {
			t.Errorf("data is missing kind %q", k.Name)
			continue
		}
		if _, isArr := v.([]interface{}); !isArr {
			t.Errorf("data[%q] is %T, want an array", k.Name, v)
		}
	}
	if _, ok := data[exportErrorsKey]; ok {
		t.Errorf("data.errors present on a clean run: %v", data[exportErrorsKey])
	}
	if got := len(data[exportKinds[0].Name].([]interface{})); got != 1 {
		t.Errorf("watched_movies rows = %d, want 1", got)
	}
	if got := len(data["history_movies"].([]interface{})); got != 2 {
		t.Errorf("history_movies rows = %d, want 2", got)
	}

	// One request per kind, on the kind's own path.
	wantPaths := map[string]bool{}
	for _, k := range exportKinds {
		wantPaths[k.Path] = true
	}
	gotPaths := map[string]bool{}
	for _, u := range fake.requests {
		gotPaths[u.Path] = true
	}
	for p := range wantPaths {
		if !gotPaths[p] {
			t.Errorf("export never requested %s", p)
		}
	}
	for p := range gotPaths {
		if !wantPaths[p] {
			t.Errorf("export requested an unexpected path %s", p)
		}
	}

	// stats carries the per-kind counts the --llm/terse summary reports.
	stats, ok := data[exportStatsKey].(map[string]interface{})
	if !ok {
		t.Fatalf("data.stats is %T, want an object", data[exportStatsKey])
	}
	kinds, ok := stats["kinds"].([]interface{})
	if !ok || len(kinds) != len(exportKinds) {
		t.Fatalf("stats.kinds = %v entries, want %d", len(kinds), len(exportKinds))
	}
	if rows, _ := stats["rows"].(float64); rows != 4 {
		t.Errorf("stats.rows = %v, want 4", rows)
	}
}

// TestSyncExportPageSizeAndCapLift: every export fetch asks for 250 rows a
// page (Trakt's server-side maximum) and no fetch trips the 100-page cap,
// because export always sets --really-all. --page is ignored: an export starts
// at page 1 by definition.
func TestSyncExportPageSizeAndCapLift(t *testing.T) {
	fake := &exportServer{}
	srv := newExportServer(t, fake)

	if _, err := runExportRoot(t, srv, "--page", "7"); err != nil {
		t.Fatalf("sync export --page 7 = %v, want success", err)
	}
	for _, u := range fake.requests {
		if got := u.Query().Get("limit"); got != strconv.Itoa(exportPageLimit) {
			t.Errorf("%s limit = %q, want %d", u.Path, got, exportPageLimit)
		}
		if got := u.Query().Get("page"); got != "1" {
			t.Errorf("%s page = %q, want 1 (--page must not move an export's start)", u.Path, got)
		}
	}
}

// TestSyncExportExtendedScope: --extended full reaches the watched and
// collection fetches (B9's genre fold needs it) and NOTHING else, so a 17k-row
// history is not inflated with metadata no consumer asked for.
func TestSyncExportExtendedScope(t *testing.T) {
	fake := &exportServer{}
	srv := newExportServer(t, fake)

	if _, err := runExportRoot(t, srv, "--extended", "full"); err != nil {
		t.Fatalf("sync export --extended full = %v, want success", err)
	}
	want := map[string]bool{}
	for _, k := range exportKinds {
		want[k.Path] = k.Extended
	}
	for _, u := range fake.requests {
		got := u.Query().Get("extended") == "full"
		if got != want[u.Path] {
			t.Errorf("%s extended=full = %v, want %v", u.Path, got, want[u.Path])
		}
	}
}

// TestSyncExportDirMode: --dir writes one file per kind, each valid JSON
// holding that kind's rows, and stdout reports the files instead of the
// payload.
func TestSyncExportDirMode(t *testing.T) {
	fake := &exportServer{bodies: map[string]string{
		"/sync/watched/movies": `[{"plays":2,"movie":{"title":"Heat","year":1995}}]`,
	}}
	srv := newExportServer(t, fake)
	dir := filepath.Join(t.TempDir(), "nested", "export") // must be created for us

	env, err := runExportRoot(t, srv, "--dir", dir)
	if err != nil {
		t.Fatalf("sync export --dir = %v, want success", err)
	}
	data := exportData(t, env)

	// The payload is on disk, not on stdout.
	for _, k := range exportKinds {
		if _, ok := data[k.Name]; ok {
			t.Errorf("data[%q] present under --dir; the rows belong in the file", k.Name)
		}
	}
	if got, _ := data["dir"].(string); got != dir {
		t.Errorf("data.dir = %q, want %q", got, dir)
	}
	files, ok := data["files"].([]interface{})
	if !ok || len(files) != len(exportKinds) {
		t.Fatalf("data.files = %d entries, want %d", len(files), len(exportKinds))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading export dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	var wantNames []string
	for _, k := range exportKinds {
		wantNames = append(wantNames, k.Name+".json")
	}
	sort.Strings(wantNames)
	if strings.Join(names, ",") != strings.Join(wantNames, ",") {
		t.Errorf("files on disk = %v, want %v", names, wantNames)
	}

	// Every file is valid JSON, and the one non-empty kind holds its rows.
	for _, name := range names {
		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			t.Fatalf("reading %s: %v", name, rerr)
		}
		var arr []json.RawMessage
		if uerr := json.Unmarshal(raw, &arr); uerr != nil {
			t.Errorf("%s is not a valid JSON array: %v", name, uerr)
		}
		if name == "watched_movies.json" {
			if len(arr) != 1 {
				t.Errorf("watched_movies.json rows = %d, want 1", len(arr))
			}
			if !strings.Contains(string(raw), `"Heat"`) {
				t.Errorf("watched_movies.json lost its row: %s", raw)
			}
		}
	}

	// A --dir path that exists as a file is a usage error, caught before any
	// fetch runs.
	filePath := filepath.Join(t.TempDir(), "not-a-dir")
	if werr := os.WriteFile(filePath, []byte("x"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	env, err = runExportRoot(t, srv, "--dir", filePath)
	if err == nil {
		t.Fatal("sync export --dir <file> = nil, want a usage error")
	}
	if cerr := classifyError(err); cerr.Code != output.CodeBadRequest {
		t.Errorf("--dir <file> code = %q, want %q", cerr.Code, output.CodeBadRequest)
	}
}

// TestSyncExportPartialFailure: one kind failing must not lose the others.
// The envelope carries errors:[{kind,code,message}] and meta.partial, and the
// exit stays 0 — the same contract `sync`'s batch mutations already use for a
// partial result.
func TestSyncExportPartialFailure(t *testing.T) {
	fake := &exportServer{
		bodies: map[string]string{"/sync/watched/movies": `[{"plays":1}]`},
		fail: map[string]int{
			"/sync/history/episodes": http.StatusInternalServerError,
			"/sync/ratings/shows":    http.StatusNotFound,
		},
	}
	srv := newExportServer(t, fake)

	env, err := runExportRoot(t, srv)
	if err != nil {
		t.Fatalf("sync export with a failing kind = %v, want success (exit 0)", err)
	}
	if !env.OK {
		t.Fatalf("ok = false (%+v), want true", env.Error)
	}
	if env.Meta == nil || !env.Meta.Partial {
		t.Error("meta.partial = false, want true when a kind failed")
	}

	data := exportData(t, env)
	if _, ok := data["watched_movies"]; !ok {
		t.Error("a surviving kind was lost when another failed")
	}
	for _, gone := range []string{"history_episodes", "ratings_shows"} {
		if _, ok := data[gone]; ok {
			t.Errorf("data[%q] present, want the failed kind absent", gone)
		}
	}

	raw, _ := json.Marshal(data[exportErrorsKey])
	var failures []exportFailure
	if uerr := json.Unmarshal(raw, &failures); uerr != nil {
		t.Fatalf("data.errors is not [{kind,code,message}]: %v (%s)", uerr, raw)
	}
	if len(failures) != 2 {
		t.Fatalf("data.errors = %d entries, want 2: %s", len(failures), raw)
	}
	byKind := map[string]exportFailure{}
	for _, f := range failures {
		byKind[f.Kind] = f
	}
	if got := byKind["history_episodes"].Code; got != output.CodeTraktServer {
		t.Errorf("history_episodes code = %q, want %q", got, output.CodeTraktServer)
	}
	if got := byKind["ratings_shows"].Code; got != output.CodeTraktNotFound {
		t.Errorf("ratings_shows code = %q, want %q", got, output.CodeTraktNotFound)
	}
	if byKind["ratings_shows"].Message == "" {
		t.Error("a failure entry carries no message")
	}
}

// TestSyncExportTotalFailure: when NO kind came back there is nothing to call
// a partial success, so the first kind's own typed error propagates — an
// export that could not authenticate must not exit 0 with an empty envelope.
func TestSyncExportTotalFailure(t *testing.T) {
	fake := &exportServer{handler: func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusForbidden)
		return true
	}}
	srv := newExportServer(t, fake)

	_, err := runExportRoot(t, srv)
	if err == nil {
		t.Fatal("sync export with every kind failing = nil, want an error")
	}
	cerr := classifyError(err)
	if cerr.Code != output.CodeTraktVIPOnly { // 403's mapping, passed through untouched
		t.Errorf("code = %q, want the underlying Trakt code %q", cerr.Code, output.CodeTraktVIPOnly)
	}
	if cerr.Exit != output.ExitTrakt {
		t.Errorf("exit = %d, want %d (the underlying class, not an invented one)", cerr.Exit, output.ExitTrakt)
	}
}

// TestSyncExportPaginatesHistory: history is the big one — it must be walked
// to the last page and merged, not truncated at page one.
func TestSyncExportPaginatesHistory(t *testing.T) {
	const (
		historyPath  = "/sync/history/episodes"
		pages        = 3
		rowsPerPage  = 2
		totalHistory = pages * rowsPerPage
	)
	var seenPages []int
	fake := &exportServer{handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != historyPath {
			return false
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		seenPages = append(seenPages, page)
		rows := make([]string, 0, rowsPerPage)
		for i := 0; i < rowsPerPage; i++ {
			rows = append(rows, fmt.Sprintf(`{"id":%d,"page":%d}`, (page-1)*rowsPerPage+i, page))
		}
		writePage(w, "["+strings.Join(rows, ",")+"]", page, pages, totalHistory)
		return true
	}}
	srv := newExportServer(t, fake)

	env, err := runExportRoot(t, srv)
	if err != nil {
		t.Fatalf("sync export = %v, want success", err)
	}
	data := exportData(t, env)

	rows, ok := data["history_episodes"].([]interface{})
	if !ok {
		t.Fatalf("history_episodes is %T, want an array", data["history_episodes"])
	}
	if len(rows) != totalHistory {
		t.Fatalf("history_episodes rows = %d, want %d (pagination stopped early)", len(rows), totalHistory)
	}
	if fmt.Sprint(seenPages) != "[1 2 3]" {
		t.Errorf("history pages fetched = %v, want [1 2 3]", seenPages)
	}
	// The last page's rows are present, not just the first page's.
	last, _ := rows[totalHistory-1].(map[string]interface{})
	if got, _ := last["page"].(float64); int(got) != pages {
		t.Errorf("last row came from page %v, want %d", last["page"], pages)
	}

	stats, _ := data[exportStatsKey].(map[string]interface{})
	kinds, _ := stats["kinds"].([]interface{})
	for _, k := range kinds {
		entry, _ := k.(map[string]interface{})
		if entry["kind"] != "history_episodes" {
			continue
		}
		if got, _ := entry["pages"].(float64); int(got) != pages {
			t.Errorf("stats page count for history = %v, want %d", entry["pages"], pages)
		}
		if got, _ := entry["count"].(float64); int(got) != totalHistory {
			t.Errorf("stats row count for history = %v, want %d", entry["count"], totalHistory)
		}
	}
}

// TestSyncExportInCommandTree covers the ground rule that the new command is
// discoverable: `traktctl commands` (and `--llm`) must list `sync export`.
func TestSyncExportInCommandTree(t *testing.T) {
	root, _ := NewRoot()
	tree := buildCommandTree(root)
	for _, group := range tree.Subcommands {
		if group.Name != "sync" {
			continue
		}
		for _, verb := range group.Subcommands {
			if verb.Name == "export" {
				if verb.Summary == "" {
					t.Error("`sync export` has no summary in the command tree")
				}
				return
			}
		}
		t.Fatalf("`sync export` missing from the sync group: %+v", group.Subcommands)
	}
	t.Fatal("sync group missing from the command tree")
}

// TestSyncExportTerseStdout drives the real --terse write path end to end
// (runRoot decodes stdout as JSON, so it cannot see this): one line per kind
// on stdout, and no JSON envelope.
func TestSyncExportTerseStdout(t *testing.T) {
	fake := &exportServer{bodies: map[string]string{
		"/sync/watched/movies": `[{"plays":1},{"plays":2}]`,
	}}
	srv := newExportServer(t, fake)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")

	root, app := NewRoot()
	var out bytes.Buffer
	app.Out = output.New(&out, io.Discard, output.FormatJSON) // --terse resets the format in build()
	root.SetArgs([]string{"--client-id", "cid", "--access-token", "tok", "--base-url", srv.URL,
		"--terse", "sync", "export"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatalf("sync export --terse = %v, want success", err)
	}

	got := strings.TrimRight(out.String(), "\n")
	if strings.HasPrefix(strings.TrimSpace(got), "{") {
		t.Fatalf("--terse emitted an envelope, not a summary:\n%s", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != len(exportKinds)+1 {
		t.Fatalf("--terse = %d lines, want %d (one per kind plus the total):\n%s",
			len(lines), len(exportKinds)+1, got)
	}
	if lines[0] != "watched_movies: 2" {
		t.Errorf("first line = %q, want %q", lines[0], "watched_movies: 2")
	}
	if !strings.Contains(lines[len(lines)-1], "rows") {
		t.Errorf("last line = %q, want the run total", lines[len(lines)-1])
	}
}

// TestSyncExportTerseSummary covers the one-line-per-kind summary: counts for
// what came back, a named line for what did not.
func TestSyncExportTerseSummary(t *testing.T) {
	stats := exportStats{
		ElapsedMS: 1500,
		Kinds: []exportKindStat{
			{Kind: "watched_movies", Count: 2585, Pages: 11},
			{Kind: "history_episodes", Count: 17079, Pages: 69},
		},
		Pages: 80,
		Rows:  19664,
	}
	got := exportTerse(stats, []exportFailure{{Kind: "ratings_shows", Code: output.CodeTraktRateLimited}}, "")
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("terse summary = %d lines, want 4 (one per kind, one per failure, one total):\n%s", len(lines), got)
	}
	if lines[0] != "watched_movies: 2585" {
		t.Errorf("line 1 = %q, want %q", lines[0], "watched_movies: 2585")
	}
	if !strings.Contains(lines[2], "ratings_shows: FAILED") {
		t.Errorf("failure line = %q, want it to name the kind and that it failed", lines[2])
	}
	if !strings.Contains(lines[3], "2 of "+strconv.Itoa(len(exportKinds))+" kinds") {
		t.Errorf("total line = %q, want the kinds fetched over the kinds attempted", lines[3])
	}
}
