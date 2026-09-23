package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestClaudeCodeVersionIsConsistentAcrossIdentityHeaders(t *testing.T) {
	upstream, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyDirectClaudeHeaders(upstream, original, &ProviderAuth{Token: "token", AuthType: AuthBearer}, false)
	if got, want := upstream.UserAgent(), "claude-cli/"+claudeCodeVersion+" (external, cli)"; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
	if got, want := upstream.Header.Get("X-Stainless-Runtime-Version"), "v26.3.0"; got != want {
		t.Fatalf("X-Stainless-Runtime-Version = %q, want %q", got, want)
	}
	if got, want := upstream.Header.Get("X-Stainless-Package-Version"), "0.112.1"; got != want {
		t.Fatalf("X-Stainless-Package-Version = %q, want %q", got, want)
	}
	for _, beta := range []string{
		"thinking-token-count-2026-05-13",
		"effort-2025-11-24",
		"extended-cache-ttl-2025-04-11",
	} {
		if !strings.Contains(upstream.Header.Get("Anthropic-Beta"), beta) {
			t.Fatalf("Anthropic-Beta does not contain %q: %s", beta, upstream.Header.Get("Anthropic-Beta"))
		}
	}

	body := injectClaudeCodeIdentity([]byte(`{"messages":[]}`), generateClaudeUserID())
	billing := gjson.GetBytes(body, "system.0.text").String()
	if !strings.Contains(billing, "cc_version="+claudeCodeVersion+".") {
		t.Fatalf("billing identity does not use Claude Code %s: %s", claudeCodeVersion, billing)
	}
	if strings.Contains(billing, "cch=") {
		t.Fatalf("billing identity contains removed cch field: %s", billing)
	}
	if got := gjson.GetBytes(body, "system.1.cache_control.ttl").String(); got != "1h" {
		t.Fatalf("agent identity cache ttl = %q, want 1h", got)
	}
}

func TestClaudeCodeHeadersForwardGatewayHints(t *testing.T) {
	original, err := http.NewRequest(http.MethodPost, "/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	original.Header.Set("X-Claude-Code-Session-Id", "session-id")
	original.Header.Set("X-Claude-Code-Request-Class", "subagent")
	original.Header.Set("X-Claude-Code-Context-Compacted", "true")
	upstream, err := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}

	applyDirectClaudeHeaders(upstream, original, &ProviderAuth{Token: "token", AuthType: AuthBearer}, true)

	for header, want := range map[string]string{
		"X-Claude-Code-Session-Id":        "session-id",
		"X-Claude-Code-Request-Class":     "subagent",
		"X-Claude-Code-Context-Compacted": "true",
	} {
		if got := upstream.Header.Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestClaudeCodeIdentityPreservesClientMetadata(t *testing.T) {
	want := `{"device_id":"client-device","account_uuid":"account","session_id":"client-session"}`
	body := injectClaudeCodeIdentity([]byte(`{"messages":[],"metadata":{"user_id":"`+strings.ReplaceAll(want, `"`, `\"`)+`"}}`), generateClaudeUserID())
	if got := gjson.GetBytes(body, "metadata.user_id").String(); got != want {
		t.Fatalf("metadata.user_id = %q, want %q", got, want)
	}
	if !isValidClaudeUserID(gjson.GetBytes(body, "metadata.user_id").String()) {
		t.Fatal("preserved current Claude Code user_id is invalid")
	}
}
