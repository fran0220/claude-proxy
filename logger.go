package main

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type RequestLog struct {
	ID           string        `json:"id"`
	Timestamp    time.Time     `json:"timestamp"`
	Model        string        `json:"model"`
	Provider     string        `json:"provider"`
	Route        string        `json:"route"`
	Path         string        `json:"path"`
	Status       int           `json:"status"`
	Latency      time.Duration `json:"latency"`
	Tokens       TokenUsage    `json:"tokens"`
	Error        string        `json:"error,omitempty"`
	RequestBody  string        `json:"request_body,omitempty"`
	ResponseBody string        `json:"response_body,omitempty"`
	Retries      int           `json:"retries,omitempty"`
}

type TokenUsage struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`
}

type ModelStats struct {
	Model             string `json:"model"`
	Provider          string `json:"provider"`
	TotalRequests     int64  `json:"total_requests"`
	TotalErrors       int64  `json:"total_errors"`
	TotalInput        int64  `json:"total_input_tokens"`
	TotalOutput       int64  `json:"total_output_tokens"`
	TotalCached       int64  `json:"total_cached_tokens"`
	TotalCacheCreate  int64  `json:"total_cache_create_tokens"`
	TotalDirectInput  int64  `json:"total_direct_input_tokens"`
	TotalFreshInput   int64  `json:"total_fresh_input_tokens"`
	TotalLogicalInput int64  `json:"total_logical_input_tokens"`
	TotalTokens       int64  `json:"total_tokens"`
}

type RequestStats struct {
	TotalRequests           int64                  `json:"total_requests"`
	TotalErrors             int64                  `json:"total_errors"`
	TotalInputTokens        int64                  `json:"total_input_tokens"`
	TotalOutputTokens       int64                  `json:"total_output_tokens"`
	TotalCacheReadTokens    int64                  `json:"total_cache_read_tokens"`
	TotalCacheCreateTokens  int64                  `json:"total_cache_create_tokens"`
	TotalDirectInputTokens  int64                  `json:"total_direct_input_tokens"`
	TotalFreshInputTokens   int64                  `json:"total_fresh_input_tokens"`
	TotalLogicalInputTokens int64                  `json:"total_logical_input_tokens"`
	TotalTokens             int64                  `json:"total_tokens"`
	Uptime                  string                 `json:"uptime"`
	ByModel                 map[string]*ModelStats `json:"by_model"`
}

type DayStats struct {
	Day                string `json:"day"`
	Requests           int64  `json:"requests"`
	Errors             int64  `json:"errors"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	CachedTokens       int64  `json:"cached_tokens"`
	CacheCreateTokens  int64  `json:"cache_create_tokens"`
	DirectInputTokens  int64  `json:"direct_input_tokens"`
	FreshInputTokens   int64  `json:"fresh_input_tokens"`
	LogicalInputTokens int64  `json:"logical_input_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
}

type HourStats struct {
	Hour               string `json:"hour"`
	Requests           int64  `json:"requests"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	CachedTokens       int64  `json:"cached_tokens"`
	CacheCreateTokens  int64  `json:"cache_create_tokens"`
	DirectInputTokens  int64  `json:"direct_input_tokens"`
	FreshInputTokens   int64  `json:"fresh_input_tokens"`
	LogicalInputTokens int64  `json:"logical_input_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
}

type RouteStats struct {
	Route              string `json:"route"`
	Requests           int64  `json:"requests"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	CachedTokens       int64  `json:"cached_tokens"`
	CacheCreateTokens  int64  `json:"cache_create_tokens"`
	DirectInputTokens  int64  `json:"direct_input_tokens"`
	FreshInputTokens   int64  `json:"fresh_input_tokens"`
	LogicalInputTokens int64  `json:"logical_input_tokens"`
	TotalTokens        int64  `json:"total_tokens"`
}

type StatsFilter struct {
	Provider string
	Route    string
	Model    string
	Since    time.Time
	Until    time.Time
}

type TokenTotals struct {
	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheCreate  int64 `json:"cache_create"`
	CacheTotal   int64 `json:"cache_total"`
	DirectInput  int64 `json:"direct_input"`
	FreshInput   int64 `json:"fresh_input"`
	LogicalInput int64 `json:"logical_input"`
	TotalTokens  int64 `json:"total_tokens"`
	Total        int64 `json:"total"`
}

// RequestLogger provides logging with SQLite persistence and a short-lived stats cache.
type RequestLogger struct {
	store     *DBStore
	startTime time.Time

	// Pending entries: LogRequest creates a partial entry, RecordResult completes it.
	mu      sync.Mutex
	pending map[string]*RequestLog // keyed by "model|path|route"
}

func NewRequestLogger(dataDir string) *RequestLogger {
	store, err := NewDBStore(dataDir)
	if err != nil {
		log.Errorf("failed to open database, stats will not persist: %v", err)
	}
	return &RequestLogger{
		store:     store,
		startTime: time.Now(),
		pending:   make(map[string]*RequestLog),
	}
}

// LogRequest records the start of a request. The entry is held in memory
// until RecordResult completes it with status/tokens, then written to DB.
func (rl *RequestLogger) LogRequest(model, provider, route, path string, start time.Time) {
	entry := &RequestLog{
		ID:        uuid.New().String()[:8],
		Timestamp: start,
		Model:     model,
		Provider:  provider,
		Route:     route,
		Path:      path,
		Latency:   time.Since(start),
	}

	rl.mu.Lock()
	rl.pending[pendingKey(model, path, route)] = entry
	rl.mu.Unlock()
}

