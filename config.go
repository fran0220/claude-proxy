package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	DefaultProxyListen = ":9327"
	DefaultAdminListen = ":9328"

	DefaultStandardClaudeModel = "claude-sonnet-4-6"
)

type Config struct {
	mu             sync.RWMutex      `yaml:"-"`
	path           string            `yaml:"-"`
	Listen         string            `yaml:"listen" json:"listen"`
	AdminListen    string            `yaml:"admin-listen" json:"admin_listen"`
	DataDir        string            `yaml:"data-dir" json:"data_dir"`
	UserID         string            `yaml:"user-id,omitempty" json:"user_id,omitempty"`
	Claude         ClaudeConfig      `yaml:"claude" json:"claude"`
	ModelRedirects map[string]string `yaml:"model-redirects,omitempty" json:"model_redirects,omitempty"`
	Retry          RetryConfig       `yaml:"retry" json:"retry"`
	Pricing        PricingConfig     `yaml:"pricing,omitempty" json:"pricing,omitempty"`
	Debug          DebugConfig       `yaml:"debug,omitempty" json:"debug,omitempty"`
	Security       SecurityConfig    `yaml:"security" json:"-"`
}

type ClaudeConfig struct {
	Source  string        `yaml:"source" json:"source"` // keychain | apikey
	APIKey  string        `yaml:"api-key,omitempty" json:"api_key,omitempty"`
	BaseURL string        `yaml:"base-url,omitempty" json:"base_url,omitempty"`
	Entries []APIKeyEntry `yaml:"entries,omitempty" json:"entries,omitempty"`
	Models  []ModelEntry  `yaml:"models" json:"models"`
}

type APIKeyEntry struct {
	ID      string `yaml:"id" json:"id"`
	Label   string `yaml:"label,omitempty" json:"label,omitempty"`
	APIKey  string `yaml:"api-key" json:"api_key"`
	BaseURL string `yaml:"base-url,omitempty" json:"base_url,omitempty"`
}

type ModelEntry struct {
	Name           string    `yaml:"name" json:"name"`
	Route          string    `yaml:"route" json:"route"` // local | apikey
	DisplayName    string    `yaml:"display-name,omitempty" json:"display_name,omitempty"`
	MaxInputTokens int64     `yaml:"max-input-tokens,omitempty" json:"max_input_tokens,omitempty"`
	MaxTokens      int64     `yaml:"max-tokens,omitempty" json:"max_tokens,omitempty"`
	Discovered     bool      `yaml:"discovered,omitempty" json:"discovered,omitempty"`
	LastSeen       time.Time `yaml:"last-seen,omitempty" json:"last_seen,omitempty"`
}

type RetryConfig struct {
	MaxAttempts  int           `yaml:"max-attempts" json:"max_attempts"`
	InitialDelay time.Duration `yaml:"initial-delay" json:"initial_delay"`
}

type DebugConfig struct {
	DumpLastRequest bool   `yaml:"dump-last-request,omitempty" json:"dump_last_request,omitempty"`
	DumpPath        string `yaml:"dump-path,omitempty" json:"dump_path,omitempty"`
}

