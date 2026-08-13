package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testPriceCatalog(t *testing.T) *PriceCatalog {
	t.Helper()
	cat := NewPriceCatalog(t.TempDir(), nil)
	data, err := os.ReadFile(filepath.Join("testdata", "modelsdev_anthropic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.LoadBytes(data, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestUsageReportPricesAndWindows(t *testing.T) {
	dir := t.TempDir()
	cat := testPriceCatalog(t)
	logger := NewRequestLoggerWithPrices(dir, cat)
	defer logger.Close()

	now := time.Date(2026, 8, 13, 18, 30, 0, 0, time.Local)
	id := logger.LogRequestWithCache("claude-sonnet-4-6", "anthropic", "apikey/api-key", "/v1/messages", now.Add(-2*time.Hour), "")
	logger.RecordResultID(id, "claude-sonnet-4-6", 200, TokenUsage{
		InputTokens:       1_000_000,
		OutputTokens:      1_000_000,
		CacheReadTokens:   1_000_000,
		CacheCreateTokens: 1_000_000,
	}, 0, "", "", "")

	oldID := logger.LogRequest("claude-opus-4-8", "anthropic", "local/keychain", "/v1/messages", now.Add(-48*time.Hour))
	logger.RecordResultID(oldID, "claude-opus-4-8", 200, TokenUsage{InputTokens: 1_000_000, OutputTokens: 1_000_000}, 0, "", "", "")

	countID := logger.LogRequest("claude-sonnet-4-6", "anthropic", "apikey/api-key", "/v1/messages/count_tokens", now.Add(-time.Hour))
	logger.RecordResultID(countID, "claude-sonnet-4-6", 200, TokenUsage{InputTokens: 88}, 0, "", "", "")

	unknownID := logger.LogRequest("claude-unknown-x", "anthropic", "apikey/api-key", "/v1/messages", now.Add(-30*time.Minute))
	logger.RecordResultID(unknownID, "claude-unknown-x", 200, TokenUsage{InputTokens: 1000, OutputTokens: 1000}, 0, "", "", "")

	filter, rng := applyUsageRange(StatsFilter{Provider: "anthropic"}, "24h", now)
	report := logger.GetUsage(filter, rng, time.Local)
	if report.Range != "24h" || report.Granularity != "hour" {
		t.Fatalf("range=%s granularity=%s", report.Range, report.Granularity)
	}
	if report.Totals.Requests != 3 {
		t.Fatalf("24h requests=%d want 3", report.Totals.Requests)
	}
	if report.Totals.CostUSD == nil || *report.Totals.CostUSD != 22.05 {
		t.Fatalf("24h cost=%v want 22.05", report.Totals.CostUSD)
	}
	if report.Totals.CostAPIKeyUSD == nil || *report.Totals.CostAPIKeyUSD != 22.05 {
		t.Fatalf("apikey cost=%v", report.Totals.CostAPIKeyUSD)
	}
	if len(report.Totals.UnpricedModels) != 1 || report.Totals.UnpricedModels[0] != "claude-unknown-x" {
		t.Fatalf("unpriced=%v", report.Totals.UnpricedModels)
	}

	allFilter, allRange := applyUsageRange(StatsFilter{Provider: "anthropic"}, "all", now)
	all := logger.GetUsage(allFilter, allRange, time.Local)
	if all.Totals.Requests != 4 {
		t.Fatalf("all requests=%d want 4", all.Totals.Requests)
	}
	if all.Totals.CostUSD == nil || *all.Totals.CostUSD != 52.05 {
		t.Fatalf("all cost=%v want 52.05", all.Totals.CostUSD)
	}

	logs := logger.GetLogsFiltered(10, 0, "anthropic", "", 0)
	var sonnet *RequestLog
	for i := range logs {
		if logs[i].Model == "claude-sonnet-4-6" && logs[i].Path == "/v1/messages" {
			sonnet = &logs[i]
			break
		}
	}
	if sonnet == nil || sonnet.Priced != "snapshot" || sonnet.CostUSD == nil || *sonnet.CostUSD != 22.05 {
		t.Fatalf("snapshot log=%+v", sonnet)
	}
}

func TestApplyUsageRange(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	filter, rng := applyUsageRange(StatsFilter{}, "7d", now)
	if rng != UsageRange7d {
		t.Fatalf("range=%s", rng)
	}
	if !filter.Since.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("since=%s", filter.Since)
	}
	filter, rng = applyUsageRange(StatsFilter{}, "all", now)
	if rng != UsageRangeAll || !filter.Since.IsZero() {
		t.Fatalf("all range=%s since=%s", rng, filter.Since)
	}
}
