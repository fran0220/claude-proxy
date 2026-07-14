package main

import "github.com/tidwall/gjson"

// ParseClaudeUsage extracts token usage from a Claude API response or SSE data event.
// Claude format: {"usage":{"input_tokens":N,"output_tokens":N,"cache_read_input_tokens":N,"cache_creation_input_tokens":N}}
func ParseClaudeUsage(data []byte) TokenUsage {
	usage := gjson.GetBytes(data, "usage")
	if !usage.Exists() {
		root := gjson.ParseBytes(data)
		if root.Get("input_tokens").Exists() || root.Get("output_tokens").Exists() {
			return TokenUsage{
				InputTokens:       root.Get("input_tokens").Int(),
				OutputTokens:      root.Get("output_tokens").Int(),
				CacheReadTokens:   root.Get("cache_read_input_tokens").Int(),
				CacheCreateTokens: root.Get("cache_creation_input_tokens").Int(),
			}
		}
		return TokenUsage{}
	}
	return TokenUsage{
		InputTokens:       usage.Get("input_tokens").Int(),
		OutputTokens:      usage.Get("output_tokens").Int(),
		CacheReadTokens:   usage.Get("cache_read_input_tokens").Int(),
		CacheCreateTokens: usage.Get("cache_creation_input_tokens").Int(),
	}
}
