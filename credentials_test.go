package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMergeClaudeCredentialsPreservesUnknownFields(t *testing.T) {
	existing := []byte(`{
  "pluginCredentials": {"example": "keep"},
  "claudeAiOauth": {
    "accessToken": "old-access",
    "refreshToken": "old-refresh",
    "expiresAt": 1,
    "futureField": "keep"
  }
}`)
	want := &ClaudeCredentials{
		AccessToken:      "new-access",
		RefreshToken:     "new-refresh",
		ExpiresAt:        42,
		Scopes:           []string{"user:inference"},
		SubscriptionType: "max",
		RateLimitTier:    "default_claude_max_5x",
	}

	data, err := mergeClaudeCredentials(existing, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeClaudeCredentials(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("credentials = %#v, want %#v", got, want)
	}

	var extensions map[string]any
	if err := json.Unmarshal(data, &extensions); err != nil {
		t.Fatal(err)
	}
	plugin, ok := extensions["pluginCredentials"].(map[string]any)
	if !ok || plugin["example"] != "keep" {
		t.Fatalf("top-level extension was not preserved: %#v", extensions["pluginCredentials"])
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var oauth map[string]json.RawMessage
	if err := json.Unmarshal(document["claudeAiOauth"], &oauth); err != nil {
		t.Fatal(err)
	}
	if string(oauth["futureField"]) != `"keep"` {
		t.Fatalf("OAuth extension was not preserved: %s", oauth["futureField"])
	}
}

func TestDecodeClaudeCredentialsRejectsInvalidDocuments(t *testing.T) {
	for _, data := range []string{
		`not json`,
		`{}`,
		`{"claudeAiOauth":{}}`,
	} {
		if _, err := decodeClaudeCredentials([]byte(data)); err == nil {
			t.Fatalf("expected %q to fail", data)
		}
	}
}
