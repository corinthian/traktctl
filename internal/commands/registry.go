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

// addAll attaches every registered group to root, then hardens every parent
// (group) command so that:
//   - `<group> --llm` emits JSON help (the parent becomes Runnable, so the
//     root PersistentPreRunE's --llm short-circuit fires instead of cobra
//     printing human help and exiting 0), and
//   - `<group>` with no/unknown subcommand returns a JSON error envelope at
//     exit 1 instead of printing help at exit 0.
//
// Leaf commands (those with their own RunE) are left untouched.
func addAll(root *cobra.Command, app *App) {
	for _, f := range registry {
		g := f(app)
		root.AddCommand(g)
		hardenGroup(g)
	}
}

// hardenGroup recursively converts parent commands (subcommands present, no
// RunE) into erroring groups with cobra.NoArgs and a RunE that reports a
// missing subcommand. Leaves are skipped.
func hardenGroup(cmd *cobra.Command) {
	for _, c := range cmd.Commands() {
		hardenGroup(c)
	}
	if !cmd.HasSubCommands() || cmd.RunE != nil || cmd.Run != nil {
		return
	}
	cmd.Args = cobra.NoArgs
	cmd.RunE = func(c *cobra.Command, args []string) error {
		e := output.NewError(output.CodeBadConfig,
			"missing subcommand for `"+c.CommandPath()+"`", output.ExitUser)
		e.Hint = "run `" + c.CommandPath() + " --help` to list subcommands, or `--llm` for JSON help"
		return e
	}
}
