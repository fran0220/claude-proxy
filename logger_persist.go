package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	_ "modernc.org/sqlite"
)

type DBStore struct {
	db *sql.DB
}

func NewDBStore(dataDir string) (*DBStore, error) {
	dir := dataDir
	if dir == "" {
		dir = defaultConfigDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	dbPath := filepath.Join(dir, "claude-proxy.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	log.Infof("database opened at %s", dbPath)
	return &DBStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS request_logs (
			id                  TEXT PRIMARY KEY,
			timestamp           DATETIME NOT NULL,
			model               TEXT NOT NULL DEFAULT '',
			provider            TEXT NOT NULL DEFAULT '',
			route               TEXT NOT NULL DEFAULT '',
			path                TEXT NOT NULL DEFAULT '',
			status              INTEGER NOT NULL DEFAULT 0,
			latency_ms          INTEGER NOT NULL DEFAULT 0,
			input_tokens        INTEGER NOT NULL DEFAULT 0,
			output_tokens       INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
			cache_create_tokens INTEGER NOT NULL DEFAULT 0,
			error               TEXT NOT NULL DEFAULT '',
			retries             INTEGER NOT NULL DEFAULT 0,
			request_body        TEXT NOT NULL DEFAULT '',
			response_body       TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_logs_timestamp ON request_logs(timestamp);
		CREATE INDEX IF NOT EXISTS idx_logs_model ON request_logs(model);
	`)
	return err
}

func (s *DBStore) Close() error {
	return s.db.Close()
}

func (s *DBStore) InsertLog(entry RequestLog) error {
	_, err := s.db.Exec(`
		INSERT INTO request_logs
			(id, timestamp, model, provider, route, path, status, latency_ms,
			 input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
			 error, retries, request_body, response_body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Timestamp.UTC(), entry.Model, entry.Provider,
		entry.Route, entry.Path, entry.Status, entry.Latency.Milliseconds(),
		entry.Tokens.InputTokens, entry.Tokens.OutputTokens,
		entry.Tokens.CacheReadTokens, entry.Tokens.CacheCreateTokens,
		entry.Error, entry.Retries, entry.RequestBody, entry.ResponseBody,
	)
	return err
}

func (s *DBStore) QueryLogs(limit, offset int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, timestamp, model, provider, route, path, status, latency_ms,
		       input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
		       error, retries
		FROM request_logs
		ORDER BY timestamp DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows, false)
}

func (s *DBStore) QueryErrors(limit int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, timestamp, model, provider, route, path, status, latency_ms,
		       input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
		       error, retries, request_body, response_body
		FROM request_logs
		WHERE status >= 400 OR error != ''
		ORDER BY timestamp DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows, true)
}

func routeBucketExpr() string {
	return "CASE " +
		"WHEN LOWER(route) = 'local' OR LOWER(route) LIKE 'local/%' THEN 'local' " +
		"WHEN LOWER(route) = 'apikey' OR LOWER(route) LIKE 'apikey/%' THEN 'apikey' " +
		"WHEN route = '' THEN 'unknown' " +
		"ELSE LOWER(route) END"
}

func directInputExpr() string {
	return "input_tokens"
}

func freshInputExpr() string {
	return "(input_tokens + cache_create_tokens)"
}

func logicalInputExpr() string {
	return "(input_tokens + cache_read_tokens + cache_create_tokens)"
}

func totalTokensExpr() string {
	return "(" + logicalInputExpr() + " + output_tokens)"
}

func buildStatsWhere(filter StatsFilter) (string, []any) {
	var conditions []string
	var args []any

	if filter.Provider != "" {
		conditions = append(conditions, "provider = ?")
		args = append(args, filter.Provider)
	}
	if filter.Model != "" {
		conditions = append(conditions, "model = ?")
		args = append(args, filter.Model)
	}
	if filter.Route != "" {
		conditions = append(conditions, routeBucketExpr()+" = ?")
		args = append(args, strings.ToLower(filter.Route))
	}
	if !filter.Since.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, filter.Since.UTC())
	}
	if !filter.Until.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, filter.Until.UTC())
	}

	if len(conditions) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conditions, " AND "), args
}

