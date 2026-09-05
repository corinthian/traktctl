package commands

import (
	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
)

// GroupFactory builds a command group's root cobra.Command from the shared App.
type GroupFactory func(app *App) *cobra.Command

// registry holds every self-registered group factory. Group files call
// Register from their init(), so the root command never needs editing to add a
// group — this is what keeps parallel group work conflict-free.
var registry []GroupFactory

// Register adds a group factory. Call from a group file's init().
func Register(f GroupFactory) { registry = append(registry, f) }

// addAll attaches every registered group to root. Hardening is a separate pass
// (harden), run once from NewRoot after the whole tree exists.
func addAll(root *cobra.Command, app *App) {
	for _, f := range registry {
		root.AddCommand(f(app))
	}
}

// harden walks the whole command tree and applies two rules.
//
// First, every command gets cobra.NoArgs. Nothing in this CLI reads a
// positional -- every input arrives as a flag -- so cobra's default
// (leaves accept and silently discard anything trailing) meant
// `traktctl movie trending garbage` exited 0 having ignored the word. One
// line here covers the tree; the alternative was editing every factory in
// helpers.go and remembering to do it for each new one. On a command with
// subcommands cobra phrases the failure as `unknown command "x" for ...`,
// which is the right message, and classifyError maps it to BAD_REQUEST.
//
// Second, a parent command (subcommands present, no RunE of its own) becomes
// Runnable with a RunE that reports a missing subcommand, so that:
//   - `<group> --llm` emits JSON help (the parent being Runnable is what lets
//     the root PersistentPreRunE's --llm short-circuit fire instead of cobra
//     printing human help and exiting 0), and
//   - `<group>` with no/unknown subcommand returns a JSON error envelope at
//     exit 1 instead of printing help at exit 0.
//
// Cobra's built-in `help` and `completion` commands are added at Execute time,
// after this runs, so they keep their own arg handling.
func harden(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		harden(c)
	}
	cmd.Args = cobra.NoArgs
	if !cmd.HasSubCommands() || cmd.RunE != nil || cmd.Run != nil {
		return
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		return output.UsageErrorHint(
			"missing subcommand for `"+c.CommandPath()+"`",
			"run `"+c.CommandPath()+" --help` to list subcommands, or `--llm` for JSON help")
	}
}
