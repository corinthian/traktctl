package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// summarize produces a one-line human summary for the common Trakt object
// shapes (movie/show/episode/season/person/list/user). It is best-effort: when
// the body does not match a known shape it returns "" and the writer falls back
// to a compact JSON line. Used by emit() for the --terse path.
func summarize(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	// Arrays: summarize the first element and note the count.
	if trimmed := strings.TrimSpace(string(data)); strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil || len(arr) == 0 {
			return ""
		}
		first := summarizeObject(arr[0])
		if first == "" {
			return ""
		}
		if len(arr) == 1 {
			return first
		}
		return fmt.Sprintf("%s (+%d more)", first, len(arr)-1)
	}
	return summarizeObject(data)
}

// summarizeObject summarizes a single JSON object, unwrapping Trakt's common
// nesting ({"movie":{…}}, {"show":{…}}, list-item wrappers, etc.).
func summarizeObject(data json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}

	// An episode wrapped alongside a show (history/progress shape): combine
	// "Show (Year) - SxxEyy Title". Handle before the generic unwrap so the
	// show wrapper does not win on its own.
	if epRaw, ok := m["episode"]; ok {
		ep := summarizeKnown("episode", epRaw)
		if showRaw, ok := m["show"]; ok {
			if showName := titleOf(showRaw); showName != "" && ep != "" {
				return showName + " - " + ep
			}
		}
		if ep != "" {
			return ep
		}
	}

	// Self-identifying bare object: when the object carries its own shape fields
	// at the top level (e.g. a single list = name+item_count, which ALSO nests a
	// `user` owner key), render it directly before the wrapper-unwrap loop so the
	// nested owner does not win. Without this, `user list`/`user lists --terse`
	// would print the list OWNER instead of the list NAME.
	//
	// This must run before the cast/crew check below: a title can legitimately
	// carry a top-level "cast" key alongside its own title/year (Trakt does not
	// promise it never will), and a real title/year object must still summarise
	// as a title, not get reinterpreted as a filmography.
	if shape := inferShape(m); shape != "" {
		if s := summarizeKnown(shape, data); s != "" {
			return s
		}
	}

	// A filmography response (person movies/shows, movie/show/season/episode
	// people): {"cast":[...], "crew":{"department":[...]}}. Neither key
	// carries a title/name/item_count, so inferShape above sees nothing and
	// this would otherwise fall through to "" — a real regression for a
	// shape whose whole point is "here is a non-empty credit list".
	if _, hasCast := m["cast"]; hasCast {
		return summarizeFilmography(m)
	}
	if _, hasCrew := m["crew"]; hasCrew {
		return summarizeFilmography(m)
	}

	// Unwrap a typed wrapper: {"type":"movie","movie":{…}} or a list/history
	// item that nests the media object under its type key.
	for _, key := range []string{"movie", "show", "season", "person", "list", "user"} {
		if inner, ok := m[key]; ok {
			if s := summarizeKnown(key, inner); s != "" {
				return s
			}
		}
	}

	// Bare object: infer the shape from its fields.
	return summarizeKnown(inferShape(m), data)
}

// inferShape guesses the object type from its key set.
func inferShape(m map[string]json.RawMessage) string {
	_, hasSeasonNum := m["number"]
	_, hasEpisodes := m["episodes"]
	switch {
	case has(m, "username") || has(m, "vip"):
		return "user"
	case has(m, "item_count") && has(m, "name"):
		return "list"
	case hasSeasonNum && hasEpisodes:
		return "season"
	case has(m, "season") && has(m, "number"):
		return "episode"
	case hasSeasonNum && !has(m, "title") && !has(m, "year"):
		// A bare season object (e.g. season info without --extended full) carries
		// only {ids, number}: no episodes array, no title/year. Classify as season
		// so "Season N" renders from the number alone.
		return "season"
	case has(m, "year") || has(m, "title"):
		// Movie and show both have title+year; show has no runtime distinction
		// we can rely on, so treat generically via title.
		return "movie"
	case has(m, "name"):
		// A bare object with a name but no title/year is a person.
		return "person"
	default:
		return ""
	}
}

func has(m map[string]json.RawMessage, k string) bool { _, ok := m[k]; return ok }

// summarizeKnown renders a known shape from its raw object.
func summarizeKnown(shape string, raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	switch shape {
	case "movie", "show":
		title := str(m["title"])
		if title == "" {
			return ""
		}
		if y := num(m["year"]); y != "" {
			out := fmt.Sprintf("%s (%s)", title, y)
			return appendWatching(out, m)
		}
		return appendWatching(title, m)
	case "episode":
		title := str(m["title"])
		s, e := num(m["season"]), num(m["number"])
		code := ""
		if s != "" && e != "" {
			code = "S" + pad(s) + "E" + pad(e)
		}
		switch {
		case code != "" && title != "":
			return code + " " + title
		case title != "":
			return title
		case code != "":
			return code
		}
		return ""
	case "season":
		n := num(m["number"])
		if n == "" {
			return ""
		}
		out := "Season " + n
		if ec := num(m["episode_count"]); ec != "" {
			out += fmt.Sprintf(" (%s episodes)", ec)
		}
		return out
	case "person":
		if name := str(m["name"]); name != "" {
			return name
		}
		return ""
	case "list":
		name := str(m["name"])
		if name == "" {
			return ""
		}
		if ic := num(m["item_count"]); ic != "" {
			return fmt.Sprintf("%s (%s items)", name, ic)
		}
		return name
	case "user":
		name := str(m["name"])
		uname := str(m["username"])
		switch {
		case name != "" && uname != "":
			return fmt.Sprintf("%s (@%s)", name, uname)
		case uname != "":
			return "@" + uname
		case name != "":
			return name
		}
		return ""
	}
	return ""
}

