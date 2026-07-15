package commands

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestSynthExamplesExampleGlobals covers TC-2: synthExamples must surface an
// inherited global (e.g. --id) when a command opts in via the
// Annotations["example_globals"] key, since LocalFlags() alone (used to keep
// --id out of payload writers, see TC-1) can't see persistent/root flags.
func TestSynthExamplesExampleGlobals(t *testing.T) {
	cmd := &cobra.Command{
		Use:         "widget",
		Annotations: map[string]string{"example_globals": "id"},
		RunE:        func(cmd *cobra.Command, args []string) error { return nil },
	}
	// Register a parent so CommandPath() resolves as expected, and so the
	// command isn't treated as HasSubCommands (it has none, so this is just
	// for a realistic path).
	root := &cobra.Command{Use: "traktctl"}
	root.AddCommand(cmd)

	got := synthExamples(cmd)
	if len(got) == 0 {
		t.Fatal("synthExamples returned no examples")
	}
	if !strings.Contains(got[0], "--id <id>") {
		t.Errorf("synthExamples(%q) = %v, want first example to contain --id <id>", cmd.Use, got)
	}
}

// TestSynthExamplesSearchID covers the live `search id` command definition:
// its example must contain both --id and --id-type (annotated "id,id-type").
func TestSynthExamplesSearchID(t *testing.T) {
	root, _ := NewRoot()
	cmd, _, err := root.Find([]string{"search", "id"})
	if err != nil {
		t.Fatalf("could not find `search id` command: %v", err)
	}
	got := synthExamples(cmd)
	if len(got) == 0 {
		t.Fatal("synthExamples returned no examples")
	}
	if !strings.Contains(got[0], "--id <id>") {
		t.Errorf("search id example = %v, want it to contain --id <id>", got)
	}
	if !strings.Contains(got[0], "--id-type <id-type>") {
		t.Errorf("search id example = %v, want it to contain --id-type <id-type>", got)
	}
}

// TestSynthExamplesRecommendHide covers `recommend hide-movie` /
// `recommend hide-show` (the recommendHide factory, annotated "id").
func TestSynthExamplesRecommendHide(t *testing.T) {
	root, _ := NewRoot()
	for _, use := range []string{"hide-movie", "hide-show"} {
		cmd, _, err := root.Find([]string{"recommend", use})
		if err != nil {
			t.Fatalf("could not find `recommend %s` command: %v", use, err)
		}
		got := synthExamples(cmd)
		if len(got) == 0 || !strings.Contains(got[0], "--id <id>") {
			t.Errorf("recommend %s example = %v, want it to contain --id <id>", use, got)
		}
	}
}

// TestSynthExamplesSyncPlaybackRemove covers `sync playback remove`
// (annotated "id").
func TestSynthExamplesSyncPlaybackRemove(t *testing.T) {
	root, _ := NewRoot()
	cmd, _, err := root.Find([]string{"sync", "playback", "remove"})
	if err != nil {
		t.Fatalf("could not find `sync playback remove` command: %v", err)
	}
	got := synthExamples(cmd)
	if len(got) == 0 || !strings.Contains(got[0], "--id <id>") {
		t.Errorf("sync playback remove example = %v, want it to contain --id <id>", got)
	}
}

// TestSynthExamplesPayloadWriterOmitsID is the load-bearing regression: a
// payload writer with no example_globals annotation (e.g. `sync history add`)
// must NOT surface --id in its synthesized examples. This is the behavior
// TC-1's fix established (--id is silently ignored by payload writers, and
// rejectIDFlags errors if it's passed) — TC-2 must not leak --id back into it.
func TestSynthExamplesPayloadWriterOmitsID(t *testing.T) {
	root, _ := NewRoot()
	cmd, _, err := root.Find([]string{"sync", "history", "add"})
	if err != nil {
		t.Fatalf("could not find `sync history add` command: %v", err)
	}
	if g := cmd.Annotations["example_globals"]; g != "" {
		t.Fatalf("sync history add unexpectedly has example_globals=%q; TC-1 payload writers must not opt in", g)
	}
	got := synthExamples(cmd)
	for _, ex := range got {
		if strings.Contains(ex, "--id") {
			t.Errorf("sync history add example = %q, must NOT contain --id (payload writers ignore --id, see TC-1)", ex)
		}
	}
}
