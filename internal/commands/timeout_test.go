package commands

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/corinthian/traktctl/internal/output"
)

// TestTimeoutFlagInvalidIsBadRequest pins 2.1's flag row: any rejected
// --timeout value is a usage error, not a silently-skipped unknown flag (the
// surface is new) and not a config error (the source is the flag, not the
// environment or the file).
func TestTimeoutFlagInvalidIsBadRequest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")
	for _, v := range []string{"30s", "abc", "0", "-5", "90000", "", " 30 "} {
		_, err := runRoot(t, "--client-id", "cid", "--timeout", v, "config", "path")
		if err == nil {
			t.Errorf("--timeout %q = nil error, want BAD_REQUEST", v)
			continue
		}
		var cerr *output.CLIError
		if !asCLIError(err, &cerr) {
			t.Errorf("--timeout %q err type = %T, want *output.CLIError", v, err)
			continue
		}
		if cerr.Code != output.CodeBadRequest {
			t.Errorf("--timeout %q code = %q, want %q", v, cerr.Code, output.CodeBadRequest)
		}
		if cerr.Exit != output.ExitUser {
			t.Errorf("--timeout %q exit = %v, want %v", v, cerr.Exit, output.ExitUser)
		}
	}
}

// TestTimeoutFlagValidApplies is the new-surface row: --timeout is accepted
// (exit 0) and actually threads through to the HTTP client's deadline rather
// than being parsed and discarded -- proven by a timeout too short for a
// deliberately slow handler failing as TRANSPORT_TIMEOUT.
func TestTimeoutFlagValidApplies(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TRAKTCTL_CONFIG", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	// Comfortably longer than the handler's delay: accepted, exit 0.
	if _, err := runRoot(t, "--client-id", "cid", "--base-url", srv.URL, "--timeout", "5",
		"search", "query", "--type", "movie", "--q", "x"); err != nil {
		t.Fatalf("--timeout 5 against a 300ms handler = %v, want success", err)
	}

	// The client.New minimum granularity is a whole second, so 1s against a
	// deliberately slower handler pins the flag actually reaching the client
	// rather than being ignored (today's behaviour: unknown flag, never
	// reaching a client at all).
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		w.Write([]byte(`[]`))
	}))
	defer slow.Close()

	_, err := runRoot(t, "--client-id", "cid", "--base-url", slow.URL, "--timeout", "1",
		"search", "query", "--type", "movie", "--q", "x")
	if err == nil {
		t.Fatal("--timeout 1 against a 1.5s handler = nil, want TRANSPORT_TIMEOUT")
	}
	var cerr *output.CLIError
	if !asCLIError(err, &cerr) {
		t.Fatalf("err type = %T, want *output.CLIError", err)
	}
	if cerr.Code != output.CodeTransportTimeout {
		t.Fatalf("code = %q, want %q", cerr.Code, output.CodeTransportTimeout)
	}
}
