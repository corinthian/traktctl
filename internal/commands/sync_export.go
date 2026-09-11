package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/corinthian/traktctl/internal/atomicfile"
	"github.com/corinthian/traktctl/internal/client"
	"github.com/corinthian/traktctl/internal/output"
	"github.com/corinthian/traktctl/internal/rawjson"
	"github.com/spf13/cobra"
)

// exportPageLimit is the per-page size every export fetch asks for. Trakt caps
// the page size at 250 server-side (verified live: --limit 1000 comes back
// with X-Pagination-Limit: 250), so 250 is the fewest round trips the API
// allows — 17,079 history rows are 69 pages at 250 against 171 at the client's
// default 100. A caller's explicit --limit still wins.
const exportPageLimit = 250

// exportKind is one slice of the account: an envelope key, the Trakt path that
// fills it, and whether --extended passes through to it.
type exportKind struct {
	Name     string
	Path     string
	Extended bool
}

// exportKinds is the export set: one entry per DISTINCT record set Trakt
// serves. Three types Trakt does serve are deliberately absent, because each
// returns rows another entry already carries (counts verified live 2026-09-11):
//
//   - /sync/history/shows and /sync/history/seasons return the same episode
//     plays as /sync/history/episodes — identical item_count (17,079) and an
//     identical first row. Fetching them would triple the largest pull in the
//     export for no new data.
//   - /sync/collection/episodes flattens what /sync/collection/shows already
//     nests (its rows carry seasons[].episodes[]).
//   - /sync/collection/seasons is not a route at all: Trakt answers it 400.
//
// /sync/watched/shows is NOT in that category: its rows carry no seasons
// array, so watched_episodes is the only source of per-episode play counts.
//
// Extended is set only on watched and collection. Those are the two kinds B9's
// genre fold reads, and they are bounded (a few thousand rows); pushing
// extended=full through 17,079 history rows or 2,532 ratings inflates the
// export with metadata nothing asked for.
var exportKinds = []exportKind{
	{"watched_movies", "/sync/watched/movies", true},
	{"watched_shows", "/sync/watched/shows", true},
	{"watched_episodes", "/sync/watched/episodes", true},
	{"collection_movies", "/sync/collection/movies", true},
	{"collection_shows", "/sync/collection/shows", true},
	{"ratings_movies", "/sync/ratings/movies", false},
	{"ratings_shows", "/sync/ratings/shows", false},
	{"ratings_seasons", "/sync/ratings/seasons", false},
	{"ratings_episodes", "/sync/ratings/episodes", false},
	{"watchlist_movies", "/sync/watchlist/movies", false},
	{"watchlist_shows", "/sync/watchlist/shows", false},
	{"watchlist_seasons", "/sync/watchlist/seasons", false},
	{"watchlist_episodes", "/sync/watchlist/episodes", false},
	{"favorites_movies", "/sync/favorites/movies", false},
	{"favorites_shows", "/sync/favorites/shows", false},
	{"history_movies", "/sync/history/movies", false},
	{"history_episodes", "/sync/history/episodes", false},
}

// reservedExportKeys are the two data keys that are not a kind. No Trakt kind
// is named either, so a consumer can iterate the object and skip these.
const (
	exportErrorsKey = "errors"
	exportStatsKey  = "stats"
)

// syncExport builds `sync export`: every personal-data read in exportKinds,
// with --all implied and the page cap lifted.
func (a *App) syncExport() *cobra.Command {
	var dir string
	c := &cobra.Command{
		Use:   "export",
		Short: "Export the whole account (watched, collection, ratings, watchlist, favorites, history)",
		Annotations: map[string]string{
			"examples": "traktctl sync export\n" +
				"traktctl sync export --dir ./trakt-backup\n" +
				"traktctl sync export --extended full --dir ./trakt-backup\n" +
				"traktctl sync export --terse",
			"output_schema": "JSON envelope. Without --dir, data holds one key per kind " +
				"(watched_movies, watched_shows, watched_episodes, collection_movies, " +
				"collection_shows, ratings_*, watchlist_*, favorites_*, history_movies, " +
				"history_episodes), each an array of Trakt rows. With --dir, data holds " +
				"{dir, files:[{kind,file,bytes}]} and the arrays go to <dir>/<kind>.json. " +
				"Both modes add stats:{elapsed_ms,pages,extended,kinds:[{kind,endpoint,count,pages,duration_ms}]} " +
				"and, when a kind failed, errors:[{kind,code,message}] with meta.partial true.",
		},
		RunE: func(cmd *cobra.Command, args []string) error { return a.runExport(dir) },
	}
	c.Flags().StringVar(&dir, "dir", "",
		"write one JSON file per kind into this directory instead of one envelope on stdout")
	return c
}