func (s *DBStore) QueryStats(filter StatsFilter) (RequestStats, error) {
	stats := RequestStats{ByModel: make(map[string]*ModelStats)}
	where, args := buildStatsWhere(filter)

	row := s.db.QueryRow(`
		SELECT COALESCE(COUNT(*),0),
		       COALESCE(SUM(CASE WHEN status >= 400 OR error != '' THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_create_tokens),0),
		       COALESCE(SUM(`+directInputExpr()+`),0),
		       COALESCE(SUM(`+freshInputExpr()+`),0),
		       COALESCE(SUM(`+logicalInputExpr()+`),0),
		       COALESCE(SUM(`+totalTokensExpr()+`),0)
		FROM request_logs`+where, args...)
	if err := row.Scan(&stats.TotalRequests, &stats.TotalErrors,
		&stats.TotalInputTokens, &stats.TotalOutputTokens,
		&stats.TotalCacheReadTokens, &stats.TotalCacheCreateTokens,
		&stats.TotalDirectInputTokens, &stats.TotalFreshInputTokens,
		&stats.TotalLogicalInputTokens, &stats.TotalTokens); err != nil {
		return stats, err
	}

	modelRowsQuery := `
		SELECT model, provider,
		       COUNT(*) as reqs,
		       SUM(CASE WHEN status >= 400 OR error != '' THEN 1 ELSE 0 END) as errs,
		       SUM(input_tokens) as inp,
		       SUM(output_tokens) as outp,
		       SUM(cache_read_tokens) as cached,
		       SUM(cache_create_tokens) as cc,
		       SUM(` + directInputExpr() + `) as direct_inp,
		       SUM(` + freshInputExpr() + `) as fresh_inp,
		       SUM(` + logicalInputExpr() + `) as logical_inp,
		       SUM(` + totalTokensExpr() + `) as total_tok
		FROM request_logs
	`
	modelRowsWhere := " WHERE model != ''"
	if where != "" {
		modelRowsWhere += " AND " + strings.TrimPrefix(where, " WHERE ")
	}
	rows, err := s.db.Query(modelRowsQuery+modelRowsWhere+`
		GROUP BY model, provider`, args...)
	if err != nil {
		return stats, err
	}
	defer rows.Close()

	for rows.Next() {
		var ms ModelStats
		if err := rows.Scan(&ms.Model, &ms.Provider, &ms.TotalRequests,
			&ms.TotalErrors, &ms.TotalInput, &ms.TotalOutput, &ms.TotalCached,
			&ms.TotalCacheCreate, &ms.TotalDirectInput, &ms.TotalFreshInput,
			&ms.TotalLogicalInput, &ms.TotalTokens); err != nil {
			continue
		}
		key := ms.Provider + "|" + ms.Model
		stats.ByModel[key] = &ms
	}
	return stats, nil
}

func (s *DBStore) QueryLogsFiltered(limit, offset int, provider, route string, minStatus int) ([]RequestLog, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, timestamp, model, provider, route, path, status, latency_ms,
	       input_tokens, output_tokens, cache_read_tokens, cache_create_tokens,
	       error, retries FROM request_logs WHERE 1=1`
	var args []any
	if provider != "" {
		query += ` AND provider = ?`
		args = append(args, provider)
	}
	if route != "" {
		query += ` AND route = ?`
		args = append(args, route)
	}
	if minStatus > 0 {
		query += ` AND status >= ?`
		args = append(args, minStatus)
	}
	query += ` ORDER BY timestamp DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanLogs(rows, false)
}

