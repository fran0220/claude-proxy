package main

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	RouteLocal  = "local"
	RouteAPIKey = "apikey"

	AuthBearer  = "bearer"
	AuthXAPIKey = "x-api-key"

	AuthSourceClaudeCode = "claude-code"
)

type ProviderAuth struct {
	Token    string
	AuthType string
	Source   string
	Expires  time.Time
	BaseURL  string
	Error    error
}

func (a *ProviderAuth) Valid() bool {
	return a != nil && a.Token != "" && a.Error == nil
}

type ClaudeAuthResolver struct {
	cfg      *Config
	tokenMgr *TokenManager
}

func NewClaudeAuthResolver(cfg *Config, tokenMgr *TokenManager) *ClaudeAuthResolver {
	return &ClaudeAuthResolver{cfg: cfg, tokenMgr: tokenMgr}
}

func (ar *ClaudeAuthResolver) Resolve(ctx context.Context, model string) (*ProviderAuth, string) {
	route := ar.cfg.ModelRoute(model)
	auth := ar.ResolveRoute(ctx, route)
	if auth.Valid() {
		return auth, route
	}

	if route == RouteLocal {
		log.Warnf("[AUTH] local Claude auth unavailable for %s (%v), trying API key", model, auth.Error)
		fallback := ar.ResolveRoute(ctx, RouteAPIKey)
		if fallback.Valid() {
			return fallback, RouteAPIKey
		}
		return &ProviderAuth{Error: fmt.Errorf("local auth failed: %v; API key fallback failed: %v", auth.Error, fallback.Error)}, route
	}

	return auth, route
}

func (ar *ClaudeAuthResolver) ResolveRoute(ctx context.Context, route string) *ProviderAuth {
	switch route {
	case RouteLocal:
		return ar.resolveLocal(ctx)
	case RouteAPIKey:
		return ar.resolveAPIKey()
	default:
		return &ProviderAuth{Error: fmt.Errorf("invalid route: %s", route)}
	}
}

func (ar *ClaudeAuthResolver) ResolveDiscoveryAuth(ctx context.Context, preferredRoute string) (*ProviderAuth, string) {
	if preferredRoute != "" {
		auth := ar.ResolveRoute(ctx, preferredRoute)
		return auth, preferredRoute
	}

	if ar.cfg.ModelRoute(DefaultStandardClaudeModel) == RouteAPIKey {
		if auth := ar.ResolveRoute(ctx, RouteAPIKey); auth.Valid() {
			return auth, RouteAPIKey
		}
	}
	if auth := ar.ResolveRoute(ctx, RouteLocal); auth.Valid() {
		return auth, RouteLocal
	}
	auth := ar.ResolveRoute(ctx, RouteAPIKey)
	return auth, RouteAPIKey
}

func (ar *ClaudeAuthResolver) resolveLocal(ctx context.Context) *ProviderAuth {
	if ar.tokenMgr == nil {
		return &ProviderAuth{Error: fmt.Errorf("Claude token manager is not initialized"), Source: AuthSourceClaudeCode}
	}
	token, err := ar.tokenMgr.GetAccessToken(ctx)
	if err != nil {
		return &ProviderAuth{Error: err, Source: AuthSourceClaudeCode}
	}
	status := ar.tokenMgr.Status()
	return &ProviderAuth{
		Token:    token,
		AuthType: AuthBearer,
		Source:   AuthSourceClaudeCode,
		Expires:  time.Now().Add(status.ExpiresIn),
	}
}

func (ar *ClaudeAuthResolver) resolveAPIKey() *ProviderAuth {
	entry, ok := ar.cfg.PreferredAPIKey()
	if !ok {
		return &ProviderAuth{Error: fmt.Errorf("Anthropic API key is not configured"), Source: "api-key"}
	}
	return &ProviderAuth{Token: entry.APIKey, AuthType: AuthXAPIKey, Source: "api-key", BaseURL: entry.BaseURL}
}

func (ar *ClaudeAuthResolver) AuthStatus() map[string]any {
	status := map[string]any{
		"local_source":     AuthSourceClaudeCode,
		"apikey_available": len(ar.cfg.AllAPIKeys()) > 0,
	}
	if ar.tokenMgr == nil {
		status["local_available"] = false
		status["local_error"] = "token manager not initialized"
		return status
	}
	ts := ar.tokenMgr.Status()
	status["local_available"] = ts.Valid
	if ts.Valid {
		status["local_expires_in"] = ts.ExpiresIn.Round(time.Second).String()
	}
	if ts.Error != nil {
		status["local_error"] = ts.Error.Error()
	}
	return status
}