// exportOpts shapes one kind's fetch: always --all, always past the 100-page
// cap (history alone is 69 pages at 250/page and grows), and --extended only
// where the kind opts in. --page is ignored on purpose: an export starts at
// page 1 by definition.
func (a *App) exportOpts(k exportKind) client.Options {
	opts := a.baseOpts(true)
	if !k.Extended {
		opts.Extended = ""
	}
	opts.All = true
	opts.ReallyAll = true
	opts.Page = 0
	if opts.Limit <= 0 {
		opts.Limit = exportPageLimit
	}
	return opts
}

// exportFailure is one entry of the envelope's `errors` array.
type exportFailure struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// exportKindStat is one entry of `stats.kinds`.
type exportKindStat struct {
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
	Count      int    `json:"count"`
	Pages      int    `json:"pages"`
	DurationMS int64  `json:"duration_ms"`
	File       string `json:"file,omitempty"`
	Bytes      int    `json:"bytes,omitempty"`
}

// exportStats is the `stats` key: what was fetched and how expensive it was.
type exportStats struct {
	ElapsedMS int64            `json:"elapsed_ms"`
	Kinds     []exportKindStat `json:"kinds"`
	Pages     int              `json:"pages"`
	Rows      int              `json:"rows"`
	Extended  string           `json:"extended,omitempty"`
}

// runExport fetches every kind, then emits one envelope.
//
// Partial failure is the whole point of the loop: a kind that fails is
// recorded and the rest keep going. The verdict then follows `sync`'s existing
// batch contract exactly (emitMutation's partial branch) — meta.partial marks
// a run that got some of what it asked for, and the exit stays 0, because the
// data that did come back is real and the caller has it. Only a run where
// EVERY kind failed is a failure, and it returns the first kind's own typed
// error untouched, so an expired token still exits 5 and a Trakt refusal still
// exits 2. No new exit code is invented here; see the PR for the ruling.
func (a *App) runExport(dir string) error {
	if dir != "" {
		if cerr := prepareExportDir(dir); cerr != nil {
			return cerr
		}
	}

	start := time.Now()
	var (
		fields    []rawField
		failures  []exportFailure
		files     []json.RawMessage
		stats     = exportStats{Extended: a.exportExtended()}
		firstErr  *output.CLIError
		succeeded int
	)

	for _, k := range exportKinds {
		res, err := a.get(k.Path, a.exportOpts(k))
		if err != nil {
			cerr := asExportError(err)
			if firstErr == nil {
				firstErr = cerr
			}
			failures = append(failures, exportFailure{Kind: k.Name, Code: cerr.Code, Message: cerr.Message})
			continue
		}
		succeeded++

		stat := exportKindStat{
			Kind: k.Name, Endpoint: k.Path,
			Count: rowCount(res.Data), DurationMS: res.DurationMS,
		}
		if res.Pagination != nil {
			stat.Pages = res.Pagination.PageCount
		}

		if dir == "" {
			fields = append(fields, rawField{k.Name, res.Data})
		} else {
			name := k.Name + ".json"
			body := append(bytes.TrimRight(res.Data, "\n"), '\n')
			if werr := atomicfile.Write(filepath.Join(dir, name), body); werr != nil {
				// A failed write is this kind's failure, not the run's: the
				// other files are already on disk and must not be lost. The
				// code matches the one output.Writer uses for its own write
				// failures, so nothing new enters the error enum.
				wcerr := output.NewError(output.CodeParseError,
					"writing export file for "+k.Name+": "+werr.Error(), output.ExitInternal)
				if firstErr == nil {
					firstErr = wcerr
				}
				failures = append(failures, exportFailure{Kind: k.Name, Code: wcerr.Code, Message: wcerr.Message})
				succeeded--
				continue
			}
			stat.File, stat.Bytes = name, len(body)
			entry, merr := jsonRaw(map[string]interface{}{"kind": k.Name, "file": name, "bytes": len(body)})
			if merr != nil {
				return output.NewError(output.CodeParseError, merr.Error(), output.ExitInternal)
			}
			files = append(files, entry)
		}

		stats.Kinds = append(stats.Kinds, stat)
		stats.Pages += stat.Pages
		stats.Rows += stat.Count
	}

	// Nothing came back at all: report the first failure as itself rather than
	// dressing a total failure up as a successful empty export.
	if succeeded == 0 && firstErr != nil {
		return firstErr
	}

	if dir != "" {
		dirRaw, err := jsonRaw(dir)
		if err != nil {
			return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
		}
		fields = append(fields,
			rawField{"dir", dirRaw},
			rawField{"files", joinRawArray(files)})
	}

	stats.ElapsedMS = time.Since(start).Milliseconds()
	if len(failures) > 0 {
		raw, err := jsonRaw(failures)
		if err != nil {
			return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
		}
		fields = append(fields, rawField{exportErrorsKey, raw})
	}
	statsRaw, err := jsonRaw(stats)
	if err != nil {
		return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
	}
	fields = append(fields, rawField{exportStatsKey, statsRaw})

	data, err := rawObject(fields)
	if err != nil {
		return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
	}

	meta := &output.Meta{DurationMS: stats.ElapsedMS, TraktAPIVersion: "2"}
	if len(failures) > 0 {
		meta.Partial = true
	}
	terse := ""
	if a.Out.Format == output.FormatTerse {
		terse = exportTerse(stats, failures, dir)
	}
	return a.Out.Emit(&output.Result{Data: data, Meta: meta, Terse: terse})
}

