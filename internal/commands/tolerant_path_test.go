package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTolerantCommandsStillForgiveAPathError: the three default-path failures
// that used to be silent are errors now, but they still wrap
// config.ErrConfigPath, so the diagnostic that reports them keeps exiting 0.
//
// `config path` is the assertion. `config init` shares the same exemption but
// cannot be asserted on these inputs: with no resolvable home there is nowhere
// to write, and with a directory sitting at the default path the write itself
// fails. Both are failures of the write, not of the tolerance.
func TestTolerantCommandsStillForgiveAPathError(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"default is a directory": func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			if err := os.MkdirAll(filepath.Join(home, ".config", "traktctl", "config.toml"), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		},
		"home lookup fails": func(t *testing.T) { t.Setenv("HOME", "") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("TRAKTCTL_CONFIG", "")
			setup(t)
			env, err := runRoot(t, "config", "path")
			if err != nil {
				t.Fatalf("`config path` over an unusable default path = %v, want success", err)
			}
			if !env.OK {
				t.Fatalf("ok = false, want true (error: %+v)", env.Error)
			}
			payload, ok := env.Data.(map[string]interface{})
			if !ok {
				t.Fatalf("data is not an object: %T", env.Data)
			}
			if msg, _ := payload["config_error"].(string); msg == "" {
				t.Error("config_error is empty; the whole point of the exemption is reporting it")
			}
		})
	}
}
