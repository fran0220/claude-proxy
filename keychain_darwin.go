package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

const keychainService = "Claude Code-credentials"

// KeychainCredentials represents the Claude Code OAuth credentials stored in macOS Keychain.
type KeychainCredentials struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // Unix milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

// keychainWrapper is the outer JSON structure stored in Keychain.
type keychainWrapper struct {
	ClaudeAiOauth *KeychainCredentials `json:"claudeAiOauth"`
}

// ReadClaudeKeychainCredentials reads Claude Code OAuth credentials from macOS Keychain.
// Uses the `security` CLI tool to avoid CGO dependencies.
func ReadClaudeKeychainCredentials() (*KeychainCredentials, error) {
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

	var wrapper keychainWrapper
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, fmt.Errorf("parse keychain JSON: %w", err)
	}

	if wrapper.ClaudeAiOauth == nil {
		return nil, fmt.Errorf("keychain entry missing claudeAiOauth field")
	}

	creds := wrapper.ClaudeAiOauth
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("keychain entry has empty access token")
	}

	return creds, nil
}

// WriteClaudeKeychainCredentials persists Claude Code OAuth credentials back
// to the macOS Keychain entry. Anthropic rotates refresh tokens on every
// refresh; writing back lets Claude.app and other Claude Code-compatible tools
// share the latest rotated refresh token via the keychain instead of stranding
// it in the memory of whichever process refreshed first.
//
// Uses `security add-generic-password -U` (update if exists) to avoid CGO.
func WriteClaudeKeychainCredentials(creds *KeychainCredentials) error {
	if creds == nil || creds.AccessToken == "" {
		return fmt.Errorf("refusing to write empty credentials")
	}
	account, err := currentUsername()
	if err != nil {
		return fmt.Errorf("get current user: %w", err)
	}
	payload, err := json.Marshal(keychainWrapper{ClaudeAiOauth: creds})
	if err != nil {
		return fmt.Errorf("marshal keychain payload: %w", err)
	}
	cmd := exec.Command("security", "add-generic-password",
		"-U",
		"-s", keychainService,
		"-a", account,
		"-w", string(payload),
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
