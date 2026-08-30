package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestTokenMatchesSupportedHeaders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		value  string
	}{
		{name: "x-api-key", header: "x-api-key", value: "secret"},
		{name: "bearer", header: "Authorization", value: "Bearer secret"},
		{name: "case-insensitive bearer", header: "Authorization", value: "bearer secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			req.Header.Set(tc.header, tc.value)
			if !requestTokenMatches(req, "secret") {
				t.Fatal("expected token to match")
			}
		})
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("x-api-key", "wrong")
	req.Header.Set("Authorization", "Bearer secret")
	if !requestTokenMatches(req, "secret") {
		t.Fatal("expected either supported header to authenticate")
	}
}

func TestClaudeRouterRequiresAccessToken(t *testing.T) {
	router := &ClaudeRouter{cfg: &Config{Security: SecurityConfig{AccessToken: "proxy-secret"}}}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(rec.Body.String(), "authentication_error") {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestAdminAPIsRequireSeparateToken(t *testing.T) {
	server := &AdminServer{cfg: &Config{Security: SecurityConfig{AdminToken: "admin-secret"}}}
	handler := server.requireAdminToken(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name   string
		path   string
		token  string
		status int
	}{
		{name: "missing", path: "/api/status", status: http.StatusUnauthorized},
		{name: "wrong", path: "/api/status", token: "wrong", status: http.StatusUnauthorized},
		{name: "valid", path: "/api/status", token: "admin-secret", status: http.StatusNoContent},
		{name: "static assets stay public", path: "/app.js", status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.token != "" {
				req.Header.Set("Authorization", "Bearer "+tc.token)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestConfigCreatesPrivateSecurityTokens(t *testing.T) {
	cfg := defaultConfig()
	if !cfg.normalize() {
		t.Fatal("expected missing security tokens to normalize")
	}
	if !strings.HasPrefix(cfg.Security.AccessToken, "cp_") || !strings.HasPrefix(cfg.Security.AdminToken, "cp_admin_") {
		t.Fatalf("unexpected generated token prefixes: %#v", cfg.Security)
	}
	if cfg.Security.AccessToken == cfg.Security.AdminToken {
		t.Fatal("access and admin tokens must differ")
	}

	cfg.path = filepath.Join(t.TempDir(), "nested", "config.yaml")
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode = %o, want 600", got)
	}
}
