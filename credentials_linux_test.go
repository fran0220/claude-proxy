//go:build linux

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxCredentialPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	path, err := claudeCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".claude", ".credentials.json"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "~/server-claude")
	path, err = claudeCredentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "server-claude", ".credentials.json"); path != want {
		t.Fatalf("custom path = %q, want %q", path, want)
	}
}

func TestLinuxCredentialStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, ".credentials.json")
	if err := os.WriteFile(path, []byte(`{"other":{"keep":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := &ClaudeCredentials{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    1234,
		Scopes:       []string{"scope"},
	}

	if err := WriteClaudeCredentials(want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	got, err := ReadClaudeCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.ExpiresAt != want.ExpiresAt {
		t.Fatalf("credentials = %#v, want %#v", got, want)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonFieldExists(data, "other") {
		t.Fatal("write removed an unrelated top-level field")
	}
}

func jsonFieldExists(data []byte, field string) bool {
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return false
	}
	_, ok := document[field]
	return ok
}
