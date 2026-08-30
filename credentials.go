package main

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type ClaudeCredentials struct {
	AccessToken      string   `json:"accessToken"`
	RefreshToken     string   `json:"refreshToken"`
	ExpiresAt        int64    `json:"expiresAt"` // Unix milliseconds
	Scopes           []string `json:"scopes"`
	SubscriptionType string   `json:"subscriptionType"`
	RateLimitTier    string   `json:"rateLimitTier"`
}

func decodeClaudeCredentials(data []byte) (*ClaudeCredentials, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse credentials JSON: %w", err)
	}
	raw, ok := document["claudeAiOauth"]
	if !ok {
		return nil, fmt.Errorf("credentials missing claudeAiOauth field")
	}
	var credentials ClaudeCredentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return nil, fmt.Errorf("parse claudeAiOauth credentials: %w", err)
	}
	if credentials.AccessToken == "" {
		return nil, fmt.Errorf("credentials have empty access token")
	}
	return &credentials, nil
}

func mergeClaudeCredentials(existing []byte, credentials *ClaudeCredentials) ([]byte, error) {
	if credentials == nil || credentials.AccessToken == "" {
		return nil, fmt.Errorf("refusing to write empty credentials")
	}

	document := make(map[string]json.RawMessage)
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &document); err != nil {
			return nil, fmt.Errorf("parse existing credentials JSON: %w", err)
		}
		if document == nil {
			document = make(map[string]json.RawMessage)
		}
	}
	oauth := make(map[string]json.RawMessage)
	if raw := document["claudeAiOauth"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &oauth); err != nil {
			return nil, fmt.Errorf("parse existing claudeAiOauth credentials: %w", err)
		}
		if oauth == nil {
			oauth = make(map[string]json.RawMessage)
		}
	}

	fields := map[string]any{
		"accessToken":      credentials.AccessToken,
		"refreshToken":     credentials.RefreshToken,
		"expiresAt":        credentials.ExpiresAt,
		"scopes":           credentials.Scopes,
		"subscriptionType": credentials.SubscriptionType,
		"rateLimitTier":    credentials.RateLimitTier,
	}
	for name, value := range fields {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal credential field %s: %w", name, err)
		}
		oauth[name] = raw
	}
	rawOAuth, err := json.Marshal(oauth)
	if err != nil {
		return nil, fmt.Errorf("marshal claudeAiOauth credentials: %w", err)
	}
	document["claudeAiOauth"] = rawOAuth

	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal credentials document: %w", err)
	}
	return append(data, '\n'), nil
}
