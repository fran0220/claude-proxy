package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseAnthropicPricesIgnoresOtherProviders(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "modelsdev_anthropic.json"))
	if err != nil {
		t.Fatal(err)
	}
	prices, err := parseAnthropicPrices(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(prices) != 5 {
		t.Fatalf("expected 5 anthropic models, got %d", len(prices))
	}
	got := prices["claude-sonnet-4-6"]
	if got.Input != 3 || got.Output != 15 || got.CacheRead != 0.3 || got.CacheWrite != 3.75 {
		t.Fatalf("unexpected sonnet 4.6 price: %+v", got)
	}
	if prices["claude-sonnet-4-6"].Input == 99 {
		t.Fatal("used non-anthropic provider price")
	}
}

func TestPriceCatalogLookupAliases(t *testing.T) {
	cat := NewPriceCatalog(t.TempDir(), nil)
	data, err := os.ReadFile(filepath.Join("testdata", "modelsdev_anthropic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.LoadBytes(data, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"claude-sonnet-4-6":            "claude-sonnet-4-6",
		"anthropic/claude-sonnet-4.6":  "claude-sonnet-4-6",
		"claude-haiku-4-5-20251001":    "claude-haiku-4-5-20251001",
		"claude-haiku-4-5-20260101":    "claude-haiku-4-5",
		"claude-opus-4.8":              "claude-opus-4-8",
	}
	for in, wantID := range cases {
		got, ok := cat.Lookup(in)
		if !ok {
			t.Fatalf("Lookup(%q) missed", in)
		}
		if got.MatchedID != wantID {
			t.Fatalf("Lookup(%q) matched %q, want %q", in, got.MatchedID, wantID)
		}
	}
	if _, ok := cat.Lookup("claude-unknown-model"); ok {
		t.Fatal("expected unknown model to be unpriced")
	}
}

func TestPriceCatalogOverrideAndHourCacheWrite(t *testing.T) {
	cfg := &Config{Pricing: PricingConfig{Overrides: map[string]ModelPrice{
		"claude-sonnet-4-6": {Input: 9, Output: 11, CacheRead: 0.9, CacheWrite: 12},
	}}}
	cat := NewPriceCatalog(t.TempDir(), cfg)
	data, err := os.ReadFile(filepath.Join("testdata", "modelsdev_anthropic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.LoadBytes(data, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, ok := cat.Lookup("claude-sonnet-4-6")
	if !ok || got.Source != PriceSourceOverride || got.Input != 9 {
		t.Fatalf("override not applied: %+v ok=%v", got, ok)
	}
	snap, src := cat.Snapshot("claude-sonnet-4-6", CacheTTL1h)
	if src != PriceSourceOverride {
		t.Fatalf("snapshot source %q", src)
	}
	if snap.CacheWrite != 18 {
		t.Fatalf("1h cache write want 18, got %v", snap.CacheWrite)
	}
}

func TestTokenCostUSD(t *testing.T) {
	price := ModelPrice{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	usage := TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheCreateTokens: 1_000_000}
	got := TokenCostUSD(usage, price)
	if got != 22.05 {
		t.Fatalf("cost=%v want 22.05", got)
	}
}

func TestNormalizeMissingCacheRates(t *testing.T) {
	got := normalizePrice(ModelPrice{Input: 2, Output: 10})
	if got.CacheRead != 0.2 || got.CacheWrite != 2.5 {
		t.Fatalf("derived cache rates: %+v", got)
	}
}
