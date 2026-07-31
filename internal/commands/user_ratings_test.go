package commands

import (
	"testing"

	"github.com/corinthian/traktctl/internal/output"
)

// TestRatingsPath covers BUG-1: `--rating N` without `--type` used to be
// parsed and dropped, because Trakt only honours the rating filter as a path
// segment after a type segment. Verified live 2026-07-31: /users/me/ratings
// and /users/me/ratings/all both return 2636 rows, /ratings/all/8 returns 322,
// /ratings/all/8,9 returns 409 — so defaulting the type to "all" is filter-
// enabling and otherwise byte-equivalent.
func TestRatingsPath(t *testing.T) {
	const me = "me"

	ok := []struct {
		name string
		typ  string
		rate string
		want string
	}{
		{"neither -> bare path, unchanged", "", "", "/users/me/ratings"},
		{"rating only -> type defaults to all", "", "8", "/users/me/ratings/all/8"},
		{"type only -> unchanged", "movies", "", "/users/me/ratings/movies"},
		{"type + rating -> both segments", "shows", "10", "/users/me/ratings/shows/10"},
		{"explicit all + rating", "all", "1", "/users/me/ratings/all/1"},
		{"comma set is passed through", "", "8,9", "/users/me/ratings/all/8,9"},
		{"comma set tolerates spaces", "movies", "8, 9", "/users/me/ratings/movies/8, 9"},
	}
	for _, tc := range ok {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ratingsPath(me, tc.typ, tc.rate)
			if err != nil {
				t.Fatalf("ratingsPath(%q, %q, %q) err = %v, want nil", me, tc.typ, tc.rate, err)
			}
			if got != tc.want {
				t.Errorf("ratingsPath(%q, %q, %q) = %q, want %q", me, tc.typ, tc.rate, got, tc.want)
			}
		})
	}

	// Out-of-range and non-numeric ratings must be rejected locally: Trakt
	// answers /ratings/all/11 with HTTP 200 and an empty array, which reads
	// exactly like "you have rated nothing 11".
	bad := []struct {
		name string
		typ  string
		rate string
	}{
		{"above range", "", "11"},
		{"zero", "", "0"},
		{"negative", "", "-1"},
		{"non-numeric", "", "eight"},
		{"empty element in set", "", "8,"},
		{"one bad element in a set", "", "8,99"},
		{"invalid type", "films", ""},
		{"invalid type with rating", "movie", "8"},
	}
	for _, tc := range bad {
		t.Run("reject "+tc.name, func(t *testing.T) {
			got, err := ratingsPath(me, tc.typ, tc.rate)
			if err == nil {
				t.Fatalf("ratingsPath(%q, %q, %q) = %q, want an error", me, tc.typ, tc.rate, got)
			}
			if err.Code != output.CodeBadRequest {
				t.Errorf("code = %q, want %q", err.Code, output.CodeBadRequest)
			}
			if err.Exit != output.ExitUser {
				t.Errorf("exit = %d, want %d", err.Exit, output.ExitUser)
			}
			if err.Hint == "" {
				t.Error("missing hint")
			}
		})
	}
}
