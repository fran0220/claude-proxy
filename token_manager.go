package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

const (
	anthropicTokenURL  = "https://api.anthropic.com/v1/oauth/token"
	anthropicClientID  = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	tokenRefreshMargin = 5 * time.Minute
)

// TokenManager manages the lifecycle of Claude OAuth tokens.
// It reads from Keychain on startup and automatically refreshes before expiry.
type TokenManager struct {
	mu           sync.RWMutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	lastRefresh  time.Time
	lastError    error
	// extras carries non-token fields (scopes, subscriptionType, rateLimitTier)
	// captured from the keychain entry at load time so we can preserve them
	// when writing rotated tokens back via WriteClaudeKeychainCredentials.
	extras     KeychainCredentials
	sfGroup    singleflight.Group
	httpClient *http.Client
}

// TokenStatus is a snapshot of token state for UI display.
type TokenStatus struct {
	Valid     bool
	ExpiresIn time.Duration
	Error     error
}

func NewTokenManager() *TokenManager {
	tm := &TokenManager{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}

	// Load initial credentials from Keychain
	if err := tm.loadFromKeychain(); err != nil {
		log.Warnf("failed to load OAuth token from Keychain: %v", err)
		tm.lastError = err
	} else {
		log.Infof("loaded OAuth token from Keychain (expires in %s)", time.Until(tm.expiresAt).Round(time.Second))
	}

	// Start background refresh loop
	go tm.refreshLoop()

	return tm
}

// GetAccessToken returns a valid access token, refreshing if necessary.
func (tm *TokenManager) GetAccessToken(ctx context.Context) (string, error) {
	tm.mu.RLock()
	token := tm.accessToken
	expiresAt := tm.expiresAt
	tm.mu.RUnlock()

	if token != "" && time.Now().Before(expiresAt.Add(-tokenRefreshMargin)) {
		return token, nil
	}

	// Token expired or about to expire — refresh
	if err := tm.refresh(ctx); err != nil {
		// If refresh fails but token is still technically valid, use it
		if token != "" && time.Now().Before(expiresAt) {
			log.Warnf("token refresh failed but token still valid: %v", err)
			return token, nil
		}
		return "", fmt.Errorf("token expired and refresh failed: %w", err)
	}

	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.accessToken, nil
}

func (tm *TokenManager) SubscriptionType() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.extras.SubscriptionType
}

func (tm *TokenManager) RateLimitTier() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.extras.RateLimitTier
}

// Status returns a snapshot for UI display.
func (tm *TokenManager) Status() TokenStatus {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if tm.lastError != nil && tm.accessToken == "" {
		return TokenStatus{Valid: false, Error: tm.lastError}
	}
	if tm.accessToken == "" {
		return TokenStatus{Valid: false, Error: fmt.Errorf("no token loaded")}
	}

	remaining := time.Until(tm.expiresAt)
	if remaining <= 0 {
		return TokenStatus{Valid: false, Error: fmt.Errorf("token expired")}
	}
	return TokenStatus{Valid: true, ExpiresIn: remaining}
}

func (tm *TokenManager) loadFromKeychain() error {
	creds, err := ReadClaudeKeychainCredentials()
	if err != nil {
		return err
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.accessToken = creds.AccessToken
	tm.refreshToken = creds.RefreshToken
	tm.expiresAt = time.UnixMilli(creds.ExpiresAt)
	tm.extras = KeychainCredentials{
		Scopes:           creds.Scopes,
		SubscriptionType: creds.SubscriptionType,
		RateLimitTier:    creds.RateLimitTier,
	}
	tm.lastError = nil
	return nil
}

func (tm *TokenManager) refresh(ctx context.Context) error {
	_, err, _ := tm.sfGroup.Do("refresh", func() (interface{}, error) {
		tm.mu.RLock()
		refreshToken := tm.refreshToken
		tm.mu.RUnlock()

		if refreshToken == "" {
			return nil, fmt.Errorf("no refresh token available")
		}

		newAccess, newRefresh, expiresIn, err := tm.doRefresh(ctx, refreshToken)
		if err != nil {
			tm.mu.Lock()
			tm.lastError = err
			tm.mu.Unlock()
			return nil, err
		}

		tm.mu.Lock()
		tm.accessToken = newAccess
		if newRefresh != "" {
			tm.refreshToken = newRefresh
		}
		tm.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
		tm.lastRefresh = time.Now()
		tm.lastError = nil
		// Snapshot the values we need outside the lock for the keychain write.
		persist := KeychainCredentials{
			AccessToken:      tm.accessToken,
			RefreshToken:     tm.refreshToken,
			ExpiresAt:        tm.expiresAt.UnixMilli(),
			Scopes:           tm.extras.Scopes,
			SubscriptionType: tm.extras.SubscriptionType,
			RateLimitTier:    tm.extras.RateLimitTier,
		}
		tm.mu.Unlock()

		// Write the rotated credentials back to Keychain so Claude.app and other
		// Claude Code-compatible tools can see the new refresh token. Anthropic rotates the
		// refresh token on every call; without write-back the old one in the
		// keychain becomes invalid_grant for any cold-starting process.
		if err := WriteClaudeKeychainCredentials(&persist); err != nil {
			log.Warnf("failed to write refreshed token back to Keychain: %v", err)
		} else {
			log.Debug("refreshed token written back to Keychain")
		}

		log.Infof("token refreshed, expires in %ds", expiresIn)
		return nil, nil
	})
	return err
}

// doRefresh performs the actual OAuth token refresh HTTP request.
func (tm *TokenManager) doRefresh(ctx context.Context, refreshToken string) (accessToken, newRefreshToken string, expiresIn int, err error) {
	body := map[string]string{
		"client_id":     anthropicClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicTokenURL, strings.NewReader(string(bodyJSON)))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", 0, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", 0, fmt.Errorf("refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", "", 0, fmt.Errorf("parse refresh response: %w", err)
	}

	if result.AccessToken == "" {
		return "", "", 0, fmt.Errorf("refresh returned empty access token")
	}

	return result.AccessToken, result.RefreshToken, result.ExpiresIn, nil
}

// refreshLoop periodically checks and refreshes the token before expiry.
func (tm *TokenManager) refreshLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		tm.mu.RLock()
		expiresAt := tm.expiresAt
		hasRefresh := tm.refreshToken != ""
		tm.mu.RUnlock()

		if !hasRefresh {
			continue
		}

		remaining := time.Until(expiresAt)
		if remaining < tokenRefreshMargin {
			log.Info("token approaching expiry, refreshing...")
			if err := tm.refresh(context.Background()); err != nil {
				log.Errorf("background token refresh failed: %v", err)
				// Refresh token may be stale — try reloading from Keychain
				log.Info("reloading token from Keychain...")
				if err2 := tm.loadFromKeychain(); err2 != nil {
					log.Errorf("keychain reload also failed: %v", err2)
				} else {
					log.Info("token reloaded from Keychain")
				}
			}
		}
	}
}