// RecordResult completes a pending request entry with status, tokens, and error,
// then writes the full record to SQLite.
func (rl *RequestLogger) RecordResult(model string, status int, tokens TokenUsage, retries int, errMsg string, reqBody, respBody string) {
	rl.mu.Lock()
	// Try to find and remove the pending entry
	var entry *RequestLog
	for key, e := range rl.pending {
		if e.Model == model {
			entry = e
			delete(rl.pending, key)
			break
		}
	}
	rl.mu.Unlock()

	if entry == nil {
		// No pending entry (e.g. the request failed before normal proxy handling), create one.
		entry = &RequestLog{
			ID:        uuid.New().String()[:8],
			Timestamp: time.Now(),
			Model:     model,
		}
	}

	entry.Status = status
	entry.Tokens = tokens
	entry.Retries = retries
	entry.Error = errMsg
	if status >= 400 || errMsg != "" {
		entry.RequestBody = truncateLog(reqBody, 4096)
		entry.ResponseBody = truncateLog(respBody, 4096)
	}

	if rl.store != nil {
		if err := rl.store.InsertLog(*entry); err != nil {
			log.Warnf("db insert error: %v", err)
		}
	}
}

// FlushPending writes any pending entries that never got a RecordResult.
func (rl *RequestLogger) FlushPending() {
	rl.mu.Lock()
	pending := make([]*RequestLog, 0, len(rl.pending))
	for _, e := range rl.pending {
		// Only flush entries older than 30s (likely never getting a RecordResult)
		if time.Since(e.Timestamp) > 30*time.Second {
			pending = append(pending, e)
		}
	}
	for _, e := range pending {
		delete(rl.pending, pendingKey(e.Model, e.Path, e.Route))
	}
	rl.mu.Unlock()

	if rl.store != nil {
		for _, e := range pending {
			_ = rl.store.InsertLog(*e)
		}
	}
}

func (rl *RequestLogger) GetLogs(limit, offset int) []RequestLog {
	if rl.store == nil {
		return nil
	}
	logs, err := rl.store.QueryLogs(limit, offset)
	if err != nil {
		log.Warnf("db query logs: %v", err)
		return nil
	}
	return logs
}

func (rl *RequestLogger) GetLogsFiltered(limit, offset int, provider, route string, minStatus int) []RequestLog {
	if rl.store == nil {
		return nil
	}
	logs, err := rl.store.QueryLogsFiltered(limit, offset, provider, route, minStatus)
	if err != nil {
		log.Warnf("db query filtered logs: %v", err)
		return nil
	}
	return logs
}

func (rl *RequestLogger) GetErrors(limit int) []RequestLog {
	if rl.store == nil {
		return nil
	}
	errors, err := rl.store.QueryErrors(limit)
	if err != nil {
		log.Warnf("db query errors: %v", err)
		return nil
	}
	return errors
}

func (rl *RequestLogger) GetStats() RequestStats {
	return rl.GetStatsFiltered(StatsFilter{})
}

func (rl *RequestLogger) GetStatsFiltered(filter StatsFilter) RequestStats {
	if rl.store == nil {
		return RequestStats{
			Uptime:  time.Since(rl.startTime).Round(time.Second).String(),
			ByModel: make(map[string]*ModelStats),
		}
	}

	stats, err := rl.store.QueryStats(filter)
	if err != nil {
		log.Warnf("db query stats: %v", err)
		stats.ByModel = make(map[string]*ModelStats)
	}
	stats.Uptime = time.Since(rl.startTime).Round(time.Second).String()
	return stats
}

func (rl *RequestLogger) Close() {
	if rl.store != nil {
		_ = rl.store.Close()
	}
}

func (rl *RequestLogger) GetStatsByDay(days int) []DayStats {
	return rl.GetStatsByDayFiltered(days, StatsFilter{})
}

func (rl *RequestLogger) GetStatsByDayFiltered(days int, filter StatsFilter) []DayStats {
	if rl.store == nil {
		return nil
	}
	r, err := rl.store.QueryStatsByDay(days, filter)
	if err != nil {
		log.Warnf("db query stats by day: %v", err)
		return nil
	}
	return r
}

func (rl *RequestLogger) GetStatsByHour(hours int) []HourStats {
	return rl.GetStatsByHourFiltered(hours, StatsFilter{})
}

func (rl *RequestLogger) GetStatsByHourFiltered(hours int, filter StatsFilter) []HourStats {
	if rl.store == nil {
		return nil
	}
	r, err := rl.store.QueryStatsByHour(hours, filter)
	if err != nil {
		log.Warnf("db query stats by hour: %v", err)
		return nil
	}
	return r
}

func (rl *RequestLogger) GetStatsByRoute() []RouteStats {
	return rl.GetStatsByRouteFiltered(StatsFilter{})
}

func (rl *RequestLogger) GetStatsByRouteFiltered(filter StatsFilter) []RouteStats {
	if rl.store == nil {
		return nil
	}
	r, err := rl.store.QueryStatsByRoute(filter)
	if err != nil {
		log.Warnf("db query stats by route: %v", err)
		return nil
	}
	return r
}

func (rl *RequestLogger) GetTokenTotals() TokenTotals {
	return rl.GetTokenTotalsFiltered(StatsFilter{})
}

func (rl *RequestLogger) GetTokenTotalsFiltered(filter StatsFilter) TokenTotals {
	if rl.store == nil {
		return TokenTotals{}
	}
	t, err := rl.store.QueryTokenTotals(filter)
	if err != nil {
		log.Warnf("db query token totals: %v", err)
		return TokenTotals{}
	}
	return t
}

func pendingKey(model, path, route string) string {
	return model + "|" + path + "|" + route
}

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}
