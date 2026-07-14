package main

import "testing"

func TestParseClaudeUsageFromMessageResponse(t *testing.T) {
	data := []byte("{\"usage\":{\"input_tokens\":120,\"output_tokens\":40,\"cache_read_input_tokens\":15,\"cache_creation_input_tokens\":7}}")
	got := ParseClaudeUsage(data)
	if got.InputTokens != 120 {
		t.Fatalf("expected input_tokens=120, got %d", got.InputTokens)
	}
	if got.OutputTokens != 40 {
		t.Fatalf("expected output_tokens=40, got %d", got.OutputTokens)
	}
	if got.CacheReadTokens != 15 {
		t.Fatalf("expected cache_read_tokens=15, got %d", got.CacheReadTokens)
	}
	if got.CacheCreateTokens != 7 {
		t.Fatalf("expected cache_create_tokens=7, got %d", got.CacheCreateTokens)
	}
}

func TestParseClaudeUsageFromCountTokensResponse(t *testing.T) {
	data := []byte("{\"input_tokens\":88}")
	got := ParseClaudeUsage(data)
	if got.InputTokens != 88 {
		t.Fatalf("expected input_tokens=88, got %d", got.InputTokens)
	}
	if got.OutputTokens != 0 {
		t.Fatalf("expected output_tokens=0, got %d", got.OutputTokens)
	}
}
