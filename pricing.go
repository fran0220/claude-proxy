package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	modelsDevAPIURL = "https://models.dev/api.json"

	PriceSourceModelsDev = "models.dev"
	PriceSourceOverride  = "override"
	PriceSourceUnpriced  = "unpriced"

	CacheTTL5m = "5m"
	CacheTTL1h = "1h"
)

var datedModelSuffix = regexp.MustCompile(`-\d{8}$`)

// ModelPrice is USD per million tokens.
type ModelPrice struct {
	Input     float64 `json:"input" yaml:"input"`
	Output    float64 `json:"output" yaml:"output"`
	CacheRead float64 `json:"cache_read" yaml:"cache_read"`
	CacheWrite float64 `json:"cache_write" yaml:"cache_write"`
	Source    string  `json:"source,omitempty" yaml:"-"`
	MatchedID string  `json:"matched_id,omitempty" yaml:"-"`
}

func (p ModelPrice) Valid() bool {
	return p.Input > 0 || p.Output > 0 || p.CacheRead > 0 || p.CacheWrite > 0
}

type priceCacheFile struct {
	FetchedAt time.Time             `json:"fetched_at"`
	Source    string                `json:"source"`
	Models    map[string]ModelPrice `json:"models"`
}

type modelsDevFile map[string]struct {
	Models map[string]struct {
		ID   string `json:"id"`
		Cost struct {
			Input      float64 `json:"input"`
			Output     float64 `json:"output"`
			CacheRead  float64 `json:"cache_read"`
			CacheWrite float64 `json:"cache_write"`
		} `json:"cost"`
	} `json:"models"`
}

// PriceCatalog loads Anthropic list prices from models.dev and optional config overrides.
type PriceCatalog struct {
	mu        sync.RWMutex
	cfg       *Config
	dir       string
	path      string
	client    *http.Client
	prices    map[string]ModelPrice
	index     map[string]string
	fetchedAt time.Time
	stale     bool
	lastError string
}

func NewPriceCatalog(dataDir string, cfg *Config) *PriceCatalog {
	dir := dataDir
	if dir == "" {
		dir = defaultConfigDir()
	}
	return &PriceCatalog{
		cfg:    cfg,
		dir:    dir,
		path:   filepath.Join(dir, "prices.json"),
		client: &http.Client{Timeout: 20 * time.Second},
		prices: make(map[string]ModelPrice),
		index:  make(map[string]string),
	}
}

func (c *PriceCatalog) LoadDisk() error {
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var cache priceCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return err
	}
	c.mu.Lock()
	c.replaceUnlocked(cache.Models, cache.FetchedAt, true)
	c.mu.Unlock()
	log.Infof("loaded %d cached model prices from %s", len(cache.Models), c.path)
	return nil
}

func (c *PriceCatalog) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevAPIURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "claude-proxy/"+version)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		c.setRefreshError(err)
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		c.setRefreshError(err)
		return err
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("models.dev returned %s", resp.Status)
		c.setRefreshError(err)
		return err
	}
	prices, err := parseAnthropicPrices(body)
	if err != nil {
		c.setRefreshError(err)
		return err
	}
	fetchedAt := time.Now().UTC()
	c.mu.Lock()
	c.replaceUnlocked(prices, fetchedAt, false)
	c.lastError = ""
	c.mu.Unlock()
	if err := c.writeDisk(priceCacheFile{FetchedAt: fetchedAt, Source: modelsDevAPIURL, Models: prices}); err != nil {
		log.Warnf("failed to persist price cache: %v", err)
	}
	log.Infof("refreshed %d Anthropic model prices from models.dev", len(prices))
	return nil
}

func (c *PriceCatalog) LoadBytes(data []byte, fetchedAt time.Time) error {
	prices, err := parseAnthropicPrices(data)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.replaceUnlocked(prices, fetchedAt, false)
	c.lastError = ""
	c.mu.Unlock()
	return nil
}

func (c *PriceCatalog) Lookup(model string) (ModelPrice, bool) {
	if model == "" {
		return ModelPrice{}, false
	}
	if p, ok := c.lookupOverride(model); ok {
		return p, true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return lookupPrice(c.prices, c.index, model, PriceSourceModelsDev)
}

func (c *PriceCatalog) Snapshot(model, cacheTTL string) (ModelPrice, string) {
	p, ok := c.Lookup(model)
	if !ok {
		return ModelPrice{}, PriceSourceUnpriced
	}
	if normalizeCacheTTL(cacheTTL) == CacheTTL1h && p.Input > 0 {
		p.CacheWrite = p.Input * 2
	}
	return p, p.Source
}

type PriceStatus struct {
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
	Stale     bool      `json:"stale"`
	Models    int       `json:"models"`
	LastError string    `json:"last_error,omitempty"`
}

func (c *PriceCatalog) Status() PriceStatus {
	if c == nil {
		return PriceStatus{Source: modelsDevAPIURL, Stale: true}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return PriceStatus{
		Source:    modelsDevAPIURL,
		FetchedAt: c.fetchedAt,
		Stale:     c.stale || len(c.prices) == 0,
		Models:    len(c.prices),
		LastError: c.lastError,
	}
}

func (c *PriceCatalog) Models() map[string]ModelPrice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]ModelPrice, len(c.prices))
	for k, v := range c.prices {
		out[k] = v
	}
	return out
}

