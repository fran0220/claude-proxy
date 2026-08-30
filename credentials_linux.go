//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func claudeCredentialsPath() (string, error) {
	dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home directory: %w", err)
		}
		dir = filepath.Join(home, ".claude")
	} else if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand CLAUDE_CONFIG_DIR: %w", err)
		}
		if dir == "~" {
			dir = home
		} else {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return filepath.Join(dir, ".credentials.json"), nil
}

func ReadClaudeCredentials() (*ClaudeCredentials, error) {
	path, err := claudeCredentialsPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read Claude Code credentials %s: %w", path, err)
	}
	credentials, err := decodeClaudeCredentials(data)
	if err != nil {
		return nil, fmt.Errorf("read Claude Code credentials %s: %w", path, err)
	}
	return credentials, nil
}

func WriteClaudeCredentials(credentials *ClaudeCredentials) error {
	path, err := claudeCredentialsPath()
	if err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing Claude Code credentials %s: %w", path, err)
	}
	data, err := mergeClaudeCredentials(existing, credentials)
	if err != nil {
		return fmt.Errorf("update Claude Code credentials %s: %w", path, err)
	}
	if err := writePrivateFile(path, data); err != nil {
		return fmt.Errorf("write Claude Code credentials %s: %w", path, err)
	}
	return nil
}
