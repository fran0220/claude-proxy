//go:build !darwin && !linux

package main

import (
	"fmt"
	"runtime"
)

func ReadClaudeCredentials() (*ClaudeCredentials, error) {
	return nil, fmt.Errorf("Claude Code credential storage is not supported on %s", runtime.GOOS)
}

func WriteClaudeCredentials(_ *ClaudeCredentials) error {
	return fmt.Errorf("Claude Code credential storage is not supported on %s", runtime.GOOS)
}
