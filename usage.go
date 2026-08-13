package main

import (
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type UsageRange string

const (
	UsageRange24h UsageRange = "24h"
	UsageRange7d  UsageRange = "7d"
	UsageRange30d UsageRange = "30d"
	UsageRangeAll UsageRange = "all"
)

type CostBreakdown struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Total      float64 `json:"total"`
}

type UsageTokenTotals struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CacheRead   int64 `json:"cache_read"`
	CacheCreate int64 `json:"cache_create"`
	Total       int64 `json:"total"`
}

type UsageTotals struct {
	Requests        int64             `json:"requests"`
	Errors          int64             `json:"errors"`
	Tokens          UsageTokenTotals  `json:"tokens"`
	CostUSD         *float64          `json:"cost_usd"`
	CostByComponent CostBreakdown     `json:"cost_by_component"`
	CostLocalUSD    *float64          `json:"cost_local_usd"`
	CostAPIKeyUSD   *float64          `json:"cost_apikey_usd"`
	UnpricedTokens  int64             `json:"unpriced_tokens"`
	UnpricedModels  []string          `json:"unpriced_models,omitempty"`
}

type UsageModelRow struct {
	Model          string            `json:"model"`
	Requests       int64             `json:"requests"`
	Errors         int64             `json:"errors"`
	Tokens         UsageTokenTotals  `json:"tokens"`
	CostUSD        *float64          `json:"cost_usd"`
	UnpricedTokens int64             `json:"unpriced_tokens,omitempty"`
	Priced         string            `json:"priced"`
}

type UsageRouteRow struct {
	Route          string           `json:"route"`
	Requests       int64            `json:"requests"`
	Tokens         UsageTokenTotals `json:"tokens"`
	CostUSD        *float64         `json:"cost_usd"`
	Equivalent     bool             `json:"equivalent,omitempty"`
	UnpricedTokens int64            `json:"unpriced_tokens,omitempty"`
}

type UsageBucket struct {
	Bucket         string   `json:"bucket"`
	Requests       int64    `json:"requests"`
	Tokens         int64    `json:"tokens"`
	CostUSD        *float64 `json:"cost_usd"`
	UnpricedTokens int64    `json:"unpriced_tokens,omitempty"`
}

type UsageReport struct {
	Range     string          `json:"range"`
	Since     *time.Time      `json:"since,omitempty"`
	Until     time.Time       `json:"until"`
	Timezone  string          `json:"timezone"`
	Granularity string        `json:"granularity"`
	Pricing   PriceStatus     `json:"pricing"`
	Totals    UsageTotals     `json:"totals"`
	ByModel   []UsageModelRow `json:"by_model"`
	ByRoute   []UsageRouteRow `json:"by_route"`
	Series    []UsageBucket   `json:"series"`
}

func parseUsageRange(raw string) UsageRange {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "24h", "1d", "day":
		return UsageRange24h
	case "7d", "week":
		return UsageRange7d
	case "30d", "month":
		return UsageRange30d
	case "all", "":
		if strings.TrimSpace(raw) == "" {
			return UsageRange24h
		}
		return UsageRangeAll
	default:
		return UsageRange24h
	}
}

func applyUsageRange(filter StatsFilter, raw string, now time.Time) (StatsFilter, UsageRange) {
	rng := parseUsageRange(raw)
	if filter.Until.IsZero() {
		filter.Until = now
	}
	if filter.Since.IsZero() {
		switch rng {
		case UsageRange24h:
			filter.Since = filter.Until.Add(-24 * time.Hour)
		case UsageRange7d:
			filter.Since = filter.Until.Add(-7 * 24 * time.Hour)
		case UsageRange30d:
			filter.Since = filter.Until.Add(-30 * 24 * time.Hour)
		case UsageRangeAll:
			// leave Since zero for unbounded history
		}
	}
	return filter, rng
}

func (rl *RequestLogger) GetUsage(filter StatsFilter, rng UsageRange, loc *time.Location) UsageReport {
	if loc == nil {
		loc = time.Local
	}
	until := filter.Until
	if until.IsZero() {
		until = time.Now()
	}
	report := UsageReport{
		Range:       string(rng),
		Until:       until.In(loc),
		Timezone:    loc.String(),
		Granularity: usageGranularity(rng),
		ByModel:     []UsageModelRow{},
		ByRoute:     []UsageRouteRow{},
		Series:      []UsageBucket{},
	}
	if !filter.Since.IsZero() {
		since := filter.Since.In(loc)
		report.Since = &since
	}
	if rl.prices != nil {
		report.Pricing = rl.prices.Status()
	} else {
		report.Pricing = PriceStatus{Source: modelsDevAPIURL, Stale: true}
	}
	if rl.store == nil {
		zero := 0.0
		report.Totals.CostUSD = &zero
		report.Totals.CostLocalUSD = &zero
		report.Totals.CostAPIKeyUSD = &zero
		return report
	}

	totals, err := rl.store.QueryUsageTotals(filter)
	if err != nil {
		log.Warnf("db query usage totals: %v", err)
	} else {
		report.Totals = totals
	}
	models, err := rl.store.QueryUsageByModel(filter)
	if err != nil {
		log.Warnf("db query usage by model: %v", err)
	} else {
		report.ByModel = models
	}
	routes, err := rl.store.QueryUsageByRoute(filter)
	if err != nil {
		log.Warnf("db query usage by route: %v", err)
	} else {
		report.ByRoute = routes
	}
	series, err := rl.store.QueryUsageSeries(filter, rng, loc)
	if err != nil {
		log.Warnf("db query usage series: %v", err)
	} else {
		report.Series = series
	}
	rl.estimateUnpriced(&report)
	return report
}

func usageGranularity(rng UsageRange) string {
	if rng == UsageRange24h {
		return "hour"
	}
	return "day"
}

func (rl *RequestLogger) estimateUnpriced(report *UsageReport) {
	if report == nil {
		return
	}
	unpriced := make([]string, 0)
	added := 0.0
	for i := range report.ByModel {
		row := &report.ByModel[i]
		if row.UnpricedTokens == 0 {
			continue
		}
		if rl.prices == nil {
			unpriced = append(unpriced, row.Model)
			continue
		}
		price, ok := rl.prices.Lookup(row.Model)
		if !ok {
			unpriced = append(unpriced, row.Model)
			continue
		}
		est := TokenCostUSD(TokenUsage{
			InputTokens:       row.Tokens.Input,
			OutputTokens:      row.Tokens.Output,
			CacheReadTokens:   row.Tokens.CacheRead,
			CacheCreateTokens: row.Tokens.CacheCreate,
		}, price)
		existing := 0.0
		if row.CostUSD != nil {
			existing = *row.CostUSD
		}
		sum := existing + est
		row.CostUSD = &sum
		added += est
		if row.Priced == "unpriced" {
			row.Priced = "estimated"
		} else {
			row.Priced = "partial"
		}
	}
	if added != 0 && report.Totals.CostUSD != nil {
		sum := *report.Totals.CostUSD + added
		report.Totals.CostUSD = &sum
		report.Totals.CostByComponent.Total = sum
	}
	report.Totals.UnpricedModels = unpriced
}
