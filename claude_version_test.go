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

	body := injectClaudeCodeIdentity([]byte(`{"messages":[]}`), generateClaudeUserID())
	billing := gjson.GetBytes(body, "system.0.text").String()
	if !strings.Contains(billing, "cc_version="+claudeCodeVersion+".") {
		t.Fatalf("billing identity does not use Claude Code %s: %s", claudeCodeVersion, billing)
	}
}