func (c *PriceCatalog) lookupOverride(model string) (ModelPrice, bool) {
	if c.cfg == nil {
		return ModelPrice{}, false
	}
	overrides := c.cfg.PricingOverrides()
	if len(overrides) == 0 {
		return ModelPrice{}, false
	}
	index := make(map[string]string, len(overrides))
	prices := make(map[string]ModelPrice, len(overrides))
	for id, price := range overrides {
		price = normalizePrice(price)
		price.Source = PriceSourceOverride
		price.MatchedID = id
		prices[id] = price
		index[normalizeModelID(id)] = id
		stripped := stripDatedModelID(normalizeModelID(id))
		if stripped != normalizeModelID(id) {
			if _, exists := index[stripped]; !exists {
				index[stripped] = id
			}
		}
	}
	return lookupPrice(prices, index, model, PriceSourceOverride)
}

func (c *PriceCatalog) replaceUnlocked(prices map[string]ModelPrice, fetchedAt time.Time, stale bool) {
	c.prices = make(map[string]ModelPrice, len(prices))
	c.index = make(map[string]string, len(prices)*2)
	for id, price := range prices {
		price = normalizePrice(price)
		if price.Source == "" {
			price.Source = PriceSourceModelsDev
		}
		price.MatchedID = id
		c.prices[id] = price
		norm := normalizeModelID(id)
		c.index[norm] = id
		if stripped := stripDatedModelID(norm); stripped != norm {
			if _, exists := c.index[stripped]; !exists {
				c.index[stripped] = id
			}
		}
	}
	c.fetchedAt = fetchedAt
	c.stale = stale
}

func (c *PriceCatalog) setRefreshError(err error) {
	c.mu.Lock()
	c.lastError = err.Error()
	if len(c.prices) > 0 {
		c.stale = true
	}
	c.mu.Unlock()
	log.Warnf("price catalog refresh failed: %v", err)
}

func (c *PriceCatalog) writeDisk(cache priceCacheFile) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o644)
}

func parseAnthropicPrices(data []byte) (map[string]ModelPrice, error) {
	var file modelsDevFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode models.dev api.json: %w", err)
	}
	provider, ok := file["anthropic"]
	if !ok || len(provider.Models) == 0 {
		return nil, fmt.Errorf("models.dev api.json missing anthropic.models")
	}
	prices := make(map[string]ModelPrice, len(provider.Models))
	for id, model := range provider.Models {
		price := ModelPrice{
			Input:      model.Cost.Input,
			Output:     model.Cost.Output,
			CacheRead:  model.Cost.CacheRead,
			CacheWrite: model.Cost.CacheWrite,
			Source:     PriceSourceModelsDev,
			MatchedID:  id,
		}
		price = normalizePrice(price)
		if !price.Valid() {
			continue
		}
		prices[id] = price
	}
	if len(prices) == 0 {
		return nil, fmt.Errorf("models.dev anthropic catalog had no priced models")
	}
	return prices, nil
}

func lookupPrice(prices map[string]ModelPrice, index map[string]string, model, source string) (ModelPrice, bool) {
	if p, ok := prices[model]; ok {
		p.Source = source
		p.MatchedID = model
		return p, true
	}
	candidates := []string{
		normalizeModelID(model),
		stripDatedModelID(normalizeModelID(model)),
	}
	seen := map[string]bool{}
	for _, key := range candidates {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if id, ok := index[key]; ok {
			p := prices[id]
			p.Source = source
			p.MatchedID = id
			return p, true
		}
	}
	return ModelPrice{}, false
}

func normalizeModelID(model string) string {
	s := strings.ToLower(strings.TrimSpace(model))
	s = strings.TrimPrefix(s, "anthropic/")
	s = strings.TrimPrefix(s, "anthropic.")
	s = strings.ReplaceAll(s, ".", "-")
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

func stripDatedModelID(model string) string {
	return datedModelSuffix.ReplaceAllString(model, "")
}

func normalizePrice(p ModelPrice) ModelPrice {
	if p.CacheRead == 0 && p.Input > 0 {
		p.CacheRead = p.Input * 0.1
	}
	if p.CacheWrite == 0 && p.Input > 0 {
		p.CacheWrite = p.Input * 1.25
	}
	return p
}

func normalizeCacheTTL(ttl string) string {
	switch strings.ToLower(strings.TrimSpace(ttl)) {
	case "1h", "1hr", "3600s":
		return CacheTTL1h
	case "5m", "5min", "300s":
		return CacheTTL5m
	default:
		return strings.ToLower(strings.TrimSpace(ttl))
	}
}

func TokenCostUSD(tokens TokenUsage, price ModelPrice) float64 {
	return (float64(tokens.InputTokens)*price.Input +
		float64(tokens.OutputTokens)*price.Output +
		float64(tokens.CacheReadTokens)*price.CacheRead +
		float64(tokens.CacheCreateTokens)*price.CacheWrite) / 1_000_000
}

func isCountTokensPath(path string) bool {
	return strings.Contains(path, "count_tokens")
}

func detectCacheTTL(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if strings.Contains(string(body), `"ttl":"1h"`) || strings.Contains(string(body), `"ttl": "1h"`) {
		return CacheTTL1h
	}
	if strings.Contains(string(body), `"ttl":"5m"`) || strings.Contains(string(body), `"ttl": "5m"`) {
		return CacheTTL5m
	}
	return ""
}

func (t TokenUsage) Total() int64 {
	return t.InputTokens + t.OutputTokens + t.CacheReadTokens + t.CacheCreateTokens
}
