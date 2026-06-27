package commands

import (
	"testing"
)

// TestConfirmedGate covers the destructive-mutation authorization gate
// (app.go confirmed()): the settings/reorder/update-item and remove verbs must
// refuse without authorization and proceed when either --confirm is set OR
// TRAKTCTL_CONFIRM=1 is in the environment. The "proceeds" legs are asserted
// here rather than against the live account, since firing them live would mutate
// real Trakt state.
func TestConfirmedGate(t *testing.T) {
	tests := []struct {
		name        string
		confirmFlag bool
		envVal      string
		want        bool
	}{
		{"no flag, empty env -> refuse", false, "", false},
		{"--confirm only -> proceed", true, "", true},
		{"TRAKTCTL_CONFIRM=1 only -> proceed", false, "1", true},
		{"both -> proceed", true, "1", true},
		{"env set but not exactly 1 -> refuse", false, "true", false},
		{"env set to 0 -> refuse", false, "0", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRAKTCTL_CONFIRM", tc.envVal)
			a := &App{Flags: &GlobalFlags{Confirm: tc.confirmFlag}}
			if got := a.confirmed(); got != tc.want {
				t.Errorf("confirmed() with flag=%v env=%q = %v, want %v",
					tc.confirmFlag, tc.envVal, got, tc.want)
			}
		})
	}
}