// filmographyNameLimit caps how many names summarizeFilmography spells out
// before collapsing the rest into "(+N more)" — house style for any credit
// list, matching the single-name-plus-count convention summarize() already
// uses for a bare array (see summarize()'s "(+%d more)" branch above).
const filmographyNameLimit = 2

// summarizeFilmography renders a cast/crew credit map ({"cast":[...],
// "crew":{"department":[...]}}) — the shape returned by person movies/shows
// and by movie/show/season/episode people — as names plus a count, e.g.
// "Bryan Cranston, Aaron Paul (+2 more)", never as a bare count: the count is
// a suffix, not a replacement for the names a caller actually wants.
//
// Returns "" (fall back to raw JSON) when the shape doesn't decode the way
// this function expects — crew present but not a department map, or cast
// entries that don't carry a recognizable name — rather than misreporting an
// unrecognized shape as "no credits". An empty-but-well-formed filmography
// (both arrays present and empty) is a real, non-error answer: "no credits".
func summarizeFilmography(m map[string]json.RawMessage) string {
	castN := arrLen(m["cast"])
	crewN := 0
	if raw, ok := m["crew"]; ok {
		var depts map[string]json.RawMessage
		if err := json.Unmarshal(raw, &depts); err != nil {
			return ""
		}
		for _, d := range depts {
			crewN += arrLen(d)
		}
	}
	total := castN + crewN

	if castN > 0 {
		names := creditNames(m["cast"], filmographyNameLimit)
		if len(names) == 0 {
			// Cast entries exist but none decoded into a recognizable
			// person/movie/show name -- an unrecognized shape, not "no
			// credits".
			return ""
		}
		joined := strings.Join(names, ", ")
		if total > len(names) {
			return fmt.Sprintf("%s (+%d more)", joined, total-len(names))
		}
		return joined
	}
	if crewN > 0 {
		return plural(crewN, "crew") + " credit" + plural1(crewN)
	}
	return "no credits"
}

// creditName extracts a display name from one cast/crew entry: the nested
// person's name for a title's cast/crew ({"person":{"name":...}}, from
// `movie|show|season|episode people`), or the nested movie/show's
// "Title (Year)" for a person's filmography ({"movie":{...}}/{"show":{...}},
// from `person movies|shows`). Returns "" on any other shape.
func creditName(raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	if p, ok := m["person"]; ok {
		var pm map[string]json.RawMessage
		if err := json.Unmarshal(p, &pm); err != nil {
			return ""
		}
		return str(pm["name"])
	}
	if mv, ok := m["movie"]; ok {
		return titleOf(mv)
	}
	if sh, ok := m["show"]; ok {
		return titleOf(sh)
	}
	return ""
}

// creditNames collects up to limit non-empty display names from a cast/crew
// array, in order, skipping entries that yield no name rather than padding
// the list with blanks.
func creditNames(raw json.RawMessage, limit int) []string {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	var names []string
	for _, item := range arr {
		if len(names) >= limit {
			break
		}
		if n := creditName(item); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// plural renders "N label" (e.g. "1 cast", "3 crew").
func plural(n int, label string) string {
	return strconv.Itoa(n) + " " + label
}

// plural1 returns "" for n==1, "s" otherwise, for a trailing noun.
func plural1(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// arrLen returns the length of a JSON array field, or 0 if absent/not an array.
func arrLen(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}

// appendWatching adds a " · N watching" suffix when the body carries a watcher
// count (e.g. the /watching aggregate shape).
func appendWatching(s string, m map[string]json.RawMessage) string {
	if w := num(m["watcher_count"]); w != "" {
		return fmt.Sprintf("%s · %s watching", s, w)
	}
	return s
}

// titleOf returns "Title (Year)" or just the title of a nested media object.
func titleOf(raw json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	title := str(m["title"])
	if title == "" {
		return ""
	}
	if y := num(m["year"]); y != "" {
		return fmt.Sprintf("%s (%s)", title, y)
	}
	return title
}

// str decodes a JSON string field, returning "" on any mismatch.
func str(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// num decodes a JSON number (or numeric string) field to its string form.
func num(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return str(raw)
}

// pad left-pads a 1-digit numeric string to 2 digits for SxxEyy codes.
func pad(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}
