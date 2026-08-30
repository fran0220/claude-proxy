package main

import (
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

const keychainService = "Claude Code-credentials"

// ReadClaudeCredentials reads Claude Code OAuth credentials from macOS Keychain.
// Uses the `security` CLI tool to avoid CGO dependencies.
func ReadClaudeCredentials() (*ClaudeCredentials, error) {
	account, err := currentUsername()
	if err != nil {
		return nil, fmt.Errorf("get current user: %w", err)
	}

	cmd := exec.Command("security", "find-generic-password",
		"-s", keychainService,
		"-a", account,
		"-w",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain read failed (service=%q, account=%q): %w", keychainService, account, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return nil, fmt.Errorf("keychain entry is empty")
	}
	return decodeClaudeCredentials([]byte(raw))
}

// WriteClaudeCredentials persists Claude Code OAuth credentials back
// to the macOS Keychain entry. Anthropic rotates refresh tokens on every
// refresh; writing back lets Claude.app and other Claude Code-compatible tools
// share the latest rotated refresh token via the keychain instead of stranding
// it in the memory of whichever process refreshed first.
//
// Uses `security add-generic-password -U` (update if exists) to avoid CGO.
func WriteClaudeCredentials(credentials *ClaudeCredentials) error {
	account, err := currentUsername()
	if err != nil {
		return fmt.Errorf("get current user: %w", err)
	}
	payload, err := mergeClaudeCredentials(nil, credentials)
	if err != nil {
		return fmt.Errorf("marshal keychain payload: %w", err)
	}
	cmd := exec.Command("security", "add-generic-password",
		"-U",
		"-s", keychainService,
		"-a", account,
		"-w", strings.TrimSpace(string(payload)),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("keychain write failed (service=%q): %w: %s", keychainService, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func currentUsername() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
