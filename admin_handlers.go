package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *AdminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	stats := s.logger.GetStatsFiltered(StatsFilter{Provider: "anthropic"})
	writeJSON(w, map[string]any{
		"running":        true,
		"version":        version,
		"uptime":         time.Since(s.startAt).Round(time.Second).String(),
		"listen":         s.cfg.Listen,
		"admin_listen":   s.cfg.AdminListen,
		"data_dir":       s.cfg.DataDirPath(),
		"total_requests": stats.TotalRequests,
		"total_errors":   stats.TotalErrors,
		"total_input":    stats.TotalInputTokens,
		"total_output":   stats.TotalOutputTokens,
		"auth":           s.authResolver.AuthStatus(),
	})
}

func (s *AdminServer) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": version})
}

func (s *AdminServer) handleOverview(w http.ResponseWriter, r *http.Request) {
	stats := s.logger.GetStatsFiltered(StatsFilter{Provider: "anthropic"})
	recentLogs := s.logger.GetLogsFiltered(10, 0, "anthropic", "", 0)

	s.cfg.mu.RLock()
	local, apikey, total := countRoutes(s.cfg.Claude.Models)
	models := append([]ModelEntry(nil), s.cfg.Claude.Models...)
	s.cfg.mu.RUnlock()

	writeJSON(w, map[string]any{
		"uptime": time.Since(s.startAt).Round(time.Second).String(),
		"stats":  stats,
		"recent": recentLogs,
		"provider": map[string]any{
			"name":   "anthropic",
			"local":  local,
			"apikey": apikey,
			"total":  total,
			"auth":   s.authResolver.AuthStatus(),
		},
		"models": models,
	})
}

func (s *AdminServer) handleConfig(w http.ResponseWriter, r *http.Request) {
	s.cfg.mu.RLock()
	defer s.cfg.mu.RUnlock()
	writeJSON(w, map[string]any{
		"listen":       s.cfg.Listen,
		"admin_listen": s.cfg.AdminListen,
		"data_dir":     s.cfg.DataDir,
		"debug":        s.cfg.Debug,
		"claude": map[string]any{
			"source":   s.cfg.Claude.Source,
			"has_key":  s.cfg.Claude.APIKey != "" || len(s.cfg.Claude.Entries) > 0,
			"base_url": s.cfg.Claude.BaseURL,
			"models":   s.cfg.Claude.Models,
			"entries":  maskEntries(s.cfg.allAPIKeysUnlocked()),
		},
		"model_redirects": s.cfg.ModelRedirects,
		"retry":           s.cfg.Retry,
	})
}