// QueryStatsByDay returns daily aggregated stats
func (s *DBStore) QueryStatsByDay(days int, filter StatsFilter) ([]DayStats, error) {
	if days <= 0 {
		days = 30
	}
	where, args := buildStatsWhere(filter)
	dayBase := "timestamp >= date('now', '-' || ? || ' days')"
	args = append([]any{days}, args...)
	if where != "" {
		where = " WHERE " + dayBase + " AND " + strings.TrimPrefix(where, " WHERE ")
	} else {
		where = " WHERE " + dayBase
	}
	rows, err := s.db.Query(`
		SELECT date(timestamp) as day,
		       COUNT(*) as reqs,
		       SUM(CASE WHEN status >= 400 OR error != '' THEN 1 ELSE 0 END) as errs,
		       SUM(input_tokens) as inp,
		       SUM(output_tokens) as outp,
		       SUM(cache_read_tokens) as cached,
		       SUM(cache_create_tokens) as cc,
		       SUM(`+directInputExpr()+`) as direct_inp,
		       SUM(`+freshInputExpr()+`) as fresh_inp,
		       SUM(`+logicalInputExpr()+`) as logical_inp,
		       SUM(`+totalTokensExpr()+`) as total_tok
		FROM request_logs
	`+where+`
		GROUP BY day
		ORDER BY day`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DayStats
	for rows.Next() {
		var d DayStats
		if err := rows.Scan(&d.Day, &d.Requests, &d.Errors, &d.InputTokens, &d.OutputTokens,
			&d.CachedTokens, &d.CacheCreateTokens, &d.DirectInputTokens,
			&d.FreshInputTokens, &d.LogicalInputTokens, &d.TotalTokens); err != nil {
			continue
		}
		result = append(result, d)
	}
	return result, nil
}

// QueryStatsByHour returns hourly aggregated stats for the last N hours
func (s *DBStore) QueryStatsByHour(hours int, filter StatsFilter) ([]HourStats, error) {
	if hours <= 0 {
		hours = 24
	}
	where, args := buildStatsWhere(filter)
	hourBase := "timestamp >= datetime('now', '-' || ? || ' hours')"
	args = append([]any{hours}, args...)
	if where != "" {
		where = " WHERE " + hourBase + " AND " + strings.TrimPrefix(where, " WHERE ")
	} else {
		where = " WHERE " + hourBase
	}
	rows, err := s.db.Query(`
		SELECT strftime('%Y-%m-%d %H:00', timestamp) as hour,
		       COUNT(*) as reqs,
		       SUM(input_tokens) as inp,
		       SUM(output_tokens) as outp,
		       SUM(cache_read_tokens) as cached,
		       SUM(cache_create_tokens) as cc,
		       SUM(`+directInputExpr()+`) as direct_inp,
		       SUM(`+freshInputExpr()+`) as fresh_inp,
		       SUM(`+logicalInputExpr()+`) as logical_inp,
		       SUM(`+totalTokensExpr()+`) as total_tok
		FROM request_logs
	`+where+`
		GROUP BY hour
		ORDER BY hour`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []HourStats
	for rows.Next() {
		var h HourStats
		if err := rows.Scan(&h.Hour, &h.Requests, &h.InputTokens, &h.OutputTokens,
			&h.CachedTokens, &h.CacheCreateTokens, &h.DirectInputTokens,
			&h.FreshInputTokens, &h.LogicalInputTokens, &h.TotalTokens); err != nil {
			continue
		}
		result = append(result, h)
	}
	return result, nil
}

// QueryStatsByRoute returns aggregated stats grouped by route
func (s *DBStore) QueryStatsByRoute(filter StatsFilter) ([]RouteStats, error) {
	where, args := buildStatsWhere(filter)
	rows, err := s.db.Query(`
		SELECT `+routeBucketExpr()+` as route_bucket,
		       COUNT(*) as reqs,
		       SUM(input_tokens) as inp,
		       SUM(output_tokens) as outp,
		       SUM(cache_read_tokens) as cached,
		       SUM(cache_create_tokens) as cc,
		       SUM(`+directInputExpr()+`) as direct_inp,
		       SUM(`+freshInputExpr()+`) as fresh_inp,
		       SUM(`+logicalInputExpr()+`) as logical_inp,
		       SUM(`+totalTokensExpr()+`) as total_tok
		FROM request_logs
	`+where+`
		GROUP BY route_bucket`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RouteStats
	for rows.Next() {
		var r RouteStats
		if err := rows.Scan(&r.Route, &r.Requests, &r.InputTokens, &r.OutputTokens,
			&r.CachedTokens, &r.CacheCreateTokens, &r.DirectInputTokens,
			&r.FreshInputTokens, &r.LogicalInputTokens, &r.TotalTokens); err != nil {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}

// QueryTokenTotals returns total token counts broken down by type
func (s *DBStore) QueryTokenTotals(filter StatsFilter) (TokenTotals, error) {
	var t TokenTotals
	where, args := buildStatsWhere(filter)
	row := s.db.QueryRow(`
		SELECT COALESCE(SUM(input_tokens),0),
		       COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_create_tokens),0),
		       COALESCE(SUM(`+directInputExpr()+`),0),
		       COALESCE(SUM(`+freshInputExpr()+`),0),
		       COALESCE(SUM(`+logicalInputExpr()+`),0),
		       COALESCE(SUM(`+totalTokensExpr()+`),0)
		FROM request_logs`+where, args...)
	err := row.Scan(&t.Input, &t.Output, &t.CacheRead, &t.CacheCreate,
		&t.DirectInput, &t.FreshInput, &t.LogicalInput, &t.TotalTokens)
	t.CacheTotal = t.CacheRead + t.CacheCreate
	t.Total = t.TotalTokens
	return t, err
}

func scanLogs(rows *sql.Rows, withBodies bool) ([]RequestLog, error) {
	var logs []RequestLog
	for rows.Next() {
		var entry RequestLog
		var latencyMs int64
		var ts time.Time

		if withBodies {
			if err := rows.Scan(&entry.ID, &ts, &entry.Model, &entry.Provider,
				&entry.Route, &entry.Path, &entry.Status, &latencyMs,
				&entry.Tokens.InputTokens, &entry.Tokens.OutputTokens,
				&entry.Tokens.CacheReadTokens, &entry.Tokens.CacheCreateTokens,
				&entry.Error, &entry.Retries,
				&entry.RequestBody, &entry.ResponseBody); err != nil {
				continue
			}
		} else {
			if err := rows.Scan(&entry.ID, &ts, &entry.Model, &entry.Provider,
				&entry.Route, &entry.Path, &entry.Status, &latencyMs,
				&entry.Tokens.InputTokens, &entry.Tokens.OutputTokens,
				&entry.Tokens.CacheReadTokens, &entry.Tokens.CacheCreateTokens,
				&entry.Error, &entry.Retries); err != nil {
				continue
			}
		}
		entry.Timestamp = ts
		entry.Latency = time.Duration(latencyMs) * time.Millisecond
		logs = append(logs, entry)
	}
	return logs, nil
}
