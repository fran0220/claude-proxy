package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
)

// Retryer handles automatic retry for transient HTTP errors (429, 529).
type Retryer struct {
	maxAttempts  int
	initialDelay time.Duration
}

func NewRetryer(maxAttempts int, initialDelay time.Duration) *Retryer {
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	if initialDelay <= 0 {
		initialDelay = 1 * time.Second
	}
	return &Retryer{maxAttempts: maxAttempts, initialDelay: initialDelay}
}

// Do executes an HTTP request with retry on 429/529.
// The caller provides a factory that creates a fresh request for each attempt
// (since request bodies can only be read once).
// On success (non-retryable status), returns the response for the caller to consume.
func (r *Retryer) Do(ctx context.Context, client *http.Client, newRequest func() (*http.Request, error)) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt < r.maxAttempts; attempt++ {
		req, err := newRequest()
		if err != nil {
			return nil, fmt.Errorf("create request (attempt %d): %w", attempt+1, err)
		}
		req = req.WithContext(ctx)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			// Network error — retry with backoff
			delay := r.backoff(attempt)
			log.Warnf("request error (attempt %d/%d): %v, retrying in %s", attempt+1, r.maxAttempts, err, delay)
			if waitErr := r.wait(ctx, delay); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		if !isRetryableStatus(resp.StatusCode) {
			return resp, nil
		}

		// Retryable status — read body for debugging, then retry
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		delay := r.retryDelay(resp, attempt)
		bodyPreview := string(respBody)
		if len(bodyPreview) > 200 {
			bodyPreview = bodyPreview[:200]
		}
		log.Warnf("retryable status %d (attempt %d/%d): %s — retrying in %s", resp.StatusCode, attempt+1, r.maxAttempts, bodyPreview, delay)
		lastResp = resp

		if waitErr := r.wait(ctx, delay); waitErr != nil {
			return nil, waitErr
		}
	}

	if lastResp != nil {
		return lastResp, nil // Return last retryable response so caller can forward the error
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

func isRetryableStatus(code int) bool {
	return code == 429 || code == 529 || code == 502 || code == 503
}

func (r *Retryer) backoff(attempt int) time.Duration {
	// Short backoff: 0.5s, 1s, 1.5s, 2s, 2.5s — total ~7.5s for 5 attempts
	// Claude Code CLI uses ~0s delay. We add a small delay to avoid hammering.
	d := 500*time.Millisecond + time.Duration(attempt)*500*time.Millisecond
	if d > 3*time.Second {
		d = 3 * time.Second
	}
	return d
}

func (r *Retryer) retryDelay(resp *http.Response, attempt int) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if seconds, err := strconv.Atoi(ra); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return r.backoff(attempt)
}

func (r *Retryer) wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
