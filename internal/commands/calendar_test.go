package commands

import "testing"

// TestCalendarPath covers TC-6: --days with no --start used to be silently
// dropped. calendarPath must default start to today whenever days is set, so
// `calendar all-movies --days 30` (no --start) actually sends the window.
func TestCalendarPath(t *testing.T) {
	const prefix = "/calendars/all/movies"
	const today = "2026-07-15"

	tests := []struct {
		name  string
		start string
		days  int
		want  string
	}{
		{"days only -> start defaults to today", "", 3, "/calendars/all/movies/2026-07-15/3"},
		{"neither -> bare prefix, unchanged", "", 0, "/calendars/all/movies"},
		{"explicit start + days -> both used, no default", "2026-01-01", 7, "/calendars/all/movies/2026-01-01/7"},
		{"explicit start, no days -> start only", "2026-01-01", 0, "/calendars/all/movies/2026-01-01"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := calendarPath(prefix, tc.start, tc.days, today)
			if got != tc.want {
				t.Errorf("calendarPath(%q, %q, %d, %q) = %q, want %q",
					prefix, tc.start, tc.days, today, got, tc.want)
			}
		})
	}
}
