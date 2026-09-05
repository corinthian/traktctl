package commands

import (
	"os"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

// TestMain installs the in-memory keyring mock before any test in this package
// runs, for the same reason internal/auth does it: a command test that reaches
// `config path` or `auth status` drives Manager.Token(), which reads the token
// store -- and unmocked that is the real macOS Keychain entry (service
// "traktctl") holding this machine's live Trakt credentials. Tests here must be
// safe to run in any order, any number of times, on a developer's machine.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}
