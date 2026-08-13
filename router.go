package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type ClaudeRouter struct {
	cfg          *Config
	handler      *ClaudeHandler
	logger       *RequestLogger
	authResolver *ClaudeAuthResolver
}

func NewClaudeRouter(cfg *Config, logger *RequestLogger, authResolver *ClaudeAuthResolver) *ClaudeRouter {
	retryer := NewRetryer(cfg.Retry.MaxAttempts, cfg.Retry.InitialDelay)
	return &ClaudeRouter{
		cfg:          cfg,
		handler:      NewClaudeHandler(cfg, retryer, logger),
		logger:       logger,
		authResolver: authResolver,
	}
}

func (rt *ClaudeRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	log.Infof("[REQ] %s %s", r.Method, r.URL.Path)

	switch r.URL.Path {
	case "/healthz":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
		return
	case "/v1/messages", "/v1/messages/count_tokens":
		if r.Method != http.MethodPost {
			writeProxyError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		rt.handleClaude(w, r, start)
		return
	default:
		writeProxyError(w, http.StatusNotFound, "not_found", "claude-proxy only handles /v1/messages and /v1/messages/count_tokens")
		return
	}
}

func (rt *ClaudeRouter) handleClaude(w http.ResponseWriter, r *http.Request, start time.Time) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, "proxy_error", "failed to read request body")
		return
	}

	model := gjson.GetBytes(bodyBytes, "model").String()
	if model == "" {
		model = DefaultStandardClaudeModel
		bodyBytes, _ = sjson.SetBytes(bodyBytes, "model", model)
	}
	if target, redirected := rt.cfg.ResolveModelRedirect(model); redirected {
		log.Infof("[REDIRECT] %s -> %s", model, target)
		model = target
		bodyBytes, _ = sjson.SetBytes(bodyBytes, "model", model)
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	auth, resolvedRoute := rt.authResolver.Resolve(r.Context(), model)
	if auth == nil || !auth.Valid() {
		msg := "no Claude credentials available"
		if auth != nil && auth.Error != nil {
			msg = auth.Error.Error()
		}
		log.Warnf("[AUTH] no auth available for %s (route=%s): %s", model, resolvedRoute, msg)
		routeLabel := resolvedRoute
		if routeLabel == "" {
			routeLabel = "none"
		}
		id := rt.logger.LogRequestWithCache(model, "anthropic", routeLabel, r.URL.Path, start, detectCacheTTL(bodyBytes))
		rt.logger.RecordResultID(id, model, http.StatusServiceUnavailable, TokenUsage{}, 0, msg, string(bodyBytes), "")
		writeProxyError(w, http.StatusServiceUnavailable, "auth_error", msg)
		return
	}

	routeLabel := resolvedRoute + "/" + auth.Source
	log.Infof("[CLAUDE] %s -> %s (%s)", model, r.URL.Path, routeLabel)
	id := rt.logger.LogRequestWithCache(model, "anthropic", routeLabel, r.URL.Path, start, detectCacheTTL(bodyBytes))
	rt.handler.Handle(w, r, bodyBytes, auth, id)
}

func writeProxyError(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"type": typ, "message": message},
	})
}
