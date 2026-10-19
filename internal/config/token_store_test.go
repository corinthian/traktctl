package config

import (
	"strings"
	"testing"
)

func TestTokenStoreDefaultsToKeychain(t *testing.T) {
	t.Setenv("TRAKTCTL_TOKEN_STORE", "")
	cfg, err := Load(Flags{ConfigPath: writeTempConfig(t, "")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TokenStore != TokenStoreKeychain {
		t.Errorf("TokenStore = %q, want keychain", cfg.TokenStore)
	}
}

func TestTokenStoreFromFile(t *testing.T) {
	t.Setenv("TRAKTCTL_TOKEN_STORE", "")
	cfg, err := Load(Flags{ConfigPath: writeTempConfig(t, "token_store = \"file\"\n")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TokenStore != TokenStoreFile {
		t.Errorf("TokenStore = %q, want file", cfg.TokenStore)
	}
}

func TestTokenStoreEnvBeatsFile(t *testing.T) {
	t.Setenv("TRAKTCTL_TOKEN_STORE", "keychain")
	cfg, err := Load(Flags{ConfigPath: writeTempConfig(t, "token_store = \"file\"\n")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TokenStore != TokenStoreKeychain {
		t.Errorf("TokenStore = %q, want the env value keychain", cfg.TokenStore)
	}
}

func TestTokenStoreInvalid(t *testing.T) {
	cases := []struct {
		name, env, body string
		flags           Flags
		wantSrc         string
	}{
		{"file", "", "token_store = \"disk\"\n", Flags{}, "config token_store"},
		{"env", "disk", "", Flags{}, "$TRAKTCTL_TOKEN_STORE"},
		{"with --timeout", "", "token_store = \"disk\"\n", Flags{Timeout: "5", TimeoutSet: true}, "config token_store"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRAKTCTL_TOKEN_STORE", c.env)
			f := c.flags
			f.ConfigPath = writeTempConfig(t, c.body)
			_, err := Load(f)
			if err == nil {
				t.Fatal("Load accepted an invalid token_store")
			}
			msg := err.Error()
			if !strings.Contains(msg, c.wantSrc) || !strings.Contains(msg, `"keychain"`) || !strings.Contains(msg, `"file"`) {
				t.Errorf("error %q should name %s and the allowed values", msg, c.wantSrc)
			}
		})
	}
}