func (s *AdminServer) handleSetSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Source        string `json:"source"`
		ApplyToModels bool   `json:"apply_to_models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.SetClaudeSource(req.Source, req.ApplyToModels) {
		http.Error(w, "invalid source", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"status":          "ok",
		"source":          req.Source,
		"route":           defaultRouteForSource(req.Source),
		"apply_to_models": req.ApplyToModels,
	})
}

func (s *AdminServer) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger.GetStatsFiltered(parseStatsFilter(r)))
}

func (s *AdminServer) handleStatsByDay(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	writeJSON(w, s.logger.GetStatsByDayFiltered(days, parseStatsFilter(r)))
}

func (s *AdminServer) handleStatsByHour(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	writeJSON(w, s.logger.GetStatsByHourFiltered(hours, parseStatsFilter(r)))
}

func (s *AdminServer) handleStatsByRoute(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger.GetStatsByRouteFiltered(parseStatsFilter(r)))
}

func (s *AdminServer) handleTokenTotals(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.logger.GetTokenTotalsFiltered(parseStatsFilter(r)))
}

func (s *AdminServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		provider = "anthropic"
	}
	route := r.URL.Query().Get("route")
	minStatus, _ := strconv.Atoi(r.URL.Query().Get("status"))
	writeJSON(w, s.logger.GetLogsFiltered(limit, offset, provider, route, minStatus))
}

func (s *AdminServer) handleErrors(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	writeJSON(w, s.logger.GetErrors(limit))
}

func (s *AdminServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"claude": s.authResolver.AuthStatus()})
}

func (s *AdminServer) handleTokenRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.tokenMgr.refresh(r.Context()); err != nil {
		writeJSON(w, map[string]any{"status": "error", "message": err.Error()})
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, maskEntries(s.cfg.AllAPIKeys()))
}

func (s *AdminServer) handleAddAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Label   string `json:"label"`
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entry, ok := s.cfg.AddAPIKey(req.Label, req.APIKey, req.BaseURL)
	if !ok {
		http.Error(w, "api_key is required", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	entry.APIKey = maskKey(entry.APIKey)
	writeJSON(w, map[string]any{"status": "ok", "entry": entry})
}

func (s *AdminServer) handleUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.UpdateAPIKey(req.ID, req.Label, req.APIKey, req.BaseURL) {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleRemoveAPIKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.RemoveAPIKey(req.ID) {
		http.Error(w, "key not found", http.StatusNotFound)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleTestCredential(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Route   string `json:"route"`
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var auth *ProviderAuth
	route := req.Route
	if req.APIKey != "" {
		route = RouteAPIKey
		auth = &ProviderAuth{Token: req.APIKey, AuthType: AuthXAPIKey, Source: "api-key", BaseURL: req.BaseURL}
	} else {
		if route == "" {
			route = s.cfg.ModelRoute(DefaultStandardClaudeModel)
		}
		auth = s.authResolver.ResolveRoute(r.Context(), route)
	}
	writeJSON(w, testClaudeCredential(r.Context(), auth, route))
}

func (s *AdminServer) handleModels(w http.ResponseWriter, r *http.Request) {
	s.cfg.mu.RLock()
	models := append([]ModelEntry(nil), s.cfg.Claude.Models...)
	s.cfg.mu.RUnlock()
	writeJSON(w, models)
}

func (s *AdminServer) handleAddModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model       string `json:"model"`
		Route       string `json:"route"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.AddModel(req.Model, req.Route, req.DisplayName) {
		http.Error(w, "model is required", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleRemoveModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.DeleteModel(req.Model) {
		http.Error(w, "model not found", http.StatusNotFound)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleModelRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Model string `json:"model"`
		Route string `json:"route"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.SetModelRoute(req.Model, req.Route) {
		http.Error(w, "invalid model or route", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *AdminServer) handleDiscoverModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Route string `json:"route"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Route != "" && !isValidRoute(req.Route) {
		http.Error(w, "invalid route", http.StatusBadRequest)
		return
	}

	auth, route := s.authResolver.ResolveDiscoveryAuth(r.Context(), req.Route)
	models, err := discoverClaudeModels(r.Context(), auth)
	if err != nil && req.Route == "" && route == RouteLocal {
		fallback := s.authResolver.ResolveRoute(r.Context(), RouteAPIKey)
		if fallback.Valid() {
			if fallbackModels, fallbackErr := discoverClaudeModels(r.Context(), fallback); fallbackErr == nil {
				auth, route, models, err = fallback, RouteAPIKey, fallbackModels, nil
			}
		}
	}
	if err != nil {
		writeJSONStatus(w, http.StatusBadGateway, map[string]any{"status": "error", "message": err.Error(), "route": route})
		return
	}
	added, updated := s.cfg.MergeDiscoveredModels(models, route, time.Now())
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	source := ""
	if auth != nil {
		source = auth.Source
	}
	writeJSON(w, map[string]any{
		"status":  "ok",
		"route":   route,
		"source":  source,
		"seen":    len(models),
		"added":   added,
		"updated": updated,
		"models":  models,
	})
}

func (s *AdminServer) handleRedirects(w http.ResponseWriter, r *http.Request) {
	s.cfg.mu.RLock()
	redirects := make(map[string]string, len(s.cfg.ModelRedirects))
	for k, v := range s.cfg.ModelRedirects {
		redirects[k] = v
	}
	s.cfg.mu.RUnlock()
	writeJSON(w, redirects)
}

func (s *AdminServer) handleSetRedirect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !s.cfg.SetModelRedirect(req.From, req.To) {
		http.Error(w, "from is required", http.StatusBadRequest)
		return
	}
	if err := s.cfg.Save(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func parseStatsFilter(r *http.Request) StatsFilter {
	q := r.URL.Query()
	provider := q.Get("provider")
	if provider == "" {
		provider = "anthropic"
	}
	filter := StatsFilter{Provider: provider, Route: q.Get("route"), Model: q.Get("model")}
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = t
		}
	}
	if until := q.Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			filter.Until = t
		}
	}
	return filter
}

func countRoutes(models []ModelEntry) (local, apikey, total int) {
	total = len(models)
	for _, m := range models {
		switch strings.ToLower(m.Route) {
		case RouteAPIKey:
			apikey++
		default:
			local++
		}
	}
	return local, apikey, total
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
