package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamResponsePassthroughPreservesTrailingUsageFrame(t *testing.T) {
	input := "event: message_delta\r\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}"
	recorder := httptest.NewRecorder()
	handler := &ClaudeHandler{}

	usage, err := handler.streamResponsePassthrough(recorder, strings.NewReader(input), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := recorder.Body.String(); got != input {
		t.Fatalf("stream body = %q, want exact input %q", got, input)
	}
	if usage.OutputTokens != 7 {
		t.Fatalf("output tokens = %d, want 7", usage.OutputTokens)
	}
}
