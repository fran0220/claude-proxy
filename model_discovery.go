package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type TestResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Latency int64  `json:"latency_ms"`
	Route   string `json:"route,omitempty"`
	Source  string `json:"source,omitempty"`
}

type DiscoveredModel struct {
	ID             string         `json:"id"`
	DisplayName    string         `json:"display_name,omitempty"`
	CreatedAt      string         `json:"created_at,omitempty"`
	MaxInputTokens int64          `json:"max_input_tokens,omitempty"`
	MaxTokens      int64          `json:"max_tokens,omitempty"`
	Capabilities   map[string]any `json:"capabilities,omitempty"`
}

func testClaudeCredential(ctx context.Context, auth *ProviderAuth, route string) TestResult {
	if auth == nil || !auth.Valid() {
		msg := "credential is unavailable"
		if auth != nil && auth.Error != nil {
			msg = auth.Error.Error()
		}
		return TestResult{Success: false, Message: msg, Route: route}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	start := time.Now()
	body := "{\"model\":\"claude-haiku-4-5-20251001\",\"max_tokens\":1,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildAnthropicURL(authBaseURL(auth), "/v1/messages"), strings.NewReader(body))
	if err != nil {
		return TestResult{Success: false, Message: err.Error(), Route: route, Source: auth.Source}
	}
	applyModelAPIHeaders(req, auth)

	resp, err := client.Do(req)
	if err != nil {
		return TestResult{Success: false, Message: err.Error(), Latency: time.Since(start).Milliseconds(), Route: route, Source: auth.Source}
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	latency := time.Since(start).Milliseconds()

	if resp.StatusCode == http.StatusOK {
		return TestResult{Success: true, Message: "OK", Latency: latency, Route: route, Source: auth.Source}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return TestResult{Success: true, Message: "OK (rate limited)", Latency: latency, Route: route, Source: auth.Source}
	}
	errMsg := gjson.GetBytes(respBody, "error.message").String()
	if errMsg == "" {
		errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return TestResult{Success: false, Message: errMsg, Latency: latency, Route: route, Source: auth.Source}
}

func discoverClaudeModels(ctx context.Context, auth *ProviderAuth) ([]DiscoveredModel, error) {
	if auth == nil || !auth.Valid() {
		if auth != nil && auth.Error != nil {
			return nil, auth.Error
		}
		return nil, fmt.Errorf("credential is unavailable")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var models []DiscoveredModel
	afterID := ""

	for {
		u, err := url.Parse(buildAnthropicURL(authBaseURL(auth), "/v1/models"))
		if err != nil {
			return nil, err
		}
		q := u.Query()
		q.Set("limit", "1000")
		if afterID != "" {
			q.Set("after_id", afterID)
		}
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		applyModelAPIHeaders(req, auth)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			errMsg := gjson.GetBytes(respBody, "error.message").String()
			if errMsg == "" {
				errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
			}
			return nil, fmt.Errorf("list models failed: %s", errMsg)
		}

		var result struct {
			Data []struct {
				ID             string         `json:"id"`
				DisplayName    string         `json:"display_name"`
				CreatedAt      string         `json:"created_at"`
				MaxInputTokens int64          `json:"max_input_tokens"`
				MaxTokens      int64          `json:"max_tokens"`
				Capabilities   map[string]any `json:"capabilities"`
			} `json:"data"`
			LastID  string `json:"last_id"`
			HasMore bool   `json:"has_more"`
		}
		if err := json.Unmarshal(respBody, &result); err != nil {
			return nil, err
		}
		for _, m := range result.Data {
			if m.ID == "" {
				continue
			}
			models = append(models, DiscoveredModel{
				ID:             m.ID,
				DisplayName:    m.DisplayName,
				CreatedAt:      m.CreatedAt,
				MaxInputTokens: m.MaxInputTokens,
				MaxTokens:      m.MaxTokens,
				Capabilities:   m.Capabilities,
			})
		}
		if !result.HasMore || result.LastID == "" {
			break
		}
		afterID = result.LastID
	}

	return models, nil
}

func applyModelAPIHeaders(req *http.Request, auth *ProviderAuth) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	if auth.AuthType == AuthXAPIKey {
		req.Header.Set("x-api-key", auth.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	}
}

func authBaseURL(auth *ProviderAuth) string {
	if auth != nil && auth.BaseURL != "" {
		return auth.BaseURL
	}
	return anthropicAPIBase
}

func buildAnthropicURL(base, path string) string {
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = anthropicAPIBase
	}
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return base + path
}