type PricingConfig struct {
	Timezone  string                `yaml:"timezone,omitempty" json:"timezone,omitempty"`
	Overrides map[string]ModelPrice `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}

type SecurityConfig struct {
	AccessToken string `yaml:"access-token" json:"-"`
	AdminToken  string `yaml:"admin-token" json:"-"`
}

func generateID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func generateToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("secure random unavailable: %v", err))
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

func defaultConfigDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude-proxy")
}

func defaultConfigPath() string {
	if p := os.Getenv("CLAUDE_PROXY_CONFIG"); p != "" {
		return expandHome(p)
	}
	return filepath.Join(defaultConfigDir(), "config.yaml")
}

func defaultConfig() *Config {
	return &Config{
		Listen:      DefaultProxyListen,
		AdminListen: DefaultAdminListen,
		DataDir:     defaultConfigDir(),
		Claude: ClaudeConfig{
			Source: "keychain",
			Models: []ModelEntry{
				{Name: "claude-opus-4-8", Route: RouteLocal, DisplayName: "Claude Opus 4.8"},
				{Name: "claude-sonnet-4-6", Route: RouteLocal, DisplayName: "Claude Sonnet 4.6"},
				{Name: "claude-haiku-4-5-20251001", Route: RouteLocal, DisplayName: "Claude Haiku 4.5"},
			},
		},
		ModelRedirects: map[string]string{
			"claude-opus-4-7": "claude-opus-4-8",
		},
		Retry: RetryConfig{
			MaxAttempts:  5,
			InitialDelay: time.Second,
		},
	}
}

func loadConfig() *Config {
	cfg := defaultConfig()
	cfgPath := defaultConfigPath()
	cfg.path = cfgPath

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Infof("config not found at %s, creating default", cfgPath)
			cfg.normalize()
			if err := cfg.Save(); err != nil {
				log.Warnf("failed to save default config: %v", err)
			}
			return cfg
		}
		log.Warnf("failed to read config: %v, using defaults", err)
		cfg.normalize()
		return cfg
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		log.Warnf("failed to parse config: %v, using defaults", err)
		cfg = defaultConfig()
	}
	cfg.path = cfgPath
	changed := cfg.normalize()
	if changed {
		log.Info("config normalized, saving")
		if err := cfg.Save(); err != nil {
			log.Warnf("failed to save normalized config: %v", err)
		}
	}
	return cfg
}

func (c *Config) normalize() bool {
	changed := false
	if c.Listen == "" {
		c.Listen = DefaultProxyListen
		changed = true
	}
	if c.AdminListen == "" {
		c.AdminListen = DefaultAdminListen
		changed = true
	}
	if c.DataDir == "" {
		c.DataDir = defaultConfigDir()
		changed = true
	}
	c.DataDir = expandHome(c.DataDir)
	if c.Claude.Source == "" {
		c.Claude.Source = "keychain"
		changed = true
	}
	if c.Claude.Source != "keychain" && c.Claude.Source != "apikey" {
		c.Claude.Source = "keychain"
		changed = true
	}
	if c.Retry.MaxAttempts <= 0 {
		c.Retry.MaxAttempts = 5
		changed = true
	}
	if c.Retry.InitialDelay <= 0 {
		c.Retry.InitialDelay = time.Second
		changed = true
	}
	if c.UserID == "" || !isValidClaudeUserID(c.UserID) {
		c.UserID = generateClaudeUserID()
		changed = true
	}
	if c.ModelRedirects == nil {
		c.ModelRedirects = map[string]string{}
		changed = true
	}
	defaults := defaultConfig()
	for from, to := range defaults.ModelRedirects {
		if _, ok := c.ModelRedirects[from]; !ok {
			c.ModelRedirects[from] = to
			changed = true
		}
	}
	changed = mergeDefaultModels(&c.Claude.Models, defaults.Claude.Models) || changed
	for i := range c.Claude.Models {
		if c.Claude.Models[i].Route == "" || !isValidRoute(c.Claude.Models[i].Route) {
			c.Claude.Models[i].Route = defaultRouteForSource(c.Claude.Source)
			changed = true
		}
	}
	if c.Debug.DumpPath == "" {
		c.Debug.DumpPath = filepath.Join(os.TempDir(), "claude-proxy-last-request.json")
	}
	if strings.TrimSpace(c.Security.AccessToken) == "" {
		c.Security.AccessToken = generateToken("cp_")
		changed = true
	}
	if strings.TrimSpace(c.Security.AdminToken) == "" {
		c.Security.AdminToken = generateToken("cp_admin_")
		changed = true
	}
	return changed
}

func mergeDefaultModels(models *[]ModelEntry, defaults []ModelEntry) bool {
	changed := false
	clean := make([]ModelEntry, 0, len(*models)+len(defaults))
	seen := make(map[string]bool, len(*models)+len(defaults))
	for _, m := range *models {
		if m.Name == "" || m.Name == "undefined" || seen[m.Name] {
			changed = true
			continue
		}
		seen[m.Name] = true
		clean = append(clean, m)
	}
	for _, d := range defaults {
		if !seen[d.Name] {
			seen[d.Name] = true
			clean = append(clean, d)
			changed = true
		}
	}
	*models = clean
	return changed
}

func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.path == "" {
		c.path = defaultConfigPath()
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return writePrivateFile(c.path, data)
}

func (c *Config) AccessToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Security.AccessToken
}

func (c *Config) AdminToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Security.AdminToken
}

func (c *Config) DataDirPath() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.DataDir == "" {
		return defaultConfigDir()
	}
	return expandHome(c.DataDir)
}

func (c *Config) PricingOverrides() map[string]ModelPrice {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.Pricing.Overrides) == 0 {
		return nil
	}
	out := make(map[string]ModelPrice, len(c.Pricing.Overrides))
	for k, v := range c.Pricing.Overrides {
		out[k] = v
	}
	return out
}

func (c *Config) TimeLocation() *time.Location {
	c.mu.RLock()
	tz := strings.TrimSpace(c.Pricing.Timezone)
	c.mu.RUnlock()
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Warnf("invalid pricing.timezone %q, using local: %v", tz, err)
		return time.Local
	}
	return loc
}

func (c *Config) SetClaudeSource(source string, applyToModels bool) bool {
	if source != "keychain" && source != "apikey" {
		return false
	}
	route := defaultRouteForSource(source)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Claude.Source = source
	if applyToModels {
		for i := range c.Claude.Models {
			c.Claude.Models[i].Route = route
		}
	}
	return true
}

func (c *Config) ModelRoute(model string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, m := range c.Claude.Models {
		if m.Name == model {
			if isValidRoute(m.Route) {
				return m.Route
			}
			break
		}
	}
	return defaultRouteForSource(c.Claude.Source)
}

func (c *Config) SetModelRoute(model, route string) bool {
	if model == "" || !isValidRoute(route) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Claude.Models {
		if c.Claude.Models[i].Name == model {
			c.Claude.Models[i].Route = route
			return true
		}
	}
	c.Claude.Models = append(c.Claude.Models, ModelEntry{Name: model, Route: route})
	return true
}

func (c *Config) AddModel(model, route, displayName string) bool {
	if model == "" {
		return false
	}
	if !isValidRoute(route) {
		route = defaultRouteForSource(c.Claude.Source)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Claude.Models {
		if c.Claude.Models[i].Name == model {
			c.Claude.Models[i].Route = route
			if displayName != "" {
				c.Claude.Models[i].DisplayName = displayName
			}
			return true
		}
	}
	c.Claude.Models = append(c.Claude.Models, ModelEntry{Name: model, Route: route, DisplayName: displayName})
	return true
}

func (c *Config) DeleteModel(model string) bool {
	if model == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	filtered := c.Claude.Models[:0]
	removed := false
	for _, m := range c.Claude.Models {
		if m.Name == model {
			removed = true
			continue
		}
		filtered = append(filtered, m)
	}
	c.Claude.Models = filtered
	return removed
}

func (c *Config) ResolveModelRedirect(model string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	target, ok := c.ModelRedirects[model]
	return target, ok && target != "" && target != model
}

func (c *Config) SetModelRedirect(from, to string) bool {
	if from == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ModelRedirects == nil {
		c.ModelRedirects = map[string]string{}
	}
	if to == "" || to == from {
		delete(c.ModelRedirects, from)
	} else {
		c.ModelRedirects[from] = to
	}
	return true
}

func (c *Config) MergeDiscoveredModels(models []DiscoveredModel, route string, seenAt time.Time) (added, updated int) {
	if !isValidRoute(route) {
		route = defaultRouteForSource(c.Claude.Source)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := make(map[string]int, len(c.Claude.Models))
	for i, m := range c.Claude.Models {
		idx[m.Name] = i
	}
	for _, dm := range models {
		if dm.ID == "" {
			continue
		}
		if i, ok := idx[dm.ID]; ok {
			m := &c.Claude.Models[i]
			m.Discovered = true
			m.LastSeen = seenAt
			if dm.DisplayName != "" {
				m.DisplayName = dm.DisplayName
			}
			if dm.MaxInputTokens > 0 {
				m.MaxInputTokens = dm.MaxInputTokens
			}
			if dm.MaxTokens > 0 {
				m.MaxTokens = dm.MaxTokens
			}
			updated++
			continue
		}
		c.Claude.Models = append(c.Claude.Models, ModelEntry{
			Name:           dm.ID,
			Route:          route,
			DisplayName:    dm.DisplayName,
			MaxInputTokens: dm.MaxInputTokens,
			MaxTokens:      dm.MaxTokens,
			Discovered:     true,
			LastSeen:       seenAt,
		})
		idx[dm.ID] = len(c.Claude.Models) - 1
		added++
	}
	return added, updated
}

func (c *Config) AllAPIKeys() []APIKeyEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.allAPIKeysUnlocked()
}

func (c *Config) allAPIKeysUnlocked() []APIKeyEntry {
	entries := make([]APIKeyEntry, 0, len(c.Claude.Entries)+1)
	if c.Claude.APIKey != "" {
		entries = append(entries, APIKeyEntry{ID: "_legacy", Label: "Legacy", APIKey: c.Claude.APIKey, BaseURL: c.Claude.BaseURL})
	}
	for _, e := range c.Claude.Entries {
		if e.APIKey == "" {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

func (c *Config) PreferredAPIKey() (APIKeyEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, e := range c.Claude.Entries {
		if e.APIKey != "" {
			return e, true
		}
	}
	if c.Claude.APIKey != "" {
		return APIKeyEntry{ID: "_legacy", Label: "Legacy", APIKey: c.Claude.APIKey, BaseURL: c.Claude.BaseURL}, true
	}
	return APIKeyEntry{}, false
}

func (c *Config) AddAPIKey(label, apiKey, baseURL string) (APIKeyEntry, bool) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return APIKeyEntry{}, false
	}
	entry := APIKeyEntry{ID: generateID(), Label: strings.TrimSpace(label), APIKey: apiKey, BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/")}
	if entry.Label == "" {
		entry.Label = "Anthropic API Key"
	}
	c.mu.Lock()
	c.Claude.Entries = append(c.Claude.Entries, entry)
	c.mu.Unlock()
	return entry, true
}

func (c *Config) UpdateAPIKey(id, label, apiKey, baseURL string) bool {
	if id == "" || id == "_legacy" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.Claude.Entries {
		if c.Claude.Entries[i].ID == id {
			if label != "" {
				c.Claude.Entries[i].Label = label
			}
			if apiKey != "" {
				c.Claude.Entries[i].APIKey = apiKey
			}
			c.Claude.Entries[i].BaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
			return true
		}
	}
	return false
}

func (c *Config) RemoveAPIKey(id string) bool {
	if id == "" || id == "_legacy" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	filtered := c.Claude.Entries[:0]
	removed := false
	for _, e := range c.Claude.Entries {
		if e.ID == id {
			removed = true
			continue
		}
		filtered = append(filtered, e)
	}
	c.Claude.Entries = filtered
	return removed
}

func isValidRoute(route string) bool {
	return route == RouteLocal || route == RouteAPIKey
}

func defaultRouteForSource(source string) string {
	if source == "apikey" {
		return RouteAPIKey
	}
	return RouteLocal
}

func expandHome(path string) string {
	if path == "" || path == "~" {
		return defaultConfigDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}

func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 10 {
		return "****"
	}
	return key[:6] + "..." + key[len(key)-4:]
}

func maskEntries(entries []APIKeyEntry) []map[string]any {
	masked := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		masked = append(masked, map[string]any{
			"id":       e.ID,
			"label":    e.Label,
			"api_key":  maskKey(e.APIKey),
			"base_url": e.BaseURL,
		})
	}
	return masked
}
