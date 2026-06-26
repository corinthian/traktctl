package commands

import "github.com/spf13/cobra"

// GroupFactory builds a command group's root cobra.Command from the shared App.
type GroupFactory func(app *App) *cobra.Command

// registry holds every self-registered group factory. Group files call
// Register from their init(), so the root command never needs editing to add a
// group — this is what keeps parallel group work conflict-free.
var registry []GroupFactory

// Register adds a group factory. Call from a group file's init().
func Register(f GroupFactory) { registry = append(registry, f) }

// addAll attaches every registered group to root.
func addAll(root *cobra.Command, app *App) {
	for _, f := range registry {
		root.AddCommand(f(app))
	}
}
