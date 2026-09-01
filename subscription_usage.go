package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	oauthUsageURL      = "https://api.anthropic.com/api/oauth/usage"
	oauthUsageCacheTTL = 3 * time.Minute
	oauthUsageBackoff  = 10 * time.Minute
)

type UsageWindow struct {
	Utilization      *float64 `json:"utilization"`
	ResetsAt         *string  `json:"resets_at"`
	LimitDollars     *float64 `json:"limit_dollars"`
	UsedDollars      *float64 `json:"used_dollars"`
	RemainingDollars *float64 `json:"remaining_dollars"`
}

type ExtraUsage struct {
	IsEnabled          bool     `json:"is_enabled"`
	MonthlyLimit       *float64 `json:"monthly_limit"`
	UsedCredits        *float64 `json:"used_credits"`
	Utilization        *float64 `json:"utilization"`
	Currency           string   `json:"currency,omitempty"`
	DecimalPlaces      int      `json:"decimal_places,omitempty"`
	DisabledReason     string   `json:"disabled_reason,omitempty"`
	UserDisabled       bool     `json:"user_disabled"`
	SpendLimitReached  bool     `json:"spend_limit_reached"`
	CreditsEverEnabled bool     `json:"credits_ever_enabled"`
}

type LimitScopeModel struct {
	ID          *string `json:"id"`
	DisplayName string  `json:"display_name"`
}

type LimitScope struct {
	Model   *LimitScopeModel `json:"model"`
	Surface *string          `json:"surface"`
}

type OAuthLimit struct {
	Kind     string      `json:"kind"`
	Group    string      `json:"group"`
	Percent  float64     `json:"percent"`
	Severity string      `json:"severity"`
	ResetsAt *string     `json:"resets_at"`
	Scope    *LimitScope `json:"scope"`
	IsActive bool        `json:"is_active"`
}

type OAuthUsagePayload struct {
	FiveHour          *UsageWindow    `json:"five_hour"`
	SevenDay          *UsageWindow    `json:"seven_day"`
	SevenDayOAuthApps *UsageWindow    `json:"seven_day_oauth_apps"`
	SevenDayOpus      *UsageWindow    `json:"seven_day_opus"`
	SevenDaySonnet    *UsageWindow    `json:"seven_day_sonnet"`
	SevenDayCowork    *UsageWindow    `json:"seven_day_cowork"`
	SevenDayOmelette  *UsageWindow    `json:"seven_day_omelette"`
	ExtraUsage        *ExtraUsage     `json:"extra_usage"`
	Limits            []OAuthLimit    `json:"limits"`
	RawSpend          json.RawMessage `json:"spend,omitempty"`
}

type SubscriptionUsage struct {
	Available     bool         `json:"available"`
	FetchedAt     *time.Time   `json:"fetched_at,omitempty"`
	Stale         bool         `json:"stale"`
	Error         string       `json:"error,omitempty"`
	Subscription  string       `json:"subscription,omitempty"`
	RateLimitTier string       `json:"rate_limit_tier,omitempty"`
	Session       *UsageWindow `json:"session,omitempty"`
	Weekly        *UsageWindow `json:"weekly,omitempty"`
	WeeklyOpus    *UsageWindow `json:"weekly_opus,omitempty"`
	WeeklySonnet  *UsageWindow `json:"weekly_sonnet,omitempty"`
	Limits        []OAuthLimit `json:"limits,omitempty"`
	ExtraUsage    *ExtraUsage  `json:"extra_usage,omitempty"`
}

type SubscriptionUsageClient struct {
	tokenMgr *TokenManager
	client   *http.Client

	mu        sync.Mutex
	cached    SubscriptionUsage
	fetchedAt time.Time
	nextTry   time.Time
}

func NewSubscriptionUsageClient(tokenMgr *TokenManager) *SubscriptionUsageClient {
	return &SubscriptionUsageClient{
		tokenMgr: tokenMgr,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *SubscriptionUsageClient) Get(ctx context.Context) SubscriptionUsage {
	if c == nil {
		return SubscriptionUsage{Available: false, Error: "subscription usage client not initialized"}
	}
	now := time.Now()
	c.mu.Lock()
	fresh := !c.fetchedAt.IsZero() && now.Sub(c.fetchedAt) < oauthUsageCacheTTL
	cached := c.cached
	nextTry := c.nextTry
	c.mu.Unlock()
	if fresh {
		return cached
	}
	if !nextTry.IsZero() && now.Before(nextTry) && cached.Available {
		cached.Stale = true
		return cached
	}
	updated, err := c.refresh(ctx)
	if err != nil {
		if cached.Available {
			cached.Stale = true
			cached.Error = err.Error()
			return cached
		}
		return SubscriptionUsage{Available: false, Error: err.Error(), Subscription: c.subscriptionMeta()}
	}
	return updated
}

func (c *SubscriptionUsageClient) refresh(ctx context.Context) (SubscriptionUsage, error) {
	if c.tokenMgr == nil {
		return SubscriptionUsage{}, fmt.Errorf("local Claude token is not available")
	}
	token, err := c.tokenMgr.GetAccessToken(ctx)
	if err != nil {
		return SubscriptionUsage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthUsageURL, nil)
	if err != nil {
		return SubscriptionUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("User-Agent", "claude-code/"+claudeCodeVersion)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return SubscriptionUsage{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SubscriptionUsage{}, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		c.mu.Lock()
		c.nextTry = time.Now().Add(oauthUsageBackoff)
		c.mu.Unlock()
		return SubscriptionUsage{}, fmt.Errorf("oauth usage rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return SubscriptionUsage{}, fmt.Errorf("oauth usage returned %s: %s", resp.Status, truncateStr(string(body), 240))
	}
	report, err := parseOAuthUsage(body)
	if err != nil {
		return SubscriptionUsage{}, err
	}
	now := time.Now().UTC()
	report.Available = true
	report.FetchedAt = &now
	report.Subscription, report.RateLimitTier = c.subscriptionMetaPair()
	c.mu.Lock()
	c.cached = report
	c.fetchedAt = time.Now()
	c.nextTry = time.Time{}
	c.mu.Unlock()
	return report, nil
}

func (c *SubscriptionUsageClient) subscriptionMeta() string {
	sub, _ := c.subscriptionMetaPair()
	return sub
}

func (c *SubscriptionUsageClient) subscriptionMetaPair() (string, string) {
	if c.tokenMgr == nil {
		return "", ""
	}
	return c.tokenMgr.SubscriptionType(), c.tokenMgr.RateLimitTier()
}

func parseOAuthUsage(data []byte) (SubscriptionUsage, error) {
	var raw OAuthUsagePayload
	if err := json.Unmarshal(data, &raw); err != nil {
		return SubscriptionUsage{}, fmt.Errorf("decode oauth usage: %w", err)
	}
	report := SubscriptionUsage{
		Session:      raw.FiveHour,
		Weekly:       raw.SevenDay,
		WeeklyOpus:   raw.SevenDayOpus,
		WeeklySonnet: raw.SevenDaySonnet,
		Limits:       raw.Limits,
		ExtraUsage:   raw.ExtraUsage,
	}
	if report.Limits == nil {
		report.Limits = []OAuthLimit{}
	}
	return report, nil
}
