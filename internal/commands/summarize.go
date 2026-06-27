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
	if shape := inferShape(m); shape != "" {
		if s := summarizeKnown(shape, data); s != "" {
			return s
		}
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
