package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestResolvePayloadFileFromStdin covers --payload-file - reading stdin.
func TestResolvePayloadFileFromStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()

	go func() {
		w.Write([]byte(`{"a":1}`))
		w.Close()
	}()

	got, err := resolvePayload("", "-")
	if err != nil {
		t.Fatalf("resolvePayload: %v", err)
	}
	want, _ := parsePayload(`{"a":1}`)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resolvePayload(stdin) = %v, want %v", got, want)
	}
}

// TestResolvePayloadMutuallyExclusive covers --payload and --payload-file
// both set: that's a usage error, not a silent pick-one.
func TestResolvePayloadMutuallyExclusive(t *testing.T) {
	if _, err := resolvePayload(`{"a":1}`, "somefile.json"); err == nil {
		t.Fatal("expected error when both --payload and --payload-file are set")
	}
}

// TestResolvePayloadFileRoundTripsSameBodyAsPayload covers the file variant
// producing the identical decoded body as the equivalent --payload string.
func TestResolvePayloadFileRoundTripsSameBodyAsPayload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.json")
	body := `{"movies":[{"ids":{"slug":"gilda-1946"}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp payload file: %v", err)
	}

	fromFile, err := resolvePayload("", path)
	if err != nil {
		t.Fatalf("resolvePayload(file): %v", err)
	}
	fromInline, err := resolvePayload(body, "")
	if err != nil {
		t.Fatalf("resolvePayload(inline): %v", err)
	}
	if !reflect.DeepEqual(fromFile, fromInline) {
		t.Errorf("file variant = %v, inline variant = %v; want equal", fromFile, fromInline)
	}
}
