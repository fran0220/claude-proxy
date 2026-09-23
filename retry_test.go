package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryerReturnsReadableFinalResponse(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("still unavailable"))
	}))
	defer server.Close()

	retryer := NewRetryer(2, time.Millisecond)
	resp, err := retryer.Do(context.Background(), server.Client(), func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, server.URL, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(body), "still unavailable"; got != want {
		t.Fatalf("response body = %q, want %q", got, want)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestRetryAfterIsCapped(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Retry-After": []string{"3600"}}}
	if got := NewRetryer(2, time.Second).retryDelay(resp, 0); got != maxRetryAfter {
		t.Fatalf("retry delay = %s, want %s", got, maxRetryAfter)
	}

	resp.Header.Set("Retry-After", "5")
	if got := NewRetryer(2, time.Second).retryDelay(resp, 0); got != 5*time.Second {
		t.Fatalf("retry delay = %s, want 5s", got)
	}
}
