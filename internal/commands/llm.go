package commands

import (
	"encoding/json"
	"strings"

	"github.com/corinthian/traktctl/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// llmHelp is the machine-readable per-command help object emitted by --llm.
// Per the spec, every command's --llm returns {usage, args, flags, examples,
// output_schema}; output_schema is always present (no omitempty) and examples
// is always populated.
type llmHelp struct {
	Command      string     `json:"command"`
	Usage        string     `json:"usage"`
	Summary      string     `json:"summary"`
	Args         []string   `json:"args"`
	Flags        []llmFlag  `json:"flags"`
	Examples     []string   `json:"examples"`
	OutputSchema string     `json:"output_schema"`
	Subcommands  []llmChild `json:"subcommands,omitempty"`
}

type llmFlag struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default string `json:"default,omitempty"`
	Usage   string `json:"usage"`
	Scope   string `json:"scope"`
}

type llmChild struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// emitLLMHelp prints the JSON help object for cmd and is the --llm handler.
// Endpoint/examples/output_schema come from cmd.Annotations when a group sets
// them; everything else is derived from the cobra command.
func emitLLMHelp(cmd *cobra.Command, out *output.Writer) {
	h := llmHelp{
		Command: cmd.CommandPath(),
		Usage:   cmd.UseLine(),
		Summary: cmd.Short,
	}
	if cmd.Args != nil || len(cmd.ValidArgs) > 0 {
		h.Args = cmd.ValidArgs
	}
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		scope := "inherited"
		if cmd.LocalFlags().Lookup(f.Name) != nil {
			scope = "local"
		}
		h.Flags = append(h.Flags, llmFlag{
			Name: f.Name, Type: f.Value.Type(), Default: f.DefValue, Usage: f.Usage, Scope: scope,
		})
	})
	if ex := cmd.Annotations["examples"]; ex != "" {
		h.Examples = strings.Split(ex, "\n")
	} else {
		h.Examples = synthExamples(cmd)
	}
	if os := cmd.Annotations["output_schema"]; os != "" {
		h.OutputSchema = os
	} else {
		h.OutputSchema = synthOutputSchema(cmd)
	}
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" {
			continue
		}
		h.Subcommands = append(h.Subcommands, llmChild{Name: c.Name(), Summary: c.Short})
	}
	data, _ := json.MarshalIndent(h, "", "  ")
	out.Out.Write(append(data, '\n'))
}

// synthExamples builds at least one usable example invocation when a command
// supplies none via annotations, so the --llm `examples` key is never null.
func synthExamples(cmd *cobra.Command) []string {
	path := cmd.CommandPath()
	// Group/parent: show how to discover and machine-read it. (Parents are
	// hardened with a RunE, so detect them by their subcommands, not RunE==nil.)
	if cmd.HasSubCommands() {
		return []string{path + " --help", path + " --llm"}
	}
	ex := path
	// Inherited globals a command opts into surfacing — e.g. `search id` needs
	// --id even though it is a root persistent flag. Without this, LocalFlags()
	// (used below to keep --id out of payload writers) also hides it from the
	// commands that require it. Comma-separated flag names.
	if g := cmd.Annotations["example_globals"]; g != "" {
		for _, name := range strings.Split(g, ",") {
			if name = strings.TrimSpace(name); name != "" {
				ex += " --" + name + " <" + name + ">"
			}
		}
	}
	// Append common flags with placeholder values, restricted to flags the
	// command itself declares (LocalFlags) so inherited globals like --id
	// don't leak into examples for commands that ignore them. Groups
	// returned early above, so this runs only for leaf commands.
	for _, f := range []string{"id", "show", "season", "episode", "q", "type", "list-id", "section", "year", "payload"} {
		if fl := cmd.LocalFlags().Lookup(f); fl != nil {
			ex += " --" + f + " <" + f + ">"
		}
	}
	out := []string{ex}
	out = append(out, ex+" --terse")
	return out
}

// synthOutputSchema returns a default description of the --llm output contract
// when a command does not annotate its own. The envelope is uniform across all
// commands, so the default documents that envelope.
func synthOutputSchema(cmd *cobra.Command) string {
	return "JSON envelope: {ok:bool, data:object|array|null, meta:{endpoint,duration_ms," +
		"trakt_api_version,pagination?}} on success; {ok:false, error:{code,message," +
		"http_status?,hint?}} on failure. data is the raw Trakt response for this endpoint."
}

// commandNode is a node in the `traktctl commands` JSON tree.
type commandNode struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Summary     string        `json:"summary"`
	Subcommands []commandNode `json:"subcommands,omitempty"`
}

func buildCommandTree(root *cobra.Command) commandNode {
	return nodeFor(root)
}

func nodeFor(cmd *cobra.Command) commandNode {
	n := commandNode{Name: cmd.Name(), Path: cmd.CommandPath(), Summary: cmd.Short}
	for _, c := range cmd.Commands() {
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" {
			continue
		}
		n.Subcommands = append(n.Subcommands, nodeFor(c))
	}
	return n
}

func jsonRaw(v interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