// exportExtended reports the extended value the watched/collection fetches
// will carry, for the stats block.
func (a *App) exportExtended() string {
	return a.baseOpts(true).Extended
}

// asExportError types whatever a fetch returned. Every client failure path
// returns *output.CLIError; anything else would be a bug, and is reported as
// one rather than silently losing its message.
func asExportError(err error) *output.CLIError {
	var cerr *output.CLIError
	if errors.As(err, &cerr) {
		return cerr
	}
	return output.NewError(output.CodeParseError, err.Error(), output.ExitInternal)
}

// prepareExportDir makes --dir usable: an existing directory is kept (files
// are replaced per kind), a missing one is created 0700, and a path that
// exists as something other than a directory is a usage error rather than a
// mid-run write failure.
func prepareExportDir(dir string) *output.CLIError {
	info, err := os.Stat(dir)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		return output.UsageErrorHint("--dir exists and is not a directory: "+dir,
			"pass a directory path, or remove that file")
	case !errors.Is(err, fs.ErrNotExist):
		return output.UsageError("--dir is unusable: " + err.Error())
	}
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return output.UsageError("creating --dir: " + mkErr.Error())
	}
	return nil
}

// rowCount counts the rows of a kind's payload. A non-array body (Trakt has
// none here, but a future endpoint might) counts as one row.
func rowCount(raw json.RawMessage) int {
	if els, ok := rawjson.SplitArray(raw); ok {
		return len(els)
	}
	return 1
}

// rawField is one key of the data object, carrying bytes rather than a decoded
// value: Trakt ids exceed what a float64 holds, and a decode/re-encode round
// trip would rewrite them.
type rawField struct {
	key string
	val json.RawMessage
}

// rawObject assembles the data object from raw fields, in the order given.
func rawObject(fields []rawField) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		compact, err := rawjson.Compact(f.val)
		if err != nil {
			return nil, fmt.Errorf("value for key %q is not valid JSON: %w", f.key, err)
		}
		buf.Write(compact)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// joinRawArray builds a JSON array from element bytes, preserving them exactly.
func joinRawArray(els []json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, el := range els {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(el)
	}
	buf.WriteByte(']')
	return buf.Bytes()
}

// exportTerse is the --terse summary: one line per kind with its row count,
// one line per failed kind, then the run total.
func exportTerse(stats exportStats, failures []exportFailure, dir string) string {
	var b strings.Builder
	for _, k := range stats.Kinds {
		fmt.Fprintf(&b, "%s: %d\n", k.Kind, k.Count)
	}
	for _, f := range failures {
		fmt.Fprintf(&b, "%s: FAILED (%s)\n", f.Kind, f.Code)
	}
	fmt.Fprintf(&b, "%d of %d kinds, %d rows, %d pages, %.1fs",
		len(stats.Kinds), len(exportKinds), stats.Rows, stats.Pages,
		float64(stats.ElapsedMS)/1000)
	if dir != "" {
		fmt.Fprintf(&b, " -> %s", dir)
	}
	return b.String()
}
