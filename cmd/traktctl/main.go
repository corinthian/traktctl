// Command traktctl is a JSON-first CLI wrapper over the Trakt API.
package main

import "github.com/rlarsen/traktctl/internal/commands"

func main() {
	commands.Execute()
}
